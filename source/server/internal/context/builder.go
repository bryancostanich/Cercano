package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const contextTemplate = `You are analyzing a software project to build a concise reference document.
Your output will be prepended to future AI queries about this project, so focus on information that would help understand domain-specific code, data structures, protocols, and conventions.

Write a markdown document with these sections (skip any that don't apply):
- **Overview**: 1-2 paragraphs on what this project is and does
- **Architecture**: Key components and how they interact
- **Key Data Structures**: Important types, structs, schemas with field descriptions
- **APIs & Protocols**: Endpoints, wire formats, message types
- **Conventions**: Naming patterns, important constants, gotchas
- **File Layout**: Where key code lives

Rules:
- Be concise — target 2000-3000 words maximum
- Focus on domain-specific knowledge that a generic model wouldn't know
- Include struct field offsets, enum values, and magic numbers when present
- Skip boilerplate and obvious things
- Output ONLY the markdown document, no preamble

Here are the project files I found:

`

// Builder constructs a context.md from discovered files and optional host context.
type Builder struct{}

// NewBuilder creates a new Builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// BuildPrompt constructs the prompt to send to the local model for context generation.
// Returns the prompt string and a summary of what files were included.
func (b *Builder) BuildPrompt(files []DiscoveredFile, hostContext string) (prompt string, filesSummary string) {
	var sb strings.Builder
	sb.WriteString(contextTemplate)

	var summaryParts []string
	totalSize := 0
	maxPromptSize := 48 * 1024 // 48KB max prompt to leave room for model context

	for _, f := range files {
		entry := fmt.Sprintf("### File: %s\n```\n%s\n```\n\n", f.RelPath, f.Content)
		if totalSize+len(entry) > maxPromptSize {
			break
		}
		sb.WriteString(entry)
		summaryParts = append(summaryParts, f.RelPath)
		totalSize += len(entry)
	}

	if hostContext != "" {
		sb.WriteString("### Additional context provided by the host AI:\n")
		sb.WriteString(hostContext)
		sb.WriteString("\n\n")
	}

	return sb.String(), fmt.Sprintf("Scanned %d files: %s", len(summaryParts), strings.Join(summaryParts, ", "))
}

// WriteContext writes the generated context to .cercano/context.md in the project dir.
func (b *Builder) WriteContext(projectDir, content string) error {
	dir := filepath.Join(projectDir, CercanoDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create .cercano directory: %w", err)
	}
	path := ContextPath(projectDir)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write context file: %w", err)
	}
	return nil
}
