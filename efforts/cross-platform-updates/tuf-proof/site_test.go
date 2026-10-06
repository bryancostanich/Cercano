package tufproof

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"testing"
)

// Test-only composition contract, not a production publisher. Inputs representing
// TUF metadata must already be signature-verified/authorized by the publisher.
// A production Pages workflow must also prove shared deployment serialization
// and freshness checks: composition alone cannot prevent a stale whole-site deploy.
func composeSite(website, previousFeed, nextFeed map[string][]byte) (map[string][]byte, error) {
	const prefix = "updates/tuf/"
	result := map[string][]byte{}
	safe := func(name string) bool {
		return name != "" && name != "." && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
	}
	for name, data := range website {
		if !safe(name) || name == "updates/tuf" || strings.HasPrefix(name, prefix) {
			return nil, fmt.Errorf("website cannot own reserved metadata path")
		}
		result[name] = append([]byte{}, data...)
	}
	for name, data := range previousFeed {
		if !safe(name) {
			return nil, fmt.Errorf("unsafe prior metadata path")
		}
		result[prefix+name] = append([]byte{}, data...)
	}
	for name, data := range nextFeed {
		if !safe(name) {
			return nil, fmt.Errorf("unsafe new metadata path")
		}
		// Bare role names are mutable pointers; numbered immutable metadata and roots
		// must be retained unchanged. The real verifier must check role/version too.
		mutable := name == "timestamp.json" || name == "snapshot.json" || name == "targets.json"
		if old, ok := previousFeed[name]; ok && !mutable && !bytes.Equal(old, data) {
			return nil, fmt.Errorf("immutable metadata replaced")
		}
		result[prefix+name] = append([]byte{}, data...)
	}
	return result, nil
}

func TestSharedWebsiteCompositionPreservesSignedBytes(t *testing.T) {
	r := newRepo(t)
	root := r.get("/metadata/1.root.json")
	site := map[string][]byte{"index.html": []byte("<html>website</html>"), "assets/style.css": []byte("body{}"), "CNAME": []byte("example.invalid")}
	prior := map[string][]byte{"1.root.json": root, "timestamp.json": []byte("previous timestamp")}
	next := map[string][]byte{"timestamp.json": r.get("/metadata/timestamp.json"), "1.targets.json": r.get("/metadata/1.targets.json"), "1.snapshot.json": r.get("/metadata/1.snapshot.json")}
	merged, e := composeSite(site, prior, next)
	if e != nil {
		t.Fatal(e)
	}
	for name, data := range site {
		if !bytes.Equal(merged[name], data) {
			t.Fatalf("website modified: %s", name)
		}
	}
	if !bytes.Equal(merged["updates/tuf/1.root.json"], root) {
		t.Fatal("old root lost or rewritten")
	}
	for name, data := range next {
		if !bytes.Equal(merged["updates/tuf/"+name], data) {
			t.Fatalf("signed bytes changed: %s", name)
		}
	}
	// Website changes must carry forward the full latest metadata snapshot.
	changed := map[string][]byte{"index.html": []byte("new website")}
	complete := map[string][]byte{}
	for name, data := range merged {
		if strings.HasPrefix(name, "updates/tuf/") {
			complete[strings.TrimPrefix(name, "updates/tuf/")] = data
		}
	}
	again, e := composeSite(changed, complete, nil)
	if e != nil {
		t.Fatal(e)
	}
	for name, data := range complete {
		if !bytes.Equal(again["updates/tuf/"+name], data) {
			t.Fatal("site rebuild dropped metadata")
		}
	}
}
func TestSharedWebsiteRejectsReservedPathAndRootReplacement(t *testing.T) {
	_, e := composeSite(map[string][]byte{"updates/tuf/timestamp.json": []byte("page builder output")}, nil, nil)
	failIfNil(t, e)
	_, e = composeSite(nil, map[string][]byte{"1.root.json": []byte("old")}, map[string][]byte{"1.root.json": []byte("changed")})
	failIfNil(t, e)
	for _, p := range []string{"../index.html", "/index.html", "a/../../index.html", "..\\index.html"} {
		_, e = composeSite(nil, nil, map[string][]byte{p: []byte("bad")})
		failIfNil(t, e)
	}
}
