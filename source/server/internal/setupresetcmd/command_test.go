package setupresetcmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestConfirmationIsTheTrigger(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		input     string
		tty       bool
		wantCalls int
		wantCode  int
	}{
		{"confirmed", []string{"--setup"}, "RESET\n", true, 1, 0},
		{"declined", []string{"--setup"}, "no\n", true, 0, 1},
		{"EOF", []string{"--setup"}, "", true, 0, 1},
		{"noninteractive", []string{"--setup"}, "RESET\n", false, 0, 1},
		{"missing scope", nil, "RESET\n", true, 0, 2},
		{"extra", []string{"--setup", "all"}, "RESET\n", true, 0, 2},
		{"unknown flag", []string{"--setup", "--yes"}, "RESET\n", true, 0, 2},
		{"help", []string{"--help"}, "", false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errout bytes.Buffer
			calls := 0
			code := Run(context.Background(), tc.args, strings.NewReader(tc.input), &out, &errout, tc.tty, func(context.Context) (Outcome, error) {
				calls++
				return Outcome{ConfigWritten: true, WizardWritten: true}, nil
			})
			if code != tc.wantCode || calls != tc.wantCalls {
				t.Fatalf("code=%d calls=%d output=%s %s", code, calls, out.String(), errout.String())
			}
			if tc.wantCalls == 1 && !strings.Contains(out.String(), "sessions") {
				t.Fatal("missing live-session warning")
			}
		})
	}
}
func TestPartialFailureDoesNotClaimSuccess(t *testing.T) {
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"--setup"}, strings.NewReader("RESET\n"), &out, &errout, true, func(context.Context) (Outcome, error) {
		return Outcome{CredentialsRemoved: 2, LiveApplied: true}, errors.New("fixture failure")
	})
	if code == 0 || strings.Contains(out.String(), "Setup reset complete") || !strings.Contains(errout.String(), "2") {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errout.String())
	}
}

func TestUnknownAgentResultDoesNotReportFalseProgress(t *testing.T) {
	var out, errout bytes.Buffer
	code := Run(context.Background(), []string{"--setup"}, strings.NewReader("RESET\n"), &out, &errout, true, func(context.Context) (Outcome, error) {
		return Outcome{UsedAgent: true, ResultUnknown: true}, errors.New("lost agent response")
	})
	if code == 0 || strings.Contains(errout.String(), "config written: false") || !strings.Contains(errout.String(), "unknown") {
		t.Fatalf("misleading unknown result: %s", errout.String())
	}
}
