package agenttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	res, err := WriteFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","content":"hello world\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != ResultText {
		t.Errorf("type want text got %v", res.Type)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world\n" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestWriteFile_MkdirParents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c.txt")
	if _, err := WriteFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","content":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("nested file not created: %v", err)
	}
}

func TestEditFile_ExactMatchReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("alpha beta gamma\n"), 0o644)
	if _, err := EditFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","old_string":"beta","new_string":"BETA"}`)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha BETA gamma\n" {
		t.Errorf("replace wrong: %q", string(data))
	}
}

func TestEditFile_RefusesAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("foo foo foo\n"), 0o644)
	_, err := EditFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","old_string":"foo","new_string":"bar"}`))
	if err == nil {
		t.Fatal("expected ambiguous-match error")
	}
	if !strings.Contains(err.Error(), "3 times") {
		t.Errorf("expected count in error message, got: %v", err)
	}
}

func TestEditFile_RefusesZeroMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("alpha\n"), 0o644)
	_, err := EditFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","old_string":"zzz","new_string":"www"}`))
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestEditFile_RefusesNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("alpha\n"), 0o644)
	_, err := EditFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","old_string":"alpha","new_string":"alpha"}`))
	if err == nil || !strings.Contains(err.Error(), "no-op") {
		t.Errorf("expected no-op refusal, got: %v", err)
	}
}

func TestRmFile_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("x"), 0o644)
	if _, err := RmFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file not removed: stat err = %v", err)
	}
}

func TestRmFile_RefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := RmFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`"}`))
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory refusal, got: %v", err)
	}
}

func TestRmFile_PermissionIsX(t *testing.T) {
	if RmFile().Permission() != PermX {
		t.Errorf("rm_file must be X-tier, got %v", RmFile().Permission())
	}
}

func TestRunCommand_CapturesStdoutAndExit(t *testing.T) {
	res, err := RunCommand().Execute(context.Background(),
		json.RawMessage(`{"cmd":["echo","hello cercano"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "hello cercano") {
		t.Errorf("stdout missing from result: %q", res.Text)
	}
	if !strings.Contains(res.Text, "exit=0") {
		t.Errorf("exit code missing: %q", res.Text)
	}
}

func TestRunCommand_NonZeroExit(t *testing.T) {
	res, err := RunCommand().Execute(context.Background(),
		json.RawMessage(`{"cmd":["sh","-c","exit 3"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "exit=3") {
		t.Errorf("non-zero exit not captured: %q", res.Text)
	}
}

func TestRunCommand_Timeout(t *testing.T) {
	_, err := RunCommand().Execute(context.Background(),
		json.RawMessage(`{"cmd":["sleep","2"],"timeout_seconds":1}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestGitAdd_RequiresPaths(t *testing.T) {
	_, err := GitAdd().Execute(context.Background(),
		json.RawMessage(`{"paths":[]}`))
	if err == nil {
		t.Errorf("expected empty-paths error")
	}
}

func TestGitCommit_RequiresMessage(t *testing.T) {
	_, err := GitCommit().Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "message") {
		t.Errorf("expected message-required error, got %v", err)
	}
}

func TestGitResetHard_RequiresRevision(t *testing.T) {
	_, err := GitResetHard().Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "revision") {
		t.Errorf("expected revision-required error, got %v", err)
	}
}

func TestGitPush_DefaultArgsCompile(t *testing.T) {
	// We can't actually push from a unit test, but we can verify the tool
	// accepts an empty arg blob and only fails at exec time (when there's
	// no git repo / remote to talk to). This confirms the arg parser path
	// is permissive about defaults.
	_, err := GitPush().Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Skip("git push from a non-repo cwd unexpectedly succeeded; skipping")
	}
	// Expect either a git error (not a repo) or a remote error — both are
	// fine. What we DON'T want is a parse error.
	if strings.Contains(err.Error(), "parse args") {
		t.Errorf("default args should parse cleanly, got: %v", err)
	}
}

func TestRunCommand_EnvOverridePassesThrough(t *testing.T) {
	// Run a tiny shell snippet that prints an env var; verify our override
	// reaches the child.
	res, err := RunCommand().Execute(context.Background(),
		json.RawMessage(`{"cmd":["sh","-c","echo $CERCANO_TEST_VAR"],"env":{"CERCANO_TEST_VAR":"hello-from-test"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "hello-from-test") {
		t.Errorf("env override didn't reach child: %q", res.Text)
	}
}

func TestRunCommand_TruncatesLongOutput(t *testing.T) {
	// Generate ~30 KiB to verify the per-stream 16 KiB cap kicks in.
	res, err := RunCommand().Execute(context.Background(),
		json.RawMessage(`{"cmd":["sh","-c","yes hello | head -c 30000"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Errorf("expected truncation marker, got %d bytes", len(res.Text))
	}
}

func TestPermissionTiers(t *testing.T) {
	cases := []struct {
		t    Tool
		want Permission
	}{
		{WriteFile(), PermW},
		{EditFile(), PermW},
		{RunCommand(), PermW},
		{GitAdd(), PermW},
		{GitCommit(), PermW},
		{RmFile(), PermX},
		{GitPush(), PermX},
		{GitResetHard(), PermX},
	}
	for _, c := range cases {
		if got := c.t.Permission(); got != c.want {
			t.Errorf("%s.Permission() = %v, want %v", c.t.Name(), got, c.want)
		}
	}
}
