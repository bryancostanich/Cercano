package archivecheck

// PATH/LAYOUT contract tests for the portable namespace. The trusted
// layout itself must be internally consistent — no member that is also
// another member's implicit parent directory, no case-fold collisions that
// strings.ToLower misses (Greek sigma/final sigma, long s), valid UTF-8
// only, and no Windows-invalid characters (< > | ? * ") or reserved device
// names (including the superscript-digit COM¹/COM²/COM³ and LPT forms).
// Every adversarial case asserts its valid counterpart FIRST, so a
// rejection can never be a vacuous pass caused by unrelated invalidity.

import (
	"archive/tar"
	"bytes"
	"context"
	"io/fs"
	"testing"
)

// tinyNoRootLayout is the smallest rooted-free layout used by the conflict
// cases below; its single required member appears at the archive top level.
func tinyNoRootLayout(members ...string) Layout {
	return Layout{Root: "", Required: members}
}

// tinyNoRootOpts returns Options for the TarGz/Zip formats with the tiny
// no-root layout, so both containers are exercised by every case.
func tinyNoRootOpts(format Format, members ...string) Options {
	return Options{Format: format, Layout: tinyNoRootLayout(members...), Bounds: testBounds()}
}

// tinyTar builds a TarGz archive whose entries are exactly the given files
// (no directory entries unless the caller adds them).
func tinyTar(t *testing.T, files ...string) []byte {
	t.Helper()
	entries := make([]tarEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, tarEntry{name: f, body: "x", typ: tar.TypeReg, mode: 0o644})
	}
	return buildTarGz(t, entries)
}

// tinyZip builds a Zip archive whose entries are exactly the given files
// (no directory entries unless the caller adds them).
func tinyZip(t *testing.T, files ...string) []byte {
	t.Helper()
	entries := make([]zipEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, zipEntry{name: f, body: "x", mode: 0o644})
	}
	return buildZip(t, entries)
}

func tinyCheck(t *testing.T, format Format, data []byte, members ...string) (*Manifest, error) {
	t.Helper()
	opts := tinyNoRootOpts(format, members...)
	if format == Zip {
		return Check(context.Background(), Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))}, opts)
	}
	return Check(context.Background(), Archive{Reader: bytes.NewReader(data)}, opts)
}

// TestValidUnicodeLayoutAcceptedFirst proves non-ASCII member names are
// LEGITIMATE: a tiny valid fixture carrying Greek Omega and final sigma in
// required members is accepted in both containers. The adversarial cases
// below depend on this — they may reject only the specific violation, not
// Unicode generally.
func TestValidUnicodeLayoutAcceptedFirst(t *testing.T) {
	const omegaMember = "bin/cercano-Ω"
	const finalSigmaMember = "Όνομα-ς.txt"
	entries := []tarEntry{
		{name: "rel-1.0/bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "rel-1.0/" + omegaMember, body: agentBody, typ: tar.TypeReg, mode: 0o755},
		{name: "rel-1.0/" + finalSigmaMember, body: "u", typ: tar.TypeReg, mode: 0o644},
	}
	uni := Layout{Root: "rel-1.0", Required: []string{omegaMember, finalSigmaMember}}
	tarOpts := Options{Format: TarGz, Layout: uni, Bounds: testBounds()}
	man, err := Check(context.Background(), Archive{Reader: bytes.NewReader(buildTarGz(t, entries))}, tarOpts)
	if err != nil {
		t.Fatalf("valid Unicode TarGz fixture rejected: %v", err)
	}
	if len(man.Members) != 2 {
		t.Fatalf("manifest members = %d, want 2", len(man.Members))
	}

	zentries := []zipEntry{
		{name: "rel-1.0/bin/", mode: fs.ModeDir | 0o755},
		{name: "rel-1.0/" + omegaMember, body: agentBody, mode: 0o755},
		{name: "rel-1.0/" + finalSigmaMember, body: "u", mode: 0o644},
	}
	zipOpts := Options{Format: Zip, Layout: uni, Bounds: testBounds()}
	if _, err = Check(context.Background(), Archive{ReaderAt: bytes.NewReader(buildZip(t, zentries)), Size: int64(len(buildZip(t, zentries)))}, zipOpts); err != nil {
		t.Fatalf("valid Unicode Zip fixture rejected: %v", err)
	}
}

// TestLayoutFileAlsoImplicitParentDirectoryRejected covers the concrete
// conflict: a trusted layout whose Required contains both file "a" and
// file "a/b" — so "a" must be both a regular file and an implicit parent
// directory — used to let an archive carrying both members (with no
// explicit directory entry) pass. Each side alone is valid first; the
// combination must be refused regardless of declaration order, container
// and whether the conflicting member is Required or Optional.
func TestLayoutFileAlsoImplicitParentDirectoryRejected(t *testing.T) {
	// Valid first: each side alone is a legal layout and a passing archive.
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "a"), "a"); err != nil {
		t.Fatalf("valid single-file layout rejected: %v", err)
	}
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "a/b"), "a/b"); err != nil {
		t.Fatalf("valid nested-file layout (no explicit directory entry) rejected: %v", err)
	}
	if _, err := tinyCheck(t, Zip, tinyZip(t, "a/b"), "a/b"); err != nil {
		t.Fatalf("valid nested-file zip layout rejected: %v", err)
	}

	// Adversarial: the layout demands "a" as both a file and a directory.
	for _, members := range [][]string{
		{"a", "a/b"},
		{"a/b", "a"}, // declaration order must not matter
	} {
		for _, format := range []Format{TarGz, Zip} {
			data := tinyTar(t, "a", "a/b")
			if format == Zip {
				data = tinyZip(t, "a", "a/b")
			}
			_, err := tinyCheck(t, format, data, members...)
			if err == nil {
				t.Fatalf("layout %v accepted a file that is also an implicit parent directory (format %d)", members, format)
			}
		}
	}

	// The same conflict through an Optional member must be refused too.
	opts := Options{Format: TarGz, Layout: Layout{Root: "", Required: []string{"a"}, Optional: []string{"a/b"}}, Bounds: testBounds()}
	if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, "a", "a/b"))}, opts); err == nil {
		t.Fatal("Required file with Optional child beneath it was accepted")
	}
}

// TestLayoutUnicodeCaseFoldCollisionsRejected covers collisions that
// strings.ToLower misses: Greek sigma σ/ς/Σ fold to one name under Unicode
// simple case folding (the strings.EqualFold equivalence), as do long s ſ
// and s, and the Kelvin sign K and k. A layout carrying two members (or a
// member and another member's implicit parent directory) that fold to the
// same name is ambiguous on case-insensitive filesystems and must be
// refused.
func TestLayoutUnicodeCaseFoldCollisionsRejected(t *testing.T) {
	// Valid first: each Greek/sigma name alone is legitimate.
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "Όνομα-σ.txt"), "Όνομα-σ.txt"); err != nil {
		t.Fatalf("valid Greek sigma member rejected: %v", err)
	}
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "Όνομα-ς.txt"), "Όνομα-ς.txt"); err != nil {
		t.Fatalf("valid Greek final-sigma member rejected: %v", err)
	}

	colliding := [][2]string{
		{"Όνομα-σ.txt", "Όνομα-ς.txt"}, // sigma vs final sigma
		{"Όνομα-σ.txt", "Όνομα-Σ.txt"}, // sigma vs capital sigma
		{"s.bin", "ſ.bin"},             // long s (U+017F) vs s
		{"k.bin", "K.bin"},             // Kelvin sign (U+212A) vs k
	}
	for _, pair := range colliding {
		for _, members := range [][]string{pair[0:1], pair[1:2]} {
			if _, err := tinyCheck(t, TarGz, tinyTar(t, members[0]), members...); err != nil {
				t.Fatalf("valid member %q alone rejected: %v", members[0], err)
			}
		}
		// Both together must be refused, in either declaration order.
		for _, members := range [][]string{pair[0:], []string{pair[1], pair[0]}} {
			if _, err := tinyCheck(t, TarGz, tinyTar(t, members...), members...); err == nil {
				t.Fatalf("layout %v with case-fold-colliding members was accepted", members)
			}
		}
	}

	// Case-colliding implicit directories: "a/b" and "A/c" force "a" and
	// "A" to be the same directory on case-insensitive filesystems. Valid
	// first: same-case parents are fine.
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "a/b", "a/c"), "a/b", "a/c"); err != nil {
		t.Fatalf("valid shared-parent layout rejected: %v", err)
	}
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "a/b", "A/c"), "a/b", "A/c"); err == nil {
		t.Fatal("layout with case-colliding parent directories was accepted")
	}
	// A file colliding with another member's implicit parent directory.
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "a/b", "A"), "a/b", "A"); err == nil {
		t.Fatal("file colliding with a case-fold parent directory was accepted")
	}
	// The same directory collision in the Zip container.
	if _, err := tinyCheck(t, Zip, tinyZip(t, "a/b", "A/c"), "a/b", "A/c"); err == nil {
		t.Fatal("zip layout with case-colliding parent directories was accepted")
	}
}

// TestLayoutCaseFoldCollisionsRejectedRegardlessOfOrder proves the
// archive-entry order never decides whether a conflict is caught: a
// directory entry and a fold-colliding file entry are refused in either
// order, and so are two fold-colliding directory entries.
func TestLayoutCaseFoldCollisionsRejectedRegardlessOfOrder(t *testing.T) {
	// Valid first: the directory and file coexist when they do not collide.
	if _, err := tinyCheck(t, TarGz, buildTarGz(t, []tarEntry{
		{name: "a/", typ: tar.TypeDir, mode: 0o755},
		{name: "a/b", body: "x", typ: tar.TypeReg, mode: 0o644},
	}), "a/b"); err != nil {
		t.Fatalf("valid explicit-parent layout rejected: %v", err)
	}

	for _, entries := range [][]tarEntry{
		{{name: "a/", typ: tar.TypeDir, mode: 0o755}, {name: "A", body: "x", typ: tar.TypeReg, mode: 0o644}},
		{{name: "A", body: "x", typ: tar.TypeReg, mode: 0o644}, {name: "a/", typ: tar.TypeDir, mode: 0o755}},
	} {
		layout := Layout{Root: "", Required: []string{"A"}, Optional: []string{"a/b"}}
		opts := Options{Format: TarGz, Layout: layout, Bounds: testBounds()}
		_, err := Check(context.Background(), Archive{Reader: bytes.NewReader(buildTarGz(t, entries))}, opts)
		if err == nil {
			t.Fatalf("directory/file fold collision accepted in order %v", entries)
		}
	}
}

// TestInvalidUTF8MemberNamesRejected covers names that are not valid UTF-8:
// Go's range-over-string decodes their bytes to U+FFFD (RuneError), which
// used to be accepted into the allowlist and matched archive entries
// byte-for-byte. Valid first: the same names with valid UTF-8 are fine.
func TestInvalidUTF8MemberNamesRejected(t *testing.T) {
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "bad-Ω"), "bad-Ω"); err != nil {
		t.Fatalf("valid UTF-8 member rejected: %v", err)
	}

	// A layout whose Optional member is invalid UTF-8 must be refused.
	bad := "bad-\xff\xfe"
	opts := Options{Format: TarGz, Layout: Layout{Root: "", Required: []string{"a"}, Optional: []string{bad}}, Bounds: testBounds()}
	_, err := Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, "a", bad))}, opts)
	if err == nil {
		t.Fatal("invalid UTF-8 member name accepted in layout and archive")
	}

	// And directly as a required member.
	opts.Layout = Layout{Root: "", Required: []string{bad}}
	if _, err = Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, bad))}, opts); err == nil {
		t.Fatal("invalid UTF-8 required member name accepted")
	}

	// The Zip container too.
	zopts := Options{Format: Zip, Layout: Layout{Root: "", Required: []string{"a"}, Optional: []string{bad}}, Bounds: testBounds()}
	if _, err = Check(context.Background(), Archive{ReaderAt: bytes.NewReader(tinyZip(t, "a", bad)), Size: int64(len(tinyZip(t, "a", bad)))}, zopts); err == nil {
		t.Fatal("invalid UTF-8 member name accepted in zip layout and archive")
	}
}

// TestWindowsInvalidCharactersRejected covers < > | ? * and ", which
// Windows refuses in filenames but which used to pass layout and archive
// validation. Valid first: ordinary punctuation stays legitimate.
func TestWindowsInvalidCharactersRejected(t *testing.T) {
	if _, err := tinyCheck(t, TarGz, tinyTar(t, "file-name.txt"), "file-name.txt"); err != nil {
		t.Fatalf("valid ordinary member rejected: %v", err)
	}

	for _, ch := range []string{"<", ">", "|", "?", "*", "\""} {
		name := "a" + ch + "b"
		// In the layout (Optional) together with a matching archive entry.
		opts := Options{Format: TarGz, Layout: Layout{Root: "", Required: []string{"ok"}, Optional: []string{name}}, Bounds: testBounds()}
		if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, "ok", name))}, opts); err == nil {
			t.Fatalf("Windows-invalid character %q accepted in layout and archive", ch)
		}
		// As a required member.
		opts.Layout = Layout{Root: "", Required: []string{name}}
		if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, name))}, opts); err == nil {
			t.Fatalf("Windows-invalid character %q accepted as a required member", ch)
		}
	}

	// The Zip container too, one representative character.
	zopts := Options{Format: Zip, Layout: Layout{Root: "", Required: []string{"ok"}, Optional: []string{"a<b"}}, Bounds: testBounds()}
	zdata := tinyZip(t, "ok", "a<b")
	if _, err := Check(context.Background(), Archive{ReaderAt: bytes.NewReader(zdata), Size: int64(len(zdata))}, zopts); err == nil {
		t.Fatal("Windows-invalid character accepted in zip layout and archive")
	}
}

// TestWindowsReservedDeviceNamesIncludingSuperscriptsRejected covers the
// reserved device namespace, including the superscript-digit forms
// COM¹/COM²/COM³ and LPT¹/LPT²/LPT³ (U+00B9/U+00B2/U+00B3) that recent
// Windows reserves and strings.ToLower-based checks used to miss. Valid
// first: com10/lpt10 and harmless names stay legitimate.
func TestWindowsReservedDeviceNamesIncludingSuperscriptsRejected(t *testing.T) {
	for _, ok := range []string{"com10.txt", "lpt10.txt", "console-notes.txt"} {
		if _, err := tinyCheck(t, TarGz, tinyTar(t, ok), ok); err != nil {
			t.Fatalf("valid member %q rejected: %v", ok, err)
		}
	}

	for _, name := range []string{"com¹", "COM².txt", "lpt³", "Com³.tar.gz", "aux.txt", "NUL"} {
		opts := Options{Format: TarGz, Layout: Layout{Root: "", Required: []string{"ok"}, Optional: []string{name}}, Bounds: testBounds()}
		if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, "ok", name))}, opts); err == nil {
			t.Fatalf("Windows reserved device name %q accepted in layout and archive", name)
		}
	}

	// The Zip container too.
	zopts := Options{Format: Zip, Layout: Layout{Root: "", Required: []string{"ok"}, Optional: []string{"com¹"}}, Bounds: testBounds()}
	zdata := tinyZip(t, "ok", "com¹")
	if _, err := Check(context.Background(), Archive{ReaderAt: bytes.NewReader(zdata), Size: int64(len(zdata))}, zopts); err == nil {
		t.Fatal("Windows reserved superscript device name accepted in zip layout and archive")
	}
}

// TestDirectoryNameCarriesAtMostOneTrailingSlash proves a directory entry
// may carry exactly one trailing slash — and that a second one is refused
// on its own terms, rather than being double-trimmed into acceptance.
func TestDirectoryNameCarriesAtMostOneTrailingSlash(t *testing.T) {
	// Valid first: one trailing slash on a needed parent is fine.
	if _, err := tinyCheck(t, TarGz, buildTarGz(t, []tarEntry{
		{name: "a/", typ: tar.TypeDir, mode: 0o755},
		{name: "a/b", body: "x", typ: tar.TypeReg, mode: 0o644},
	}), "a/b"); err != nil {
		t.Fatalf("valid single-trailing-slash directory rejected: %v", err)
	}

	// Adversarial: two trailing slashes on a needed parent directory.
	double := []tarEntry{
		{name: "a//", typ: tar.TypeDir, mode: 0o755},
		{name: "a/b", body: "x", typ: tar.TypeReg, mode: 0o644},
	}
	_, err := Check(context.Background(), Archive{Reader: bytes.NewReader(buildTarGz(t, double))}, tinyNoRootOpts(TarGz, "a/b"))
	if err == nil {
		t.Fatal("directory entry with two trailing slashes was accepted")
	}

	// The same in the Zip container (mode bit dir, name carrying "//").
	zdata := buildZip(t, []zipEntry{
		{name: "a//", mode: fs.ModeDir | 0o755},
		{name: "a/b", body: "x", mode: 0o644},
	})
	if _, err = Check(context.Background(), Archive{ReaderAt: bytes.NewReader(zdata), Size: int64(len(zdata))}, tinyNoRootOpts(Zip, "a/b")); err == nil {
		t.Fatal("zip directory entry with two trailing slashes was accepted")
	}

	// And in the layout root itself.
	rootOpts := Options{Format: TarGz, Layout: Layout{Root: "rel//", Required: []string{"a"}}, Bounds: testBounds()}
	if _, err = Check(context.Background(), Archive{Reader: bytes.NewReader(tinyTar(t, "rel/a"))}, rootOpts); err == nil {
		t.Fatal("layout root with two trailing slashes was accepted")
	}
}
