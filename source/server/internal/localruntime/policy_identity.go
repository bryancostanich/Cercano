package localruntime

import (
	"context"
	"errors"
	"strings"
)

// ModelAtEndpoint resolves a supervised OpenAI-compatible endpoint to the
// actual loaded catalog model. In particular, mistral.rs's "default" wire alias
// cannot authorize a different model after a runtime restart or reconfiguration.
func ModelAtEndpoint(ctx context.Context, manager Manager, runtime, endpoint string) (string, error) {
	if manager == nil {
		return "", errors.New("runtime manager unavailable")
	}
	instances, err := manager.Instances(ctx)
	if err != nil {
		return "", err
	}
	model := ""
	for _, instance := range instances {
		if instance.Runtime != runtime || strings.TrimRight(instance.Endpoint, "/")+"/v1" != endpoint {
			continue
		}
		if instance.State != InstanceRunning && instance.State != InstanceHealthy {
			continue
		}
		if instance.ModelID == "" || (model != "" && model != instance.ModelID) {
			return "", errors.New("ambiguous running model")
		}
		model = instance.ModelID
	}
	if model == "" {
		return "", errors.New("running model not found")
	}
	return model, nil
}
