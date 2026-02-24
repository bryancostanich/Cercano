package loop

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	sessionService      session.Service
	maxRetries          int
	escalationThreshold int
}

// NewADKCoordinator creates an ADKCoordinator with local and cloud providers.
func NewADKCoordinator(local, cloud agentmod.ModelProvider, val tools.Validator, sessionSvc session.Service) *ADKCoordinator {
	return &ADKCoordinator{
		localProvider:       local,
		cloudProvider:       cloud,
		validator:           val,
		sessionService:      sessionSvc,
		maxRetries:          3,
		escalationThreshold: 2,
	}
}

// SetEscalationThreshold sets the number of validation failures after which to
// switch from the local provider to the cloud provider.
func (c *ADKCoordinator) SetEscalationThreshold(threshold int) {
	c.escalationThreshold = threshold
}

// SetCloudProvider replaces the cloud provider at runtime.
func (c *ADKCoordinator) SetCloudProvider(p agentmod.ModelProvider) {
	c.cloudProvider = p
}

// CoordinateStream sets up the generate→validate loop and returns an event
// iterator plus a finalize closure. The caller drains the iterator to drive the
// loop, then calls finalize to restore the workspace backup and obtain the
// final response.
func (c *ADKCoordinator) CoordinateStream(ctx context.Context, instruction, inputCode, workDir, fileName string) (
	iter.Seq2[*session.Event, error], func() (*agentmod.Response, error), error,
) {
	// 1. Filename inference — ask the local model which file to target.
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
		return nil, nil, fmt.Errorf("failed to create generator agent: %w", err)
	}

	valAgent, err := adapters.NewValidatorAgent(c.validator, workDir, c.escalationThreshold)
	if err != nil {
		restore()
		return nil, nil, fmt.Errorf("failed to create validator agent: %w", err)
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
		return nil, nil, fmt.Errorf("failed to create loop agent: %w", err)
	}

	// 5. Create a session with initial state using the shared service.
	sessionID := fmt.Sprintf("coord-%d", time.Now().UnixNano())
	_, err = c.sessionService.Create(ctx, &session.CreateRequest{
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
		return nil, nil, fmt.Errorf("failed to create ADK session: %w", err)
	}

	agentRunner, err := runner.New(runner.Config{
		AppName:        "cercano",
		Agent:          loop,
		SessionService: c.sessionService,
	})
	if err != nil {
		restore()
		return nil, nil, fmt.Errorf("failed to create runner: %w", err)
	}

	// Accumulated state from the event stream.
	var succeeded bool
	var lastGeneratedCode string
	var lastValidationError string
	escalated := false
	var streamErr error

	userContent := genai.NewContentFromText(instruction, genai.RoleUser)

	// 6. Build the event iterator that wraps agentRunner.Run and accumulates state.
	events := func(yield func(*session.Event, error) bool) {
		for event, runErr := range agentRunner.Run(ctx, "coordinator", sessionID, userContent, adkagent.RunConfig{}) {
			if runErr != nil {
				streamErr = fmt.Errorf("agent loop error: %w", runErr)
				yield(nil, streamErr)
				return
			}
			if event == nil {
				continue
			}

			// Accumulate state from each event.
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
			case "validator":
				if event.Actions.Escalate {
					succeeded = true
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
				}
			}

			if !yield(event, nil) {
				return
			}
		}
	}

	// 7. Finalize closure: restores the backup and builds the response.
	finalize := func() (*agentmod.Response, error) {
		restore()

		if streamErr != nil {
			return nil, streamErr
		}

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

	return events, finalize, nil
}

// Coordinate runs the generate→validate loop using an ADK LoopAgent.
// It satisfies the agent.Coordinator interface by delegating to CoordinateStream.
func (c *ADKCoordinator) Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress agentmod.ProgressFunc) (*agentmod.Response, error) {
	if progress == nil {
		progress = func(string) {}
	}

	progress("Planning: Identifying target file...")

	events, finalize, err := c.CoordinateStream(ctx, instruction, inputCode, workDir, fileName)
	if err != nil {
		return nil, err
	}

	for event, runErr := range events {
		if runErr != nil {
			return nil, runErr
		}
		if msg := agentmod.MapEventToProgress(event); msg != "" {
			progress(msg)
		}
	}

	return finalize()
}
