package agent

import (
	"context"
	"fmt"
	"strings"
)

// Coordinator defines the interface for the iterative generation loop.
type Coordinator interface {
	Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string) (*Response, error)
}

// Agent is the top-level orchestrator for AI requests.
type Agent struct {
	router      Router
	coordinator Coordinator
}

// NewAgent creates a new Agent orchestrator.
func NewAgent(r Router, c Coordinator) *Agent {
	return &Agent{
		router:      r,
		coordinator: c,
	}
}

// ProcessRequest orchestrates the flow: Route -> Classify -> Execute Strategy.
func (a *Agent) ProcessRequest(ctx context.Context, req *Request) (*Response, error) {
	// 1. Classify Intent
	intent, err := a.router.ClassifyIntent(req)
	if err != nil {
		return nil, fmt.Errorf("failed to classify intent: %w", err)
	}

	// 2. Select Provider
	provider, err := a.router.SelectProvider(req)
	if err != nil {
		return nil, fmt.Errorf("failed to select provider: %w", err)
	}

	// Explicit override: If user says "use cloud", force CloudModel
	if strings.Contains(strings.ToLower(req.Input), "use cloud") {
		fmt.Println("Agent: Explicit 'use cloud' detected. Overriding routing to CloudModel.")
		if cloud, ok := a.router.GetModelProviders()["CloudModel"]; ok {
			provider = cloud
		}
	}

	// 3. Execute Strategy
	if intent == IntentCoding && req.WorkDir != "" && req.FileName != "" {
		fmt.Printf("Agent: Detected Coding intent. Executing Coordinator Loop in %s for %s...\n", req.WorkDir, req.FileName)
		// Coordinate takes (ctx, instruction, inputCode, workDir, fileName)
		// We pass empty inputCode for now as instruction contains the full prompt.
		res, err := a.coordinator.Coordinate(ctx, req.Input, "", req.WorkDir, req.FileName)
		if err != nil {
			return nil, fmt.Errorf("agentic loop failed: %w", err)
		}
		// Merge metadata from routing if needed
		res.RoutingMetadata = RoutingMetadata{
			ModelName:  provider.Name(),
			Confidence: 1.0, // Initial simple value
		}
		return res, nil
	}

	fmt.Printf("Agent: Executing direct call with provider: %s\n", provider.Name())
	res, err := provider.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	res.RoutingMetadata = RoutingMetadata{
		ModelName:  provider.Name(),
		Confidence: 1.0, // Initial simple value
	}
	return res, nil
}
