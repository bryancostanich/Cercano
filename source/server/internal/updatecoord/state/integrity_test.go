package state

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOperationJSONRejectsAmbiguityAndInvalidUTF8(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rec := newRecord(t, s)
	b, e := json.Marshal(rec)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{
		append(append([]byte{}, b[:len(b)-1]...), []byte(`,"state":"complete"}`)...),
		append(append([]byte{}, b[:len(b)-1]...), []byte(`,"STATE":"complete"}`)...),
		bytes.Replace(b, []byte("1.2.3"), []byte{0xff}, 1),
	} {
		if _, e := decodeOperationRecord(bad); e == nil {
			t.Fatal("ambiguous/invalid operation JSON accepted")
		}
	}
}
func TestUnexpectedSchemaObjectsRefused(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER unexpected AFTER UPDATE ON install_state BEGIN UPDATE install_state SET next_op_id=1; END;`); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(dbFile(root, "test-install"))
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(root, "test-install")
	if e == nil {
		reopened.Close()
		t.Fatal("unknown trigger accepted")
	}
	after, e := os.ReadFile(dbFile(root, "test-install"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("schema refusal modified database")
	}
}
func TestInvalidRevisionAndUnallocatedIDRefused(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rec := newRecord(t, s)
	rec.ID = 999
	if _, e = s.SaveOperationRecord(context.Background(), 0, rec); e == nil {
		t.Fatal("unallocated operation ID accepted")
	}
}
