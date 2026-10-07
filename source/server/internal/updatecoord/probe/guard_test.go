package probe

import (
	"os"
	"testing"

	"cercano/source/server/internal/updatecoord/installation"
)

func TestInPlaceExecutableChangeInvalidatesObservation(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	writeFile(t, p.exe, "different size executable rewritten in place")
	if err := f.StillObserved(); err == nil {
		t.Fatal("in-place mutation accepted")
	}
}
func TestStaleFactsCannotAuthorizeSelfEnrollment(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	result := Corroborate(f, nil)
	if err := os.Remove(p.exe); err != nil {
		t.Fatal(err)
	}
	self := installation.SelfManagedEvidence{Root: f.ResolvedRoot(), Executable: f.ResolvedPath(), Enrolled: true, Scope: installation.ScopeUser}
	if c, err := result.Classify(self); err == nil && c.AutoEditable {
		t.Fatalf("stale facts granted eligibility: %+v", c)
	}
}
func TestSameManagerDifferentSourcesCannotChooseFirst(t *testing.T) {
	p := newInstall(t)
	f := mustObserve(t, Options{ExecutablePath: p.exe, RootPath: p.root})
	first := brewReceipt(p.exe)
	second := first
	second.PackageName = "different-package"
	second.SourceID = "different-source"
	result := Corroborate(f, []ReceiptProvider{&fakeProvider{name: "receipts", platforms: hostPlatforms(), receipts: []Receipt{first, second}}})
	c, err := result.Classify(installation.SelfManagedEvidence{})
	if err == nil && c.Status == installation.StatusActionable {
		t.Fatalf("competing same-manager sources collapsed: %+v", c)
	}
}
