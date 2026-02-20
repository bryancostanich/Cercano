package adapters

import (
	"fmt"
	"iter"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	agentmod "cercano/source/server/internal/agent"
	"cercano/source/server/internal/tools"
)

// Session state keys used to coordinate escalation between adapters.
const (
	// StateKeyUseCloud signals the GeneratorAgent to use the cloud provider.
	StateKeyUseCloud = "cercano:use_cloud"
	// StateKeyValidationFailures tracks consecutive validation failures.
	StateKeyValidationFailures = "cercano:validation_failures"
)

// NewGeneratorAgent returns an ADK agent that calls a ModelProvider.
//
// It reads StateKeyUseCloud from session state to choose between local and cloud
// providers. The instruction is extracted from InvocationContext.UserContent().
// If cloud is nil, local is always used regardless of state.
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
				useCloud, err := ctx.Session().State().Get(StateKeyUseCloud)
				if err == nil {
					if v, ok := useCloud.(bool); ok && v {
						provider = cloud
					}
				}
			}

			// Extract instruction from user content.
			var instruction string
			if uc := ctx.UserContent(); uc != nil {
				for _, part := range uc.Parts {
					instruction += part.Text
				}
			}

			resp, err := provider.Process(ctx, &agentmod.Request{Input: instruction})
			if err != nil {
				yield(nil, fmt.Errorf("generator: provider %q failed: %w", provider.Name(), err))
				return
			}

			ev := session.NewEvent(ctx.InvocationID())
			ev.LLMResponse.Content = genai.NewContentFromText(resp.Output, genai.RoleModel)
			yield(ev, nil)
		}
	}
}

// NewValidatorAgent returns an ADK agent that calls a Validator.
//
// On validation success it emits an event with Actions.Escalate = true, which
// causes the enclosing LoopAgent to terminate.
// On failure it increments StateKeyValidationFailures in session state. When
// the count reaches escalationThreshold it also sets StateKeyUseCloud = true.
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
			err := validator.Validate(ctx, workDir)

			ev := session.NewEvent(ctx.InvocationID())

			if err == nil {
				// Validation passed: signal LoopAgent to terminate.
				ev.Actions.Escalate = true
				ev.LLMResponse.Content = genai.NewContentFromText("validation passed", genai.RoleModel)
				yield(ev, nil)
				return
			}

			// Validation failed: read and increment failure counter.
			failures := 0
			if raw, stateErr := ctx.Session().State().Get(StateKeyValidationFailures); stateErr == nil {
				if v, ok := raw.(int); ok {
					failures = v
				}
			}
			failures++

			ev.Actions.StateDelta = map[string]any{
				StateKeyValidationFailures: failures,
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
