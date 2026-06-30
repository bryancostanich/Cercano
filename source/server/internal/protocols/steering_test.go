package protocols

import (
	"strings"
	"testing"
)

func TestSteeringBlockContainsRulesAndTriggers(t *testing.T) {
	ps := []Protocol{
		{Name: "a", Trigger: "Trigger A here."},
		{Name: "b", Trigger: "Trigger B here."},
	}
	out := SteeringBlock(ps)
	if !strings.Contains(out, "plain English") {
		t.Fatal("missing plain-English rule")
	}
	if strings.Count(out, "Trigger A here.")+strings.Count(out, "Trigger B here.") != 2 {
		t.Fatal("both triggers must appear exactly once")
	}
	before := strings.Count(out, "\n")
	out2 := SteeringBlock(append(ps, Protocol{Name: "c", Trigger: "Trigger C here."}))
	if strings.Count(out2, "\n") != before+1 {
		t.Fatal("adding a protocol should add exactly one trigger line")
	}
}

func TestSteeringBlockEmptyProtocols(t *testing.T) {
	out := SteeringBlock(nil)
	if !strings.Contains(out, "plain English") {
		t.Fatal("rules must be present even with no protocols")
	}
}

func TestSteeringBlockContainsCheckpointNudge(t *testing.T) {
	out := SteeringBlock(nil)
	if !strings.Contains(out, "checkpoint") {
		t.Fatal("steering block must contain checkpoint nudge")
	}
}
