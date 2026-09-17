//go:build unix

package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/reasoningexperiment"
)

func TestReasoningDiagnosticRejectsPipeBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"input_path": path})
	done := make(chan error, 1)
	go func() {
		_, err := ReasoningDiagnostic().Execute(context.Background(), &capabilities.Call{Args: args, Svc: capabilities.Services{ReasoningDiagnostic: func(context.Context, reasoningexperiment.Spec) (reasoningexperiment.Report, error) {
			return reasoningexperiment.Report{}, nil
		}}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pipe accepted")
		}
	case <-time.After(100 * time.Millisecond):
		// Release the blocked open so a failing probe does not leak a goroutine.
		writer, err := os.OpenFile(path, os.O_WRONLY, 0600)
		if err == nil {
			writer.Close()
		}
		<-done
		t.Fatal("nonregular input blocked before file validation")
	}
}
