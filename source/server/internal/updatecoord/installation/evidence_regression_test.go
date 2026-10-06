package installation

import "testing"

func TestRejectUnboundEnrollment(t *testing.T) {
	for _, exe := range []ExecutableEvidence{{}, {Platform: "linux", Arch: "amd64", ResolvedPath: "/tmp/another/cercano", ResolvedRoot: "/tmp/another"}} {
		got := Classify(exe, nil, SelfManagedEvidence{Enrolled: true, Scope: ScopeUser})
		if got.AutoEditable || got.Status == StatusActionable {
			t.Fatalf("unbound enrollment granted update authority: %+v", got)
		}
	}
}
func TestRejectRetargetedOwnedSymlink(t *testing.T) {
	exe := ExecutableEvidence{Platform: "darwin", Arch: "arm64", ResolvedPath: "/tmp/manual/cercano", LinkPath: "/opt/homebrew/bin/cercano"}
	got := Classify(exe, []ManagerEvidence{brewEvidence(true, exe.LinkPath)}, SelfManagedEvidence{})
	if got.Identity.Owner != OwnerUnknown || got.Status == StatusActionable {
		t.Fatalf("owned launcher incorrectly implies ownership of manual target: %+v", got)
	}
}
func TestRejectRelativeExecutableEvidence(t *testing.T) {
	exe := ExecutableEvidence{Platform: "linux", ResolvedPath: "relative/cercano"}
	got := Classify(exe, []ManagerEvidence{{Manager: OwnerAPT, PackageInstalled: true, OwnedFiles: []string{exe.ResolvedPath}}}, SelfManagedEvidence{})
	if got.Status == StatusActionable {
		t.Fatalf("relative path accepted: %+v", got)
	}
}

func TestAvailabilityMustMatchInstallationSource(t *testing.T) {
	exe := ExecutableEvidence{Platform: "darwin", Arch: "arm64", ResolvedPath: "/opt/homebrew/Cellar/cercano/1/bin/cercano"}
	c := Classify(exe, []ManagerEvidence{brewEvidence(true, exe.ResolvedPath)}, SelfManagedEvidence{})
	available := ReleaseAvailability{AnnouncedVersion: "2.0.0", AnnouncedSource: "github", InstallableVersion: "2.0.0", InstallableSource: "tuf", Verified: true}
	if available.InstallableFor(c) {
		t.Fatal("self-managed feed cannot update Homebrew installation")
	}
	available.InstallableSource = "homebrew"
	if !available.InstallableFor(c) {
		t.Fatal("matching verified package source refused")
	}
	available.Verified = false
	if available.InstallableFor(c) {
		t.Fatal("unverified source accepted")
	}
}
func TestRejectEnrollmentBoundToOtherRoot(t *testing.T) {
	exe := ExecutableEvidence{Platform: "linux", Arch: "amd64", ResolvedPath: "/home/me/a/bin/cercano", ResolvedRoot: "/home/me/a"}
	self := SelfManagedEvidence{Enrolled: true, Scope: ScopeUser, Root: "/home/me/b", Executable: exe.ResolvedPath}
	got := Classify(exe, nil, self)
	if got.AutoEditable || got.Status == StatusActionable {
		t.Fatal("other-root enrollment accepted")
	}
}
func TestCanonicalPathsAreValidatedForTargetPlatform(t *testing.T) {
	cases := []struct {
		platform, path string
		valid          bool
	}{
		{"windows", `C:\Users\me\Cercano\cercano.exe`, true},
		{"windows", `C:relative.exe`, false},
		{"windows", `C:\Users\..\other.exe`, false},
		{"windows", `\\server\share\Cercano\cercano.exe`, true},
		{"windows", `\\?\C:\Cercano\cercano.exe`, false},
		{"linux", "/home/me/Cercano/bin/cercano", true},
		{"linux", "/home/me/../other/cercano", false},
		{"linux", "relative/cercano", false},
	}
	for _, c := range cases {
		if (canonicalPath(c.path, c.platform) != "") != c.valid {
			t.Errorf("%s %q valid=%v", c.platform, c.path, c.valid)
		}
	}
}
