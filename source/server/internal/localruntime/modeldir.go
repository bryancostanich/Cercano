package localruntime

import "strings"

// ModelDirName turns a model identifier into a filesystem-safe directory name
// for that model's own subdirectory under a configured model dir.
//
// Downloaded models each get their own subdirectory (the uniform per-model
// layout): a GGUF lands at modelDir/<ModelDirName(id)>/<file>.gguf, and a
// multi-file safetensors/UQFF model puts all its files — including generically
// named ones like config.json and tokenizer.json — in that same directory, so
// two models can never collide on a shared filename. The download manager
// needs no file-vs-dir branch: it writes each file into filepath.Dir(Path),
// which is this subdirectory.
//
// The mapping keeps [A-Za-z0-9-_.] and replaces every other rune with '-', then
// trims leading/trailing '-' and '.' so the result is never hidden or empty.
func ModelDirName(id string) string {
	id = strings.TrimSpace(id)
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "model"
	}
	return out
}
