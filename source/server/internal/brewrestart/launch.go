package brewrestart

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// LaunchState is sensitive: arguments and environment can contain credentials.
// Do not persist or log it. Formatting intentionally reports only counts.
type LaunchState struct {
	Identity  Identity
	Args      []string
	Env       []string
	Directory string
}

func (s LaunchState) Format(f fmt.State, verb rune) {
	fmt.Fprintf(f, "LaunchState{pid:%d args:%d env:%d cwd-set:%t}", s.Identity.PID, len(s.Args), len(s.Env), s.Directory != "")
}

// KERN_PROCARGS2 returns argc, executable path, NUL padding, argc argv
// strings, then environment strings terminated by an empty string. Darwin's
// supported Apple Silicon ABI is little endian. Empty arguments after argv[0]
// must be preserved; they are not padding.
func parseProcArgs(data []byte) ([]string, []string, error) {
	bad := func() ([]string, []string, error) { return nil, nil, fmt.Errorf("malformed kernel launch state") }
	if len(data) < 4 {
		return bad()
	}
	argc := int(binary.LittleEndian.Uint32(data[:4]))
	rest := data[4:]
	if argc < 1 || argc > len(rest) {
		return bad()
	}
	end := bytes.IndexByte(rest, 0)
	if end < 1 {
		return bad()
	}
	rest = bytes.TrimLeft(rest[end+1:], "\x00")
	args := make([]string, 0, argc)
	for i := 0; i < argc; i++ {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return bad()
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	if args[0] == "" {
		return bad()
	}
	var env []string
	for len(rest) > 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return bad()
		}
		if end == 0 {
			return args, env, nil
		}
		entry := string(rest[:end])
		if key, _, ok := strings.Cut(entry, "="); !ok || key == "" {
			return bad()
		}
		env = append(env, entry)
		rest = rest[end+1:]
	}
	return args, env, nil
}
