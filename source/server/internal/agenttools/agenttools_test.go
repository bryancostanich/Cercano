package agenttools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(ReadFile()); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("Read")
	if !ok || got.Name() != "Read" {
		t.Errorf("Get(Read): ok=%v name=%q", ok, got.Name())
	}
	if err := r.Register(ReadFile()); err == nil {
		t.Errorf("expected duplicate registration error")
	}
}

func TestRegistry_All_SortedByName(t *testing.T) {
	r := NewRegistry()
	for _, tl := range []Tool{Grep(), ReadFile(), ListDir()} {
		_ = r.Register(tl)
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("want 3 tools, got %d", len(all))
	}
	want := []string{"Grep", "LS", "Read"}
	for i, n := range want {
		if all[i].Name() != n {
			t.Errorf("position %d: want %q got %q", i, n, all[i].Name())
		}
	}
}

func TestRegistry_Filter(t *testing.T) {
	r := DefaultRegistry()
	all := r.All()
	rTier := r.Filter(PermR)
	wTier := r.Filter(PermW)
	xTier := r.Filter(PermX)
	if len(rTier)+len(wTier)+len(xTier) != len(all) {
		t.Errorf("R+W+X (%d+%d+%d) should sum to All (%d)",
			len(rTier), len(wTier), len(xTier), len(all))
	}
	for _, tier := range []struct {
		name  string
		tools []Tool
	}{{"R", rTier}, {"W", wTier}, {"X", xTier}} {
		if len(tier.tools) == 0 {
			t.Errorf("expected at least one %s-tier tool in default registry", tier.name)
		}
	}
}

func TestReadFile_ReturnsText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("hello\nworld\n"), 0o644)

	res, err := ReadFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != ResultText || res.Text != "hello\nworld\n" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestReadFile_LineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644)
	res, err := ReadFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`","start":2,"end":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "b\nc\nd" {
		t.Errorf("line range mismatch: %q", res.Text)
	}
}

func TestReadFile_RefusesBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	_ = os.WriteFile(path, []byte{0, 1, 2, 3, 0, 0xff}, 0o644)
	_, err := ReadFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`"}`))
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("expected binary refusal, got %v", err)
	}
}

func TestReadFile_TruncatesLargeText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	huge := strings.Repeat("abcdefghij\n", 5000) // ~55 KB
	_ = os.WriteFile(path, []byte(huge), 0o644)
	res, err := ReadFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Errorf("expected truncated; got full %d bytes", len(res.Text))
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Errorf("expected truncation suffix in text")
	}
}

func TestListDir_BasicAndSorting(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".hidden"), []byte("h"), 0o644)

	res, err := ListDir().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != ResultRows {
		t.Fatalf("type want rows got %v", res.Type)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 entries (dotfile filtered), got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["type"] != "dir" || res.Rows[0]["name"] != "sub" {
		t.Errorf("dirs should come first; got %+v", res.Rows[0])
	}
}

func TestListDir_HiddenIncluded(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".x"), []byte("x"), 0o644)
	res, _ := ListDir().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`","hidden":true}`))
	if len(res.Rows) != 1 || res.Rows[0]["name"] != ".x" {
		t.Errorf("hidden:true should include dotfile, got %+v", res.Rows)
	}
}

func TestStatFile_MissingPathReportsExistsFalse(t *testing.T) {
	res, err := StatFile().Execute(context.Background(),
		json.RawMessage(`{"path":"/definitely/not/here"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0]["exists"]; got != false {
		t.Errorf("expected exists=false, got %v", got)
	}
}

func TestStatFile_PresentReportsFields(t *testing.T) {
	dir := t.TempDir()
	res, err := StatFile().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	if row["exists"] != true || row["type"] != "dir" {
		t.Errorf("expected dir-exists row, got %+v", row)
	}
}

func TestGrep_NoMatchesReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "x.txt"), []byte("alpha\nbeta\n"), 0o644)
	res, err := Grep().Execute(context.Background(),
		json.RawMessage(`{"pattern":"zzzzzz","path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("expected no rows, got %d: %+v", len(res.Rows), res.Rows)
	}
}

func TestGrep_FindsMatchesWithStructuredRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("hello world\nalpha\nhello\n"), 0o644)
	res, err := Grep().Execute(context.Background(),
		json.RawMessage(`{"pattern":"hello","path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(res.Rows), res.Rows)
	}
	for _, row := range res.Rows {
		if row["path"] == nil || row["line"] == nil || row["content"] == nil {
			t.Errorf("row missing required fields: %+v", row)
		}
	}
}

func TestGlob_BasicPattern(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte{}, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte{}, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "c.go"), []byte{}, 0o644)

	res, err := Glob().Execute(context.Background(),
		json.RawMessage(`{"pattern":"*.txt","path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "a.txt") || !strings.Contains(res.Text, "b.txt") {
		t.Errorf("expected a.txt and b.txt in output, got: %q", res.Text)
	}
	if strings.Contains(res.Text, "c.go") {
		t.Errorf("c.go should not match *.txt: %q", res.Text)
	}
}

func TestGlob_NoMatches(t *testing.T) {
	dir := t.TempDir()
	res, err := Glob().Execute(context.Background(),
		json.RawMessage(`{"pattern":"*.nonexistent","path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text == "" {
		t.Errorf("expected non-empty output for no matches, got empty")
	}
}

func TestGlob_PermissionIsR(t *testing.T) {
	if Glob().Permission() != PermR {
		t.Errorf("Glob must be R-tier, got %v", Glob().Permission())
	}
}

// gitAvailable is a lightweight skip-helper for environments without git.
func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH; skipping git_* tests")
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	gitAvailable(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@local")
	run("config", "user.name", "tester")
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "first commit")
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new file\n"), 0o644)
	return dir
}

func TestGitStatus_ReportsModifiedAndUntracked(t *testing.T) {
	dir := initRepo(t)
	res, err := GitStatus().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	saw := map[string]string{}
	for _, row := range res.Rows {
		saw[row["path"].(string)] = row["status"].(string)
	}
	if saw["a.txt"] == "" {
		t.Errorf("expected a.txt in status: %+v", res.Rows)
	}
	if saw["b.txt"] != "untracked" {
		t.Errorf("expected b.txt untracked, got %q", saw["b.txt"])
	}
}

func TestGitLog_ReturnsCommitRows(t *testing.T) {
	dir := initRepo(t)
	res, err := GitLog().Execute(context.Background(),
		json.RawMessage(`{"path":"`+dir+`","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected at least one commit row")
	}
	row := res.Rows[0]
	for _, key := range []string{"sha", "author", "date", "subject"} {
		if row[key] == nil || row[key] == "" {
			t.Errorf("row missing %s: %+v", key, row)
		}
	}
}

func TestOriginOfDefaultsBuiltin(t *testing.T) {
	if got := OriginOf(ReadFile()); got != OriginBuiltin {
		t.Fatalf("builtin tool origin = %q, want builtin", got)
	}
}

type fakeMCP struct{ readFileTool }

func (fakeMCP) Origin() Origin { return OriginMCP }

func TestOriginOfHonorsOptionalInterface(t *testing.T) {
	if got := OriginOf(fakeMCP{}); got != OriginMCP {
		t.Fatalf("origin = %q, want mcp", got)
	}
}
