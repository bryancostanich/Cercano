package agent

import (
	"context"
	"fmt"
	"strings"
)

// ProgressFunc defines a callback for progress updates.
type ProgressFunc func(message string)

// Coordinator defines the interface for the iterative generation loop.
type Coordinator interface {
	Coordinate(ctx context.Context, instruction, inputCode, workDir, fileName string, progress ProgressFunc) (*Response, error)
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
	provider, err := a.router.SelectProvider(req, intent)
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
		targetFile := req.FileName
		// If user asks for unit tests specifically, ensure we are targeting a _test.go file
		lowerInput := strings.ToLower(req.Input)
		if (strings.Contains(lowerInput, "unit test") || strings.Contains(lowerInput, "unit tests")) && !strings.HasSuffix(targetFile, "_test.go") {
			targetFile = strings.TrimSuffix(targetFile, ".go") + "_test.go"
			fmt.Printf("Agent: Adjusting target filename for unit test generation: %s\n", targetFile)
		}

		fmt.Printf("Agent: Detected Coding intent. Executing Coordinator Loop in %s for %s...\n", req.WorkDir, targetFile)
		// Coordinate takes (ctx, instruction, inputCode, workDir, fileName, progress)
		// We pass nil for progress in unary calls for now.
		res, err := a.coordinator.Coordinate(ctx, req.Input, "", req.WorkDir, targetFile, nil)
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
	
	if len(res.FileChanges) > 0 {
		fmt.Printf("Agent: WARNING - Provider %s returned %d file changes for non-coordinator request. Clearing them.\n", provider.Name(), len(res.FileChanges))
		res.FileChanges = nil
	}
	
	res.RoutingMetadata = RoutingMetadata{
		ModelName:  provider.Name(),
		Confidence: 1.0, // Initial simple value
	}
	return res, nil
}

// ProcessRequestStream orchestrates the flow with progress updates.
func (a *Agent) ProcessRequestStream(ctx context.Context, req *Request, progress ProgressFunc) (*Response, error) {
	if progress == nil {
		progress = func(string) {}
	}

	// 1. Classify Intent
	intent, err := a.router.ClassifyIntent(req)
	if err != nil {
		return nil, fmt.Errorf("failed to classify intent: %w", err)
	}
	progress(fmt.Sprintf("Classifying Intent... %s", intent))

	// 2. Select Provider
	provider, err := a.router.SelectProvider(req, intent)
	if err != nil {
		return nil, fmt.Errorf("failed to select provider: %w", err)
	}
	progress(fmt.Sprintf("Selecting Provider... %s", provider.Name()))

	// Explicit override: If user says "use cloud", force CloudModel
	if strings.Contains(strings.ToLower(req.Input), "use cloud") {
		fmt.Println("Agent: Explicit 'use cloud' detected. Overriding routing to CloudModel.")
		if cloud, ok := a.router.GetModelProviders()["CloudModel"]; ok {
			provider = cloud
			progress("Selecting Provider... CloudModel (Override)")
		}
	}

	// 3. Execute Strategy
	if intent == IntentCoding && req.WorkDir != "" && req.FileName != "" {
		fmt.Printf("Agent: Detected Coding intent. Executing Coordinator Loop in %s for %s...\n", req.WorkDir, req.FileName)
		progress(fmt.Sprintf("Generating and Validating Code (%s)...", provider.Name()))
		res, err := a.coordinator.Coordinate(ctx, req.Input, "", req.WorkDir, req.FileName, progress)
		if err != nil {
			return nil, fmt.Errorf("agentic loop failed: %w", err)
		}
		// Merge metadata from routing if needed
		res.RoutingMetadata = RoutingMetadata{
			ModelName:  provider.Name(),
			Confidence: 1.0, // Initial simple value
			Escalated:  res.RoutingMetadata.Escalated, // Preserve from coordinator
		}
		return res, nil
	}

	progress(fmt.Sprintf("Generating Response (%s)...", provider.Name()))
	fmt.Printf("Agent: Executing direct call with provider: %s\n", provider.Name())
	res, err := provider.Process(ctx, req)
	if err != nil {
		return nil, err
	}
	
	if len(res.FileChanges) > 0 {
		fmt.Printf("Agent: WARNING - Provider %s returned %d file changes for non-coordinator request. Clearing them.\n", provider.Name(), len(res.FileChanges))
		res.FileChanges = nil
	}
	
	res.RoutingMetadata = RoutingMetadata{
		ModelName:  provider.Name(),
		Confidence: 1.0, // Initial simple value
	}
	progress(fmt.Sprintf("Generating Response (%s)... Done.", provider.Name()))
	return res, nil
}
