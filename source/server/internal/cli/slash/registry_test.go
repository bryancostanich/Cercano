package slash

import "testing"

func TestRegistry_DispatchExact(t *testing.T) {
	r := New()
	called := false
	r.Register(Command{
		Name:    "foo",
		Help:    "test",
		Handler: func(args []string) Result { called = true; return Result{Kind: ResultText, Text: "ok"} },
	})
	res, ok := r.Dispatch("/foo")
	if !ok || !called || res.Text != "ok" {
		t.Fatalf("dispatch /foo failed: ok=%v called=%v text=%q", ok, called, res.Text)
	}
}

func TestRegistry_DispatchAlias(t *testing.T) {
	r := New()
	r.Register(Command{
		Name:    "quit",
		Aliases: []string{"exit"},
		Help:    "leave",
		Handler: func(args []string) Result { return Result{Kind: ResultQuit} },
	})
	for _, in := range []string{"/quit", "quit", "/exit", "exit"} {
		res, ok := r.Dispatch(in)
		if !ok || res.Kind != ResultQuit {
			t.Errorf("dispatch %q: ok=%v kind=%v", in, ok, res.Kind)
		}
	}
}

func TestRegistry_PrefixSuggestion(t *testing.T) {
	r := New()
	r.Register(Command{Name: "models", Help: "list", Handler: func(args []string) Result { return Result{} }})
	r.Register(Command{Name: "model", Help: "set", Handler: func(args []string) Result { return Result{} }})

	res, ok := r.Dispatch("/mod")
	if ok {
		t.Fatalf("expected no exact match for /mod")
	}
	if res.Text != "unknown command /mod — did you mean /model?" {
		t.Errorf("suggestion mismatch: %q", res.Text)
	}
}

func TestRegistry_UnknownNoPrefix(t *testing.T) {
	r := New()
	r.Register(Command{Name: "models", Help: "list", Handler: func(args []string) Result { return Result{} }})

	res, ok := r.Dispatch("/xyz")
	if ok || res.Text == "" {
		t.Fatalf("expected unknown w/ help text: ok=%v text=%q", ok, res.Text)
	}
}

func TestRegistry_DuplicateNamePanics(t *testing.T) {
	r := New()
	r.Register(Command{Name: "foo", Help: "1", Handler: func(args []string) Result { return Result{} }})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate name")
		}
	}()
	r.Register(Command{Name: "foo", Help: "2", Handler: func(args []string) Result { return Result{} }})
}

func TestRegisterBasics_All3Present(t *testing.T) {
	r := New()
	RegisterBasics(r)
	for _, want := range []string{"quit", "exit", "clear", "help"} {
		if _, ok := r.cmds[want]; !ok {
			t.Errorf("missing command %q", want)
		}
	}
}
