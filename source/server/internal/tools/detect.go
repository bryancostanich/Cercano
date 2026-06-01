package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProjectKind identifies the build system detected in a directory.
type ProjectKind int

const (
	KindUnknown ProjectKind = iota
	KindGo
	KindRust
	KindDotnetSolution
	KindDotnetProject
	KindNode
)

func (k ProjectKind) String() string {
	switch k {
	case KindGo:
		return "go"
	case KindRust:
		return "rust"
	case KindDotnetSolution:
		return "dotnet-solution"
	case KindDotnetProject:
		return "dotnet-project"
	case KindNode:
		return "node"
	default:
		return "unknown"
	}
}

// Detect scans workDir (non-recursive) for a recognized project manifest.
// Precedence (first match wins): Cargo.toml > go.mod > *.sln > *.fsproj/*.csproj
// > package.json (only if scripts.build is non-empty).
func Detect(workDir string) (ProjectKind, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return KindUnknown, err
	}

	hasCargo, hasGoMod, hasSln, hasDotnetProj, hasPackageJSON := false, false, false, false, false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case name == "Cargo.toml":
			hasCargo = true
		case name == "go.mod":
			hasGoMod = true
		case filepath.Ext(name) == ".sln":
			hasSln = true
		case filepath.Ext(name) == ".fsproj", filepath.Ext(name) == ".csproj":
			hasDotnetProj = true
		case name == "package.json":
			hasPackageJSON = true
		}
	}

	switch {
	case hasCargo:
		return KindRust, nil
	case hasGoMod:
		return KindGo, nil
	case hasSln:
		return KindDotnetSolution, nil
	case hasDotnetProj:
		return KindDotnetProject, nil
	case hasPackageJSON && nodeHasBuildScript(filepath.Join(workDir, "package.json")):
		return KindNode, nil
	default:
		return KindUnknown, nil
	}
}

func nodeHasBuildScript(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	build, ok := pkg.Scripts["build"]
	return ok && build != ""
}
