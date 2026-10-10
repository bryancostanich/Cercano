package activationtxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/updatecoord/archivecheck"
	"cercano/source/server/internal/updatecoord/imagecheck"
	"cercano/source/server/internal/updatecoord/privdir"
	"cercano/source/server/internal/updatecoord/state"
)

var ErrTargetImages = errors.New("activationtxn: target executable verification failed")

// TargetImages is supplied by the trusted installation/acquisition policy, not
// by an archive or user-supplied operation arguments. Manifest must come from
// authenticated preflight. Its digest is bound to the journal. VersionsDirectory
// is the installation's existing protected version namespace; the target path
// below it comes solely from the journal. Member names and OS/architecture are
// trusted packaging policy. Callers must not mutate this object during Switch.
// This verifies format and content, not publisher signing, version or health.
type TargetImages struct {
	VersionsDirectory         string
	Manifest                  *archivecheck.Manifest
	AgentMember, ClientMember string
	GOOS, GOARCH              string
}

func verifyTargetImages(ctx context.Context, req Request, j state.ActivationJournal) error {
	p := req.Images
	if p == nil || p.Manifest == nil || p.Manifest.ArchiveSHA256 != j.VerifiedArtifactSHA256 || p.AgentMember == "" || p.ClientMember == "" || p.AgentMember == p.ClientMember || !filepath.IsAbs(p.VersionsDirectory) || filepath.Clean(p.VersionsDirectory) != p.VersionsDirectory {
		return ErrTargetImages
	}
	if err := state.ValidateStagedRelativeIdentifier(j.StagedVersionDir); err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	if err := privdir.VerifyExisting(p.VersionsDirectory); err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	root, err := os.OpenRoot(p.VersionsDirectory)
	if err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	defer root.Close()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	relative := ""
	for _, part := range strings.Split(j.StagedVersionDir, "/") {
		if relative != "" {
			relative += "/"
		}
		relative += part
		info, err := root.Lstat(relative)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrTargetImages
		}
	}
	targetRoot, err := root.OpenRoot(j.StagedVersionDir)
	if err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	defer targetRoot.Close()
	targetInfo, err := targetRoot.Stat(".")
	if err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	stage := archivecheck.Staged{Dir: filepath.Join(p.VersionsDirectory, j.StagedVersionDir), Manifest: p.Manifest}
	if err := imagecheck.CheckStaged(ctx, stage, []string{p.AgentMember, p.ClientMember}, p.GOOS, p.GOARCH); err != nil {
		return errors.Join(ErrTargetImages, err)
	}
	namedRoot, err := os.Lstat(p.VersionsDirectory)
	if err != nil || !namedRoot.IsDir() || !os.SameFile(rootInfo, namedRoot) {
		return ErrTargetImages
	}
	namedTarget, err := root.Lstat(j.StagedVersionDir)
	if err != nil || !namedTarget.IsDir() || !os.SameFile(targetInfo, namedTarget) {
		return ErrTargetImages
	}
	return nil
}
