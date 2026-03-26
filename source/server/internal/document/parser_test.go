package document

import (
	"testing"
)

const testGoSource = `package example

// DocumentedFunc has a doc comment.
func DocumentedFunc() {}

func UndocumentedFunc(x int) string {
	return ""
}

func unexported() {}

// Engine is a documented type.
type Engine struct {
	Name string
}

type UndocumentedType struct {
	Value int
}

// Runner is a documented interface.
type Runner interface {
	Run() error
}

type UndocumentedInterface interface {
	Do()
}

// MaxRetries is documented.
const MaxRetries = 3

const UndocumentedConst = 42

// Documented method.
func (e *Engine) Start() error { return nil }

func (e *Engine) Stop() error { return nil }

func (e *Engine) unexportedMethod() {}
`

func TestParseGoSource_ExportedFunctions(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	found := findSymbol(symbols, "UndocumentedFunc")
	if found == nil {
		t.Fatal("expected to find UndocumentedFunc")
	}
	if found.Kind != KindFunction {
		t.Errorf("expected KindFunction, got %s", found.Kind)
	}
	if found.HasDoc {
		t.Error("UndocumentedFunc should not have doc")
	}
}

func TestParseGoSource_SkipsDocumented(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	found := findSymbol(symbols, "DocumentedFunc")
	if found == nil {
		t.Fatal("expected to find DocumentedFunc")
	}
	if !found.HasDoc {
		t.Error("DocumentedFunc should have doc")
	}
}

func TestParseGoSource_SkipsUnexported(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	if findSymbol(symbols, "unexported") != nil {
		t.Error("unexported function should not be included")
	}
	if findSymbol(symbols, "unexportedMethod") != nil {
		t.Error("unexported method should not be included")
	}
}

func TestParseGoSource_Methods(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	stop := findSymbol(symbols, "Stop")
	if stop == nil {
		t.Fatal("expected to find Stop method")
	}
	if stop.Kind != KindMethod {
		t.Errorf("expected KindMethod, got %s", stop.Kind)
	}
	if stop.Receiver != "Engine" {
		t.Errorf("expected receiver Engine, got %s", stop.Receiver)
	}
	if stop.HasDoc {
		t.Error("Stop should not have doc")
	}
}

func TestParseGoSource_Types(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	ut := findSymbol(symbols, "UndocumentedType")
	if ut == nil {
		t.Fatal("expected to find UndocumentedType")
	}
	if ut.Kind != KindType {
		t.Errorf("expected KindType, got %s", ut.Kind)
	}
	if ut.HasDoc {
		t.Error("UndocumentedType should not have doc")
	}
}

func TestParseGoSource_Interfaces(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	ui := findSymbol(symbols, "UndocumentedInterface")
	if ui == nil {
		t.Fatal("expected to find UndocumentedInterface")
	}
	if ui.Kind != KindInterface {
		t.Errorf("expected KindInterface, got %s", ui.Kind)
	}
	if ui.HasDoc {
		t.Error("UndocumentedInterface should not have doc")
	}
}

func TestParseGoSource_Constants(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	uc := findSymbol(symbols, "UndocumentedConst")
	if uc == nil {
		t.Fatal("expected to find UndocumentedConst")
	}
	if uc.Kind != KindConst {
		t.Errorf("expected KindConst, got %s", uc.Kind)
	}
	if uc.HasDoc {
		t.Error("UndocumentedConst should not have doc")
	}
}

func TestUndocumentedSymbols_FiltersCorrectly(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	undoc := UndocumentedSymbols(symbols)

	// Should include: UndocumentedFunc, UndocumentedType, UndocumentedInterface, UndocumentedConst, Stop
	// Should NOT include: DocumentedFunc, Engine, Runner, MaxRetries, Start
	for _, s := range undoc {
		if s.HasDoc {
			t.Errorf("UndocumentedSymbols returned documented symbol %s", s.Name)
		}
	}

	// Verify documented ones are excluded
	for _, s := range undoc {
		if s.Name == "DocumentedFunc" || s.Name == "Engine" || s.Name == "Runner" || s.Name == "MaxRetries" || s.Name == "Start" {
			t.Errorf("UndocumentedSymbols should not include %s", s.Name)
		}
	}
}

func TestSymbolBody_Function(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	uf := findSymbol(symbols, "UndocumentedFunc")
	if uf == nil {
		t.Fatal("expected to find UndocumentedFunc")
	}
	if uf.Body == "" {
		t.Error("expected non-empty body")
	}
	if !contains(uf.Body, "func UndocumentedFunc") {
		t.Errorf("body should contain function signature, got: %s", uf.Body)
	}
}

func TestSymbolBody_Interface(t *testing.T) {
	symbols, err := ParseGoSource([]byte(testGoSource), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	ui := findSymbol(symbols, "UndocumentedInterface")
	if ui == nil {
		t.Fatal("expected to find UndocumentedInterface")
	}
	if ui.Body == "" {
		t.Error("expected non-empty body")
	}
	if !contains(ui.Body, "Do()") {
		t.Errorf("interface body should contain method list, got: %s", ui.Body)
	}
}

func TestParseGoSource_GroupedConstants_UniqueLines(t *testing.T) {
	src := `package example

const (
	Foo = "foo"
	Bar = "bar"
	Baz = "baz"
)
`
	symbols, err := ParseGoSource([]byte(src), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	// Each constant in the group should have a unique StartLine
	seen := map[int]string{}
	for _, sym := range symbols {
		if prev, exists := seen[sym.StartLine]; exists {
			t.Errorf("symbols %s and %s share StartLine %d", prev, sym.Name, sym.StartLine)
		}
		seen[sym.StartLine] = sym.Name
	}

	// Verify each constant points to its own line, not the const keyword
	foo := findSymbol(symbols, "Foo")
	bar := findSymbol(symbols, "Bar")
	if foo == nil || bar == nil {
		t.Fatal("expected to find Foo and Bar")
	}
	if foo.StartLine == bar.StartLine {
		t.Errorf("Foo and Bar should have different StartLines, both got %d", foo.StartLine)
	}
}

func TestParseGoSource_SingleConst_UsesGenDeclLine(t *testing.T) {
	src := `package example

const MaxRetries = 3
`
	symbols, err := ParseGoSource([]byte(src), "test.go")
	if err != nil {
		t.Fatalf("ParseGoSource: %v", err)
	}

	mr := findSymbol(symbols, "MaxRetries")
	if mr == nil {
		t.Fatal("expected to find MaxRetries")
	}
	// For ungrouped const, StartLine should be the "const" keyword line (line 3)
	if mr.StartLine != 3 {
		t.Errorf("expected StartLine 3, got %d", mr.StartLine)
	}
}

func findSymbol(symbols []Symbol, name string) *Symbol {
	for i := range symbols {
		if symbols[i].Name == name {
			return &symbols[i]
		}
	}
	return nil
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
