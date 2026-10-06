package brewrestart

import "testing"

func TestCheckOwned(t *testing.T) {
	const newExe = "/opt/homebrew/Cellar/cercano/2/bin/cercano"
	base := Identity{PID: 100, UID: 501, Executable: "/opt/homebrew/Cellar/cercano/1/bin/cercano", StartSeconds: 123}
	for _, tt := range []struct {
		name, path string
		uid        uint32
		want       bool
	}{
		{"old version", base.Executable, 501, true},
		{"same version", newExe, 501, true},
		{"developer", "/Users/dev/Cercano/bin/cercano", 501, false},
		{"another user", base.Executable, 502, false},
		{"another prefix", "/usr/local/Cellar/cercano/1/bin/cercano", 501, false},
		{"prefix collision", "/opt/homebrew/Cellar/cercano-other/1/bin/cercano", 501, false},
		{"other binary", "/opt/homebrew/Cellar/cercano/1/bin/cercano-cli", 501, false},
		{"uncanonical", "/opt/homebrew/Cellar/cercano/1/../2/bin/cercano", 501, false},
		{"relative", "Cellar/cercano/1/bin/cercano", 501, false},
		{"prefix symlink", "/opt/homebrew/bin/cercano", 501, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			p.Executable = tt.path
			p.UID = tt.uid
			if err := CheckOwned(p, newExe, 501); (err == nil) != tt.want {
				t.Fatalf("CheckOwned = %v, want accepted=%v", err, tt.want)
			}
		})
	}
	for _, mutate := range []func(*Identity){func(p *Identity) { p.PID = 0 }, func(p *Identity) { p.StartSeconds = 0 }} {
		p := base
		mutate(&p)
		if CheckOwned(p, newExe, 501) == nil {
			t.Fatal("accepted incomplete identity")
		}
	}
	if CheckOwned(base, "/opt/homebrew/bin/cercano", 501) == nil {
		t.Fatal("accepted unresolved new executable")
	}
}

func TestSameProcessDetectsPIDReuse(t *testing.T) {
	p := Identity{PID: 42, UID: 501, Executable: "/bin/sleep", StartSeconds: 100, StartMicroseconds: 1}
	if !p.SameProcess(p) {
		t.Fatal("same identity differs")
	}
	for _, change := range []func(*Identity){
		func(q *Identity) { q.PID++ }, func(q *Identity) { q.UID++ }, func(q *Identity) { q.Executable = "/bin/sh" },
		func(q *Identity) { q.StartSeconds++ }, func(q *Identity) { q.StartMicroseconds++ },
	} {
		q := p
		change(&q)
		if p.SameProcess(q) {
			t.Fatal("different process accepted")
		}
	}
}
