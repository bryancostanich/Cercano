package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	agentmod "cercano/source/server/internal/agent"
	"cercano/source/server/internal/loop/adapters"
	"cercano/source/server/internal/tools"
)

// ADKCoordinator implements agent.Coordinator using an ADK LoopAgent.
type ADKCoordinator struct {
	localProvider       agentmod.ModelProvider
	cloudProvider       agentmod.ModelProvider
	validator           tools.Validator
	maxRetries          int
	escalationThreshold int
}

// NewADKCoordinator creates an ADKCoordinator with local and cloud providers.
func NewADKCoordinator(local, cloud agentmod.ModelProvider, val tools.Validator) *ADKCoordinator {
	return &ADKCoordinator{
		localProvider:       local,
		cloudProvider:       cloud,
		validator:           val,
		maxRetries:          3,
		escalationThreshold: 2,
	}
}

// SetEscalationThreshold sets the number of validation failures after which to
// switch from the local provider to the cloud provider.
func (c *ADKCoordinator) SetEscalationThreshold(threshold int) {
	c.escalationThreshold = threshold
}

// Coordinate runs the generate→validate loop using an ADK LoopAgent.
// It satisfies the agent.Coordinator interface.
func (c *ADKCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress agentmod.ProgressFunc) (*agentmod.Response, error) {
	if progress == nil {
		progress = func(string) {}
	}

	// 1. Filename inference — ask the local model which file to target.
	progress("Planning: Identifying target file...")
	inferPrompt := fmt.Sprintf(
		"Based on the instruction '%s' and the current file '%s', what is the single filename that should be modified or created? Return ONLY the filename.",
		instruction, fileName,
	)
	if resp, err := c.localProvider.Process(ctx, &agentmod.Request{Input: inferPrompt}); err == nil {
		name := strings.TrimSpace(resp.Output)
		if name != "" && !strings.Contains(name, " ") && strings.Contains(name, ".") && name != fileName {
			fmt.Printf(">> ADKCoordinator: Inferred target file '%s' (was '%s')\n", name, fileName)
			fileName = name
		}
	}

	targetPath := filepath.Join(workDir, fileName)

	// 2. Backup the original file (if it exists) so we can restore it afterwards.
	var backupContent []byte
	backupExists := false
	if content, err := os.ReadFile(targetPath); err == nil {
		backupContent = content
		backupExists = true
		fmt.Printf(">> ADKCoordinator: Created backup of %s\n", fileName)
	}

	restore := func() {
		if backupExists {
			_ = os.WriteFile(targetPath, backupContent, 0644)
			fmt.Printf(">> ADKCoordinator: Restored original %s\n", fileName)
		} else {
			_ = os.Remove(targetPath)
			fmt.Printf(">> ADKCoordinator: Removed temporary %s\n", fileName)
		}
	}

	// 3. Create agents.
	genAgent, err := adapters.NewGeneratorAgent(c.localProvider, c.cloudProvider)
	if err != nil {
		restore()
		return nil, fmt.Errorf("failed to create generator agent: %w", err)
	}

	valAgent, err := adapters.NewValidatorAgent(c.validator, workDir, c.escalationThreshold)
	if err != nil {
		restore()
		return nil, fmt.Errorf("failed to create validator agent: %w", err)
	}

	// 4. Wrap in a LoopAgent. MaxIterations = maxRetries + 1.
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(c.maxRetries + 1),
		AgentConfig: adkagent.Config{
			Name:      "generation_loop",
			SubAgents: []adkagent.Agent{genAgent, valAgent},
		},
	})
	if err != nil {
		restore()
		return nil, fmt.Errorf("failed to create loop agent: %w", err)
	}

	// 5. Create an in-memory session with initial state.
	svc := session.InMemoryService()
	const sessionID = "coord-session"
	_, err = svc.Create(ctx, &session.CreateRequest{
		AppName:   "cercano",
		UserID:    "coordinator",
		SessionID: sessionID,
		State: map[string]any{
			adapters.StateKeyTargetPath: targetPath,
			adapters.StateKeyInputCode:  inputCode,
		},
	})
	if err != nil {
		restore()
		return nil, fmt.Errorf("failed to create ADK session: %w", err)
	}

	agentRunner, err := runner.New(runner.Config{
		AppName:        "cercano",
		Agent:          loop,
		SessionService: svc,
	})
	if err != nil {
		restore()
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	// 6. Run the loop, collecting results and reporting progress.
	var succeeded bool
	var lastGeneratedCode string
	var lastValidationError string
	escalated := false

	userContent := genai.NewContentFromText(instruction, genai.RoleUser)

	for event, runErr := range agentRunner.Run(ctx, "coordinator", sessionID, userContent, adkagent.RunConfig{}) {
		if runErr != nil {
			restore()
			return nil, fmt.Errorf("agent loop error: %w", runErr)
		}
		if event == nil {
			continue
		}

		switch event.Author {
		case "generator":
			if event.LLMResponse.Content != nil {
				var code string
				for _, part := range event.LLMResponse.Content.Parts {
					code += part.Text
				}
				if code != "" {
					lastGeneratedCode = code
				}
			}
			progress("Generating code...")

		case "validator":
			if event.Actions.Escalate {
				succeeded = true
				progress("Validation passed.")
			} else {
				if event.LLMResponse.Content != nil {
					for _, part := range event.LLMResponse.Content.Parts {
						lastValidationError = part.Text
					}
				}
				if v, ok := event.Actions.StateDelta[adapters.StateKeyUseCloud]; ok {
					if b, ok := v.(bool); ok && b {
						escalated = true
					}
				}
				progress("Validation failed. Retrying...")
			}
		}
	}

	// 7. Always restore the backup — workspace stays clean for IDE Apply.
	restore()

	// 8. Build and return the response.
	if succeeded {
		cleanCode := tools.CleanMarkdown(lastGeneratedCode)

		chatOutput := lastGeneratedCode
		if !strings.Contains(chatOutput, "```") {
			chatOutput = fmt.Sprintf(
				"I've generated the code for **%s**:\n\n```go\n%s\n```",
				fileName, lastGeneratedCode,
			)
		}

		return &agentmod.Response{
			Output: chatOutput,
			FileChanges: []agentmod.FileChange{
				{Path: fileName, Content: cleanCode, Action: "UPDATE"},
			},
			RoutingMetadata: agentmod.RoutingMetadata{
				Escalated: escalated,
			},
			ValidationErrors: lastValidationError,
		}, nil
	}

	return &agentmod.Response{
		Output:           fmt.Sprintf("Failed to generate valid code after %d attempts.", c.maxRetries+1),
		ValidationErrors: lastValidationError,
	}, nil
}
