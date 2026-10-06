package state

import (
	"bytes"
	"context"
	"fmt"
	"os"
	subprocess "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Only tests spawn this worker; its entire database root is a parent's TempDir.
func TestStateWorker(t *testing.T) {
	mode := os.Getenv("CERCANO_STATE_TEST_WORKER")
	if mode == "" {
		t.Skip("helper exercised by parent subprocess tests")
	}
	s, e := Open(os.Getenv("CERCANO_STATE_TEST_ROOT"), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	switch mode {
	case "allocate":
		for i := 0; i < 12; i++ {
			id, e := s.AllocateOperationID(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			fmt.Printf("ALLOC:%d\n", id)
		}
	case "crash":
		conn, e := s.db.Conn(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		if _, e = conn.ExecContext(context.Background(), `BEGIN IMMEDIATE; UPDATE install_state SET next_op_id=1000;`); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(os.Getenv("CERCANO_STATE_TEST_READY"), []byte("ready"), 0600); e != nil {
			t.Fatal(e)
		}
		// Parent kills this exact child process, testing rollback of an uncommitted write.
		for {
			time.Sleep(time.Second)
		}
	default:
		t.Fatal("unknown fixture mode")
	}
}
func worker(t *testing.T, ctx context.Context, root, mode, ready string) *subprocess.Cmd {
	t.Helper()
	cmd := subprocess.CommandContext(ctx, os.Args[0], "-test.run=^TestStateWorker$", "-test.timeout=20s")
	cmd.Env = append(os.Environ(), "CERCANO_STATE_TEST_WORKER="+mode, "CERCANO_STATE_TEST_ROOT="+root, "CERCANO_STATE_TEST_READY="+ready)
	return cmd
}
func TestConcurrentProcessesAllocateDistinctPersistentIDs(t *testing.T) {
	testConcurrentAllocation(t, false)
}
func TestConcurrentProcessesUpgradeLegacyWithoutLosingIDs(t *testing.T) {
	testConcurrentAllocation(t, true)
}
func testConcurrentAllocation(t *testing.T, legacy bool) {
	t.Helper()
	root := t.TempDir()
	var s *Store
	var e error
	if legacy {
		legacyV1Database(t, root, "test-install")
	} else {
		s, e = Open(root, "test-install")
		if e != nil {
			t.Fatal(e)
		}
		s.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmds := []*subprocess.Cmd{worker(t, ctx, root, "allocate", ""), worker(t, ctx, root, "allocate", "")}
	outputs := []bytes.Buffer{{}, {}}
	var started []*subprocess.Cmd
	defer func() {
		for _, cmd := range started {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	for i, cmd := range cmds {
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		started = append(started, cmd)
	}
	for i, cmd := range cmds {
		if e = cmd.Wait(); e != nil {
			t.Fatalf("worker%d: %v %s", i, e, outputs[i].String())
		}
	}
	seen := map[int64]bool{}
	for i := range outputs {
		for _, line := range strings.Split(outputs[i].String(), "\n") {
			if !strings.HasPrefix(line, "ALLOC:") {
				continue
			}
			id, e := strconv.ParseInt(strings.TrimPrefix(line, "ALLOC:"), 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			if seen[id] {
				t.Fatal("cross-process duplicate", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 24 {
		t.Fatalf("allocated=%d", len(seen))
	}
	s, e = Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	expectedNext := int64(25)
	if legacy {
		// The legacy fixture already owns operation ID 1; workers must not reuse it.
		expectedNext++
		if seen[1] {
			t.Fatal("migration reissued the legacy operation ID")
		}
		if old, rev, err := s.LoadOperationRecord(ctx, 1); err != nil || old.ID != 1 || rev != 1 {
			t.Fatalf("legacy operation lost: %+v %d %v", old, rev, err)
		}
	}
	id, e := s.AllocateOperationID(ctx)
	if e != nil || id != expectedNext {
		t.Fatalf("counter after workers=%d want=%d %v", id, expectedNext, e)
	}
}
func TestKilledWriterRollsBackWithoutCounterReuseOfCommittedIDs(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.AllocateOperationID(context.Background())
	if e != nil || id != 1 {
		t.Fatal(id, e)
	}
	s.Close()
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := worker(t, ctx, root, "crash", ready)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped := false
	defer func() {
		_ = cmd.Process.Kill()
		if !reaped {
			<-done
		}
	}()
	deadline := time.After(10 * time.Second)
	for {
		if _, e = os.Stat(ready); e == nil {
			break
		}
		select {
		case e := <-done:
			reaped = true
			t.Fatalf("worker exited before ready: %v %s", e, output.String())
		case <-deadline:
			_ = cmd.Process.Kill()
			<-done
			reaped = true
			t.Fatal("worker readiness timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	<-done
	reaped = true
	s, e = Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	id, e = s.AllocateOperationID(context.Background())
	if e != nil || id != 2 {
		t.Fatalf("uncommitted write survived or committed ID reissued: %d %v", id, e)
	}
}
