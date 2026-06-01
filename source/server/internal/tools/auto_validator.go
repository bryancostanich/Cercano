package tools

import (
	"context"

	"cercano/source/server/internal/projectconfig"
)

// ConfigLoader loads project configuration for a given workDir.
type ConfigLoader interface {
	Load(workDir string) (projectconfig.Config, error)
}

// KindToValidator maps a detected ProjectKind to the sub-validator that runs for it.
type KindToValidator map[ProjectKind]Validator

// AutoValidator detects the project type in workDir and dispatches to the
// appropriate sub-validator, honoring overrides from .cercano/config.yaml.
type AutoValidator struct {
	loader ConfigLoader
	subs   KindToValidator
}

// NewAutoValidator wires up an AutoValidator with the given config loader and
// sub-validator map. Use DefaultKindToValidator() to get the built-in mapping.
func NewAutoValidator(loader ConfigLoader, subs KindToValidator) *AutoValidator {
	return &AutoValidator{loader: loader, subs: subs}
}

// DefaultKindToValidator returns the built-in mapping used by the production binaries.
func DefaultKindToValidator() KindToValidator {
	return KindToValidator{
		KindGo:             NewGoValidator(),
		KindRust:           NewRustValidator(),
		KindDotnetSolution: NewDotnetValidator(),
		KindDotnetProject:  NewDotnetValidator(),
		KindNode:           NewNodeValidator(),
	}
}

func (v *AutoValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	cfg, err := v.loader.Load(workDir)
	if err != nil {
		return Failed, err
	}
	if cfg.Validator.Skip {
		return Skipped, &SkipReason{Reason: "validation skipped per .cercano/config.yaml"}
	}
	if cfg.Validator.Command != "" {
		return NewCustomValidator(cfg.Validator.Command).Validate(ctx, workDir)
	}

	kind, derr := Detect(workDir)
	if derr != nil {
		return Skipped, &SkipReason{Reason: "could not read workDir for manifest detection: " + derr.Error()}
	}
	if kind == KindUnknown {
		return Skipped, &SkipReason{Reason: "no recognized project manifest in " + workDir + "; validation skipped — set validator.command in .cercano/config.yaml to enable"}
	}
	sub, ok := v.subs[kind]
	if !ok {
		return Skipped, &SkipReason{Reason: "no validator registered for project kind " + kind.String()}
	}
	return sub.Validate(ctx, workDir)
}

// loaderFunc adapts projectconfig.Load to the ConfigLoader interface.
type loaderFunc func(string) (projectconfig.Config, error)

func (f loaderFunc) Load(workDir string) (projectconfig.Config, error) { return f(workDir) }

// DefaultLoader returns the production ConfigLoader.
func DefaultLoader() ConfigLoader { return loaderFunc(projectconfig.Load) }
