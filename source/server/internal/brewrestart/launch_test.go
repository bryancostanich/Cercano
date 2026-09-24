package brewrestart

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func procArgsFixture(argc uint32, body string) []byte {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, argc)
	return append(data, []byte(body)...)
}

func TestParseProcArgs(t *testing.T) {
	args, env, err := parseProcArgs(procArgsFixture(4, "/path/executable\x00\x00\x00spoofed name\x00agent\x00\x00with spaces\x00TOKEN=secret=value\x00EMPTY=\x00\x00apple-extra\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"spoofed name", "agent", "", "with spaces"}) {
		t.Fatal("argument boundaries were lost")
	}
	if !reflect.DeepEqual(env, []string{"TOKEN=secret=value", "EMPTY="}) {
		t.Fatal("environment boundaries were lost")
	}
	for _, data := range [][]byte{
		nil, procArgsFixture(0, "x\x00"), procArgsFixture(100, "x\x00"),
		procArgsFixture(1, "unterminated"), procArgsFixture(2, "exe\x00arg\x00"),
		procArgsFixture(1, "exe\x00arg\x00bad-env\x00"), procArgsFixture(1, "exe\x00arg\x00=bad\x00"),
	} {
		if _, _, err := parseProcArgs(data); err == nil {
			t.Fatal("accepted malformed launch state")
		}
	}
}

func TestLaunchStateFormattingRedactsValues(t *testing.T) {
	s := LaunchState{Args: []string{"arg-secret"}, Env: []string{"KEY=env-secret"}, Directory: "cwd-secret"}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		text := fmt.Sprintf(format, s)
		if strings.Contains(text, "secret") {
			t.Fatal("formatting exposed sensitive launch state")
		}
	}
}
