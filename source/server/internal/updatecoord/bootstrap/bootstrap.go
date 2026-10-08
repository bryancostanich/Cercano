// Package bootstrap stages one existing caller-trusted image. Digest checking
// proves integrity, not publisher authenticity. No execution, downloads, defaults,
// selection changes or old-version cleanup occur here. Callers establish source
// trust and protect the state directory (including Windows ACLs).
//
// Placement checks resolve filesystem aliases. Cleanup is identity-checked and
// nonrecursive; unexpected/replaced objects are preserved and reported. These
// checks do not promise protection against a hostile same-user directory owner.
package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"cercano/source/server/internal/updatecoord/oneshot"
	"cercano/source/server/internal/updatecoord/state"
)

const MaxImageBytes int64 = 1 << 32
const copyChunkSize = 256 << 10
const copyName = "image"
const stagingPrefix = "bootstrap-op"

var (
	ErrInvalidRequest   = errors.New("bootstrap: invalid request")
	ErrUnsafePlacement  = errors.New("bootstrap: unsafe staging placement")
	ErrInvalidSource    = errors.New("bootstrap: invalid source image")
	ErrDigestMismatch   = errors.New("bootstrap: source digest mismatch")
	ErrSourceChanged    = errors.New("bootstrap: source changed during copy")
	ErrCopyVerification = errors.New("bootstrap: prepared copy failed verification")
	ErrCanceled         = errors.New("bootstrap: copy canceled")
)

type Source struct {
	Path, ExpectedSHA256 string
	ExpectedLength       int64
}

// ForbiddenRoots must name existing installation/version directories. StateRoot
// is an existing trusted per-installation directory, never an inferred home.
type Request struct {
	Oneshot        oneshot.Request
	Source         Source
	StateRoot      string
	ForbiddenRoots []string
}

// PreparedImage is a receipt, not a guarantee that the file can never change.
// The later execution boundary must revalidate the image before launching it.
type PreparedImage struct {
	installID                string
	operationID              int64
	path, stagingDir, sha256 string
	length                   int64
}

func (p *PreparedImage) InstallID() string  { return p.installID }
func (p *PreparedImage) OperationID() int64 { return p.operationID }
func (p *PreparedImage) Path() string       { return p.path }
func (p *PreparedImage) StagingDir() string { return p.stagingDir }
func (p *PreparedImage) SHA256() string     { return p.sha256 }
func (p *PreparedImage) Length() int64      { return p.length }

var testHookChunk func(int64)
var testHookAfterCopy func()
var testHookVerifyRead func(string, int64)

func preparedCopyName() string {
	if runtime.GOOS == "windows" {
		return copyName + ".exe"
	}
	return copyName
}

func Prepare(ctx context.Context, req Request) (image *PreparedImage, err error) {
	if ctx == nil {
		return nil, ErrInvalidRequest
	}
	if err = ctx.Err(); err != nil {
		return nil, errors.Join(ErrCanceled, err)
	}
	if err = validateRequest(req); err != nil {
		return nil, err
	}
	named, err := os.Lstat(req.Source.Path)
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}
	if !named.Mode().IsRegular() || named.Size() != req.Source.ExpectedLength {
		return nil, ErrInvalidSource
	}
	src, err := openSourceFile(req.Source.Path)
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}
	defer src.Close()
	before, err := src.Stat()
	if err != nil {
		return nil, errors.Join(ErrInvalidSource, err)
	}
	if !before.Mode().IsRegular() || !sameIdentity(named, before) {
		return nil, ErrSourceChanged
	}
	rootInfo, err := os.Lstat(req.StateRoot)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(req.StateRoot, fmt.Sprintf("%s%d-", stagingPrefix, req.Oneshot.OperationID))
	if err != nil {
		return nil, err
	}
	owned := ownedStage{root: req.StateRoot, dir: dir, rootInfo: rootInfo}
	owned.dirInfo, err = os.Lstat(dir)
	defer func() {
		if err != nil {
			err = errors.Join(err, owned.cleanup())
		}
	}()
	if err != nil {
		return nil, err
	}
	owned.path = filepath.Join(dir, preparedCopyName())
	dst, err := os.OpenFile(owned.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return nil, err
	}
	// Registered after cleanup defer: close the open image before cleaning it.
	defer dst.Close()
	owned.fileInfo, err = dst.Stat()
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	buf := make([]byte, copyChunkSize)
	var copied int64
	reader := io.LimitReader(src, req.Source.ExpectedLength+1)
	for {
		if e := ctx.Err(); e != nil {
			return nil, errors.Join(ErrCanceled, e)
		}
		n, re := reader.Read(buf)
		if n > 0 {
			if copied+int64(n) > req.Source.ExpectedLength {
				return nil, ErrSourceChanged
			}
			if _, e := dst.Write(buf[:n]); e != nil {
				return nil, e
			}
			hash.Write(buf[:n])
			copied += int64(n)
			if testHookChunk != nil {
				testHookChunk(copied)
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return nil, re
		}
	}
	if copied != req.Source.ExpectedLength {
		return nil, ErrSourceChanged
	}
	if hex.EncodeToString(hash.Sum(nil)) != req.Source.ExpectedSHA256 {
		return nil, ErrDigestMismatch
	}
	if testHookAfterCopy != nil {
		testHookAfterCopy()
	}
	if err = dst.Sync(); err != nil {
		return nil, err
	}
	if err = dst.Close(); err != nil {
		return nil, err
	}
	after, e := src.Stat()
	if e != nil {
		return nil, errors.Join(ErrSourceChanged, e)
	}
	current, e := os.Lstat(req.Source.Path)
	if e != nil {
		return nil, errors.Join(ErrSourceChanged, e)
	}
	if !current.Mode().IsRegular() || !sameIdentity(before, after) || !sameIdentity(before, current) {
		return nil, ErrSourceChanged
	}
	if err = owned.checkBinding(); err != nil {
		return nil, err
	}
	if err = verifyCopy(ctx, owned.path, req.Source.ExpectedSHA256, req.Source.ExpectedLength); err != nil {
		return nil, err
	}
	return &PreparedImage{installID: req.Oneshot.InstallID, operationID: req.Oneshot.OperationID, path: owned.path, stagingDir: dir, sha256: req.Source.ExpectedSHA256, length: copied}, nil
}
func validateRequest(req Request) error {
	if err := state.ValidateInstallID(req.Oneshot.InstallID); err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	if req.Oneshot.OperationID <= 0 || !filepath.IsAbs(req.Source.Path) || !filepath.IsAbs(req.StateRoot) || !validDigest(req.Source.ExpectedSHA256) || req.Source.ExpectedLength <= 0 || req.Source.ExpectedLength > MaxImageBytes || len(req.ForbiddenRoots) == 0 {
		return ErrInvalidRequest
	}
	info, err := os.Lstat(req.StateRoot)
	if err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidRequest
	}
	actualState, err := filepath.EvalSymlinks(req.StateRoot)
	if err != nil {
		return errors.Join(ErrInvalidRequest, err)
	}
	for _, root := range req.ForbiddenRoots {
		if !filepath.IsAbs(root) {
			return ErrInvalidRequest
		}
		actual, err := filepath.EvalSymlinks(root)
		if err != nil {
			return errors.Join(ErrInvalidRequest, err)
		}
		r, err := os.Stat(actual)
		if err != nil || !r.IsDir() {
			return ErrInvalidRequest
		}
		if withinPath(actual, actualState) {
			return ErrUnsafePlacement
		}
	}
	return nil
}
func verifyCopy(ctx context.Context, path, digest string, length int64) error {
	f, err := openSourceFile(path)
	if err != nil {
		return errors.Join(ErrCopyVerification, err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != length {
		return ErrCopyVerification
	}
	h := sha256.New()
	r := io.LimitReader(f, length+1)
	buf := make([]byte, copyChunkSize)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return errors.Join(ErrCanceled, e)
		}
		n, e := r.Read(buf)
		if n > 0 {
			total += int64(n)
			h.Write(buf[:n])
			if testHookVerifyRead != nil {
				testHookVerifyRead(path, total)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return errors.Join(ErrCopyVerification, e)
		}
	}
	after, e := f.Stat()
	if e != nil {
		return errors.Join(ErrCopyVerification, e)
	}
	named, e := os.Lstat(path)
	if e != nil {
		return errors.Join(ErrCopyVerification, e)
	}
	if total != length || !named.Mode().IsRegular() || !sameIdentity(before, after) || !sameIdentity(before, named) || hex.EncodeToString(h.Sum(nil)) != digest {
		return ErrCopyVerification
	}
	return nil
}

type ownedStage struct {
	root, dir, path             string
	rootInfo, dirInfo, fileInfo os.FileInfo
}

func (o ownedStage) checkBinding() error {
	if o.dirInfo == nil || o.rootInfo == nil {
		return errors.New("bootstrap: unproven staging identity")
	}
	root, e := os.Lstat(o.root)
	if e != nil {
		return e
	}
	dir, e := os.Lstat(o.dir)
	if e != nil {
		return e
	}
	if !root.IsDir() || !dir.IsDir() || !os.SameFile(root, o.rootInfo) || !os.SameFile(dir, o.dirInfo) {
		return errors.New("bootstrap: staging directory identity changed")
	}
	return nil
}
func (o ownedStage) cleanup() error {
	fail := func(e error) error { return fmt.Errorf("bootstrap: cleanup incomplete; staging retained: %w", e) }
	if e := o.checkBinding(); e != nil {
		return fail(e)
	}
	if o.fileInfo != nil {
		info, e := os.Lstat(o.path)
		if e != nil && !os.IsNotExist(e) {
			return fail(e)
		}
		if e == nil {
			if !info.Mode().IsRegular() || !os.SameFile(info, o.fileInfo) {
				return fail(errors.New("image identity changed"))
			}
			if e = os.Remove(o.path); e != nil {
				return fail(e)
			}
		}
	}
	// Never recurse: unexpected files and replaced directories are user data,
	// not permission to broaden the cleanup operation.
	if e := os.Remove(o.dir); e != nil {
		return fail(e)
	}
	return nil
}
func sameIdentity(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func withinPath(parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	if runtime.GOOS == "windows" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
