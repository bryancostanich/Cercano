package installation

import "testing"

// Regression test from the bounded review. Pure, like the rest of the
// package: no filesystem access, no package-manager execution, no network.

func TestClassifyWithDelegation_ExecutableMustResideUnderDelegatedRoot(t *testing.T) {
	// The delegation names the exact canonical root AND executable, and the
	// observed evidence agrees with both — but the executable is NOT
	// beneath the delegated root. A root/exe pair that merely agrees with
	// itself is not a binding: the canonical executable must reside inside
	// the canonical delegated root, with the boundary respected (a
	// prefix-naive check would also let sibling directories through).
	cases := []struct {
		name, exe, root string
	}{
		{"sibling-file-of-root", `C:\Users\me\AppData\Local\cercano.exe`, `C:\Users\me\AppData\Local\Cercano`},
		{"sibling-directory-prefix", `C:\Users\me\AppData\Local\CercanoOther\cercano.exe`, `C:\Users\me\AppData\Local\Cercano`},
		{"parent-escape", `C:\Users\me\cercano.exe`, `C:\Users\me\AppData\Local\Cercano`},
	}
	for _, tc := range cases {
		exe := ExecutableEvidence{
			Platform:     "windows",
			Arch:         "amd64",
			ResolvedPath: tc.exe,
			ResolvedRoot: tc.root,
		}
		managers := []ManagerEvidence{chocoEvidence(true, ScopeUser, exe.ResolvedPath)}
		dl := DelegationEvidence{
			Owner:                OwnerChocolatey,
			Platform:             "windows",
			Arch:                 "amd64",
			Scope:                ScopeUser,
			Root:                 tc.root,
			Executable:           tc.exe,
			ReleaseChannel:       "stable",
			FeedID:               "feed-stable-main",
			Source:               DelegatedUpdateSource,
			ContractVersion:      "1",
			ContractVerified:     true,
			ConsentRecorded:      true,
			RegistrationRetained: true,
		}
		got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, dl)
		if got.SelfUpdateRole != SelfUpdateRoleNone {
			t.Fatalf("%s: delegation honored for an executable outside the delegated root: %+v", tc.name, got)
		}
		if got.Status != StatusActionable || got.Identity.Owner != OwnerChocolatey {
			t.Fatalf("%s: refusal must not change the chocolatey classification: %+v", tc.name, got)
		}
	}

	// The genuine case still passes: the executable inside the delegated
	// root, boundary respected.
	exe, managers, dl := delegatedWindowsInstall()
	if got := ClassifyWithDelegation(exe, managers, SelfManagedEvidence{}, dl); got.SelfUpdateRole != SelfUpdateRoleDelegate {
		t.Fatal("in-root delegation no longer honored")
	}
}
