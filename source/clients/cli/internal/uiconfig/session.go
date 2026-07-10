package uiconfig

// Launch-session state: a small per-project pointer to the last active
// conversation, saved to <configdir>/session.json so a plain restart can
// auto-resume the working session (and, via the resume path, its sub-agent
// tabs). Client-only, like the rest of uiconfig.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SessionStatePath resolves the session.json path (override with
// CERCANO_SESSION_STATE, mainly for tests).
func SessionStatePath() string {
	if p := os.Getenv("CERCANO_SESSION_STATE"); p != "" {
		return p
	}
	return filepath.Join(configHome(), "session.json")
}

type sessionEntry struct {
	ConversationID string `json:"conversation_id"`
	UpdatedAt      int64  `json:"updated_at"`
}

type sessionFile struct {
	Sessions map[string]sessionEntry `json:"sessions"`
}

// loadSessionFile reads session.json, tolerating a missing or corrupt file by
// returning an empty (but usable) map.
func loadSessionFile() sessionFile {
	f := sessionFile{Sessions: map[string]sessionEntry{}}
	data, err := os.ReadFile(SessionStatePath())
	if err != nil {
		return f
	}
	if json.Unmarshal(data, &f) != nil || f.Sessions == nil {
		return sessionFile{Sessions: map[string]sessionEntry{}}
	}
	return f
}

// LoadLastConversation returns the last active conversation id saved for
// projectDir, or ("", false) if none is recorded.
func LoadLastConversation(projectDir string) (string, bool) {
	if projectDir == "" {
		return "", false
	}
	e, ok := loadSessionFile().Sessions[projectDir]
	if !ok || e.ConversationID == "" {
		return "", false
	}
	return e.ConversationID, true
}

// SaveLastConversation records conversationID as the last active conversation
// for projectDir (atomic write; other projects' entries are preserved). A
// blank projectDir or conversationID is a no-op.
func SaveLastConversation(projectDir, conversationID string) error {
	if projectDir == "" || conversationID == "" {
		return nil
	}
	f := loadSessionFile()
	f.Sessions[projectDir] = sessionEntry{ConversationID: conversationID, UpdatedAt: time.Now().Unix()}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(SessionStatePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "session-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, SessionStatePath())
}
