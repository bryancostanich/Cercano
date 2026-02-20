package adapters

import (
	"fmt"
	"iter"
	"os"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	agentmod "cercano/source/server/internal/agent"
	"cercano/source/server/internal/tools"
)

// Session state keys used to coordinate state between adapters and the coordinator.
const (
	// StateKeyUseCloud signals the GeneratorAgent to use the cloud provider.
	StateKeyUseCloud = "cercano:use_cloud"
	// StateKeyValidationFailures tracks consecutive validation failures.
	StateKeyValidationFailures = "cercano:validation_failures"
	// StateKeyTargetPath is the absolute path the ValidatorAgent writes generated code to.
	StateKeyTargetPath = "cercano:target_path"
	// StateKeyInputCode is the initial code context passed to the first generation.
	StateKeyInputCode = "cercano:input_code"
	// StateKeyLastGeneratedCode is the raw output from the most recent GeneratorAgent run.
	StateKeyLastGeneratedCode = "cercano:last_generated_code"
	// StateKeyLastValidationError is the error text from the most recent ValidatorAgent failure.
	StateKeyLastValidationError = "cercano:last_validation_error"
)

// NewGeneratorAgent returns an ADK agent that calls a ModelProvider.
//
// Provider selection:
//   - Reads StateKeyUseCloud from session state; if true and cloud != nil, uses cloud.
//   - Otherwise uses local.
//
// Prompt building:
//   - First iteration (no prior state): builds a generate prompt from UserContent + StateKeyInputCode.
//   - Subsequent iterations: builds a fix prompt from StateKeyLastGeneratedCode + StateKeyLastValidationError.
//
// State written (via event.Actions.StateDelta):
//   - StateKeyLastGeneratedCode = generated output
func NewGeneratorAgent(local, cloud agentmod.ModelProvider) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "generator",
		Description: "Generates code using a model provider",
		Run:         generatorRun(local, cloud),
	})
}

func generatorRun(local, cloud agentmod.ModelProvider) func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			provider := local

			// Switch to cloud if the session state requests it.
			if cloud != nil {
				if v, err := ctx.Session().State().Get(StateKeyUseCloud); err == nil {
					if b, ok := v.(bool); ok && b {
						provider = cloud
					}
				}
			}

			// Build prompt: fix if prior state exists, generate otherwise.
			prompt := buildPrompt(ctx)

			resp, err := provider.Process(ctx, &agentmod.Request{Input: prompt})
			if err != nil {
				yield(nil, fmt.Errorf("generator: provider %q failed: %w", provider.Name(), err))
				return
			}

			ev := session.NewEvent(ctx.InvocationID())
			ev.LLMResponse.Content = genai.NewContentFromText(resp.Output, genai.RoleModel)
			ev.Actions.StateDelta = map[string]any{
				StateKeyLastGeneratedCode: resp.Output,
			}
			yield(ev, nil)
		}
	}
}

// buildPrompt constructs the correct prompt for the current iteration.
func buildPrompt(ctx agent.InvocationContext) string {
	state := ctx.Session().State()

	prevCode, codeErr := state.Get(StateKeyLastGeneratedCode)
	prevError, errErr := state.Get(StateKeyLastValidationError)

	if codeErr == nil && errErr == nil {
		code, _ := prevCode.(string)
		errMsg, _ := prevError.(string)
		if code != "" && errMsg != "" {
			return fmt.Sprintf(
				"You are an expert Go developer.\n"+
					"The following Go code has errors. Please fix it according to the error message.\n"+
					"Return ONLY the corrected Go code. Do not explain.\n\n"+
					"Code:\n```go\n%s\n```\n\nError:\n%s",
				code, errMsg,
			)
		}
	}

	// Initial generation.
	var instruction string
	if uc := ctx.UserContent(); uc != nil {
		for _, part := range uc.Parts {
			instruction += part.Text
		}
	}

	inputCode := ""
	if raw, err := state.Get(StateKeyInputCode); err == nil {
		if s, ok := raw.(string); ok {
			inputCode = s
		}
	}

	return fmt.Sprintf(
		"You are an expert Go developer.\n"+
			"Instruction: %s\n"+
			"Return ONLY the requested Go code. Do not include any explanations.\n\n"+
			"Code Context:\n```go\n%s\n```",
		instruction, inputCode,
	)
}

// NewValidatorAgent returns an ADK agent that calls a Validator.
//
// Disk write: if StateKeyTargetPath and StateKeyLastGeneratedCode are both present in
// session state, the agent writes the cleaned code to disk before validating.
//
// On validation success it emits an event with Actions.Escalate = true.
// On failure it increments StateKeyValidationFailures and stores the error text in
// StateKeyLastValidationError. When failures >= escalationThreshold it also sets
// StateKeyUseCloud = true.
func NewValidatorAgent(validator tools.Validator, workDir string, escalationThreshold int) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "validator",
		Description: "Validates generated code and signals escalation",
		Run:         validatorRun(validator, workDir, escalationThreshold),
	})
}

func validatorRun(validator tools.Validator, workDir string, escalationThreshold int) func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			state := ctx.Session().State()

			// Write generated code to disk when the coordinator has provided a target path.
			targetRaw, pathErr := state.Get(StateKeyTargetPath)
			codeRaw, codeErr := state.Get(StateKeyLastGeneratedCode)
			if pathErr == nil && codeErr == nil {
				if targetPath, ok := targetRaw.(string); ok {
					if code, ok := codeRaw.(string); ok {
						clean := tools.CleanMarkdown(code)
						// Best-effort write; a failure will surface as a build error.
						_ = os.WriteFile(targetPath, []byte(clean), 0644)
					}
				}
			}

			err := validator.Validate(ctx, workDir)

			ev := session.NewEvent(ctx.InvocationID())

			if err == nil {
				ev.Actions.Escalate = true
				ev.LLMResponse.Content = genai.NewContentFromText("validation passed", genai.RoleModel)
				yield(ev, nil)
				return
			}

			// Validation failed: update failure counter and optionally set use_cloud.
			failures := 0
			if raw, stateErr := state.Get(StateKeyValidationFailures); stateErr == nil {
				if v, ok := raw.(int); ok {
					failures = v
				}
			}
			failures++

			ev.Actions.StateDelta = map[string]any{
				StateKeyValidationFailures:  failures,
				StateKeyLastValidationError: err.Error(),
			}

			if failures >= escalationThreshold {
				ev.Actions.StateDelta[StateKeyUseCloud] = true
			}

			ev.LLMResponse.Content = genai.NewContentFromText(
				fmt.Sprintf("validation failed: %s", err.Error()),
				genai.RoleModel,
			)
			yield(ev, nil)
		}
	}
}
