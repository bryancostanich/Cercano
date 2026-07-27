package slash

import "testing"

func TestRegisterPlan_RegistersCommand(t *testing.T) {
	r := New()
	RegisterPlan(r, nil)
	if _, ok := r.cmds["plan"]; !ok {
		t.Fatal("missing /plan")
	}
}

func TestSlash_PlanEntersPlanProfile(t *testing.T) {
	r := New()
	RegisterPlan(r, nil)
	res, _ := r.Dispatch("/plan")
	if res.Kind != ResultSetSessionProfile || res.SessionProfile != "plan" {
		t.Errorf("/plan: %+v", res)
	}
}

func TestSlash_PlanOffReturnsToDefault(t *testing.T) {
	r := New()
	RegisterPlan(r, nil)
	for _, arg := range []string{"off", "default", "exit", "none"} {
		res, _ := r.Dispatch("/plan " + arg)
		if res.Kind != ResultSetSessionProfile || res.SessionProfile != "default" {
			t.Errorf("/plan %s: %+v", arg, res)
		}
	}
}

func TestSlash_PlanNamedProfile(t *testing.T) {
	r := New()
	RegisterPlan(r, nil)
	res, _ := r.Dispatch("/plan brainstorm")
	if res.Kind != ResultSetSessionProfile || res.SessionProfile != "brainstorm" {
		t.Errorf("/plan brainstorm: %+v", res)
	}
}
