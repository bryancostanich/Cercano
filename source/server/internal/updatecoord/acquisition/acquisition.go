// Package acquisition acquires a single update target through The Update
// Framework (TUF) using the pinned go-tuf/v2 client that the approved local
// proof (efforts/cross-platform-updates/tuf-proof) validated.
//
// Scope of this slice, exactly:
//
//   - The caller supplies every trust and location input: the trusted root
//     metadata bytes, the HTTPS base URLs for signed metadata and target
//     downloads, existing private cache and output directories, and the
//     relative target path. Nothing is inferred from environment, PATH,
//     home directories, or release-provided commands. Updates are acquired
//     from configured source URLs only; no release-provided shell command,
//     script, or installer output is ever fetched or executed.
//   - The library refreshes TUF metadata against the configured metadata URL
//     (verifying signatures, versions and expiry exactly as go-tuf enforces)
//     and then downloads the requested target, which go-tuf verifies against
//     the signed length and hashes before returning any bytes.
//   - Verified bytes are written to a temporary file created inside the
//     caller-provided output directory and atomically renamed to the final
//     target filename. Only files created by this package (the single
//     temporary download file) are ever created or removed; existing cache
//     contents and user data are never modified, moved, or deleted. go-tuf
//     manages its own metadata/targets subdirectories inside the provided
//     cache directory, including cleanup of its own failed downloads.
//   - A Receipt reporting the verified length and SHA-256 from signed
//     metadata is returned only after the verified bytes are durably in
//     place.
//
// Explicitly out of scope (and absent here): archive extraction, staging,
// activation, restart, installer execution, key generation, publication,
// production URLs, production key or expiry policy, and any default
// directories. This package is not wired into any runtime, configuration,
// RPC, UI, or update flow. The exported API requires HTTPS source URLs; the
// unexported client-injection constructor exists solely for tests to drive
// the real verification against a local signed fixture over a test
// transport. It is not a public insecure-transport switch.
package acquisition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
)

// Options are the caller-supplied inputs for one verified acquisition.
// Every field is required (RequestTimeout excepted); no defaults for URLs,
// directories, or trust material exist here.
type Options struct {
	// TrustedRoot is the trusted root metadata bytes supplied by the caller
	// from its own protected provisioning. It is never fetched, never
	// written, and never re-derived here.
	TrustedRoot []byte
	// MetadataBaseURL is the HTTPS base URL of the signed TUF metadata.
	MetadataBaseURL string
	// TargetBaseURL is the HTTPS base URL of authorized target files.
	TargetBaseURL string
	// CacheDir is a trusted, existing, private directory for go-tuf client
	// state (metadata and verified target cache). It is not created and not
	// cleaned here; its existing contents are preserved untouched.
	CacheDir string
	// OutputDir is a trusted, existing, private directory into which the
	// verified target file is atomically published. It is not created and
	// not cleaned here; only the owned temporary download file is created
	// and only that file is removed on failure.
	OutputDir string
	// RequestTimeout bounds every metadata and target fetch (including
	// retries). Values <= 0 use defaultRequestTimeout. The caller's context
	// remains the overall bound.
	RequestTimeout time.Duration
}

// Receipt describes one completed verified acquisition. Length and SHA256
// are the values authorized by signed TUF metadata, which the downloaded
// bytes were verified against before being written.
type Receipt struct {
	// TargetPath is the signed relative target path that was requested.
	TargetPath string
	// OutputPath is the location of the verified bytes in OutputDir.
	OutputPath string
	// Length is the signed target length in bytes.
	Length int64
	// SHA256 is the raw 32-byte SHA-256 digest authorized by signed
	// metadata for this target.
	SHA256 []byte
}

const (
	defaultRequestTimeout  = 30 * time.Second
	fetchRetryInterval     = 100 * time.Millisecond
	fetchRetryAttempts     = 3
	ownedTempFilePattern   = ".cercano-acquire-*"
	cacheMetadataSubdir    = "metadata"
	cacheTargetsSubdir     = "targets"
	verifiedFileMode     = 0o600
)

// ErrNoSignedSHA256 reports that signed metadata authorized the target
// without the SHA-256 digest this package requires for its receipt.
var ErrNoSignedSHA256 = errors.New("signed metadata carries no sha256 digest for the target")

// Acquire refreshes trusted metadata from the configured HTTPS source and
// downloads the requested relative target path, returning a receipt only
// after the bytes verified against the signed length and SHA-256 are durably
// written into OutputDir. The context bounds the whole acquisition; a
// canceled context fails closed with no output file left behind.
func Acquire(ctx context.Context, opts Options, targetRelPath string) (*Receipt, error) {
	if err := opts.validate(true); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, opts.requestTimeout())
	defer cancel()
	return acquire(ctx, opts, targetRelPath, &http.Client{})
}

// acquire performs the acquisition with a caller-provided HTTP client. It is
// unexported so tests can inject a transport against a local signed fixture;
// it is not a public insecure-HTTP switch. The caller is responsible for
// HTTPS enforcement (Acquire applies it); acquire still validates every
// other input and fails closed.
func acquire(ctx context.Context, opts Options, targetRelPath string, client *http.Client) (*Receipt, error) {
	if err := opts.validate(false); err != nil {
		return nil, err
	}
	if err := validateTargetPath(targetRelPath); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("acquisition: HTTP client is required")
	}
	cfg, err := config.New(opts.MetadataBaseURL, opts.TrustedRoot)
	if err != nil {
		return nil, fmt.Errorf("acquisition: metadata configuration rejected: %w", err)
	}
	cfg.RemoteTargetsURL = opts.TargetBaseURL
	cfg.LocalMetadataDir = filepath.Join(opts.CacheDir, cacheMetadataSubdir)
	cfg.LocalTargetsDir = filepath.Join(opts.CacheDir, cacheTargetsSubdir)
	if err = cfg.EnsurePathsExist(); err != nil {
		return nil, fmt.Errorf("acquisition: cannot use cache directory %q: %w", opts.CacheDir, err)
	}
	// Bind every fetch to this acquisition's context so cancellation and the
	// request bound are honored inside the library's default fetcher.
	client.Transport = &ctxRoundTripper{ctx: ctx, base: client.Transport}
	if err = cfg.SetDefaultFetcherHTTPClient(client); err != nil {
		return nil, fmt.Errorf("acquisition: fetcher configuration rejected: %w", err)
	}
	// Bounded, explicit retry policy: never rely on library defaults that
	// may mean "unlimited" (see the tuf-proof observations).
	if err = cfg.SetDefaultFetcherRetry(fetchRetryInterval, fetchRetryAttempts); err != nil {
		return nil, fmt.Errorf("acquisition: retry configuration rejected: %w", err)
	}
	u, err := updater.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("acquisition: trusted root refused: %w", err)
	}
	if err = u.Refresh(); err != nil {
		return nil, fmt.Errorf("acquisition: trusted metadata refresh refused: %w", err)
	}
	info, err := u.GetTargetInfo(targetRelPath)
	if err != nil {
		return nil, fmt.Errorf("acquisition: target %q is not authorized by signed metadata: %w", targetRelPath, err)
	}
	_, data, err := u.DownloadTarget(info, "", "")
	if err != nil {
		return nil, fmt.Errorf("acquisition: verified download refused for %q: %w", targetRelPath, err)
	}
	digest, ok := info.Hashes["sha256"]
	if !ok || len(digest) != sha256.Size {
		return nil, fmt.Errorf("acquisition: target %q: %w", targetRelPath, ErrNoSignedSHA256)
	}
	// Defense in depth: re-check the signed digest and length against the
	// exact bytes that will be written, independent of library internals.
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], digest) || info.Length != int64(len(data)) {
		return nil, fmt.Errorf("acquisition: target %q: downloaded bytes fail the signed length or sha256: %w",
			targetRelPath, &metadata.ErrLengthOrHashMismatch{})
	}
	outputPath := filepath.Join(opts.OutputDir, path.Base(targetRelPath))
	if err = writeVerified(opts.OutputDir, outputPath, data); err != nil {
		return nil, err
	}
	return &Receipt{
		TargetPath: targetRelPath,
		OutputPath: outputPath,
		Length:     info.Length,
		SHA256:     digest,
	}, nil
}

// writeVerified writes the verified bytes to an owned temporary file inside
// dir and atomically renames it to final. On any error only the owned
// temporary file is removed; no other file is touched.
func writeVerified(dir, final string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ownedTempFilePattern)
	if err != nil {
		return fmt.Errorf("acquisition: cannot create a temporary file in the output directory: %w", err)
	}
	name := tmp.Name()
	open := true
	defer func() {
		if open {
			_ = tmp.Close()
			// Cleanup only the file this package created.
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(verifiedFileMode); err != nil {
		return fmt.Errorf("acquisition: cannot protect the temporary file: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("acquisition: writing verified bytes failed: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("acquisition: syncing verified bytes failed: %w", err)
	}
	if err = tmp.Close(); err != nil {
		open = false
		return fmt.Errorf("acquisition: closing verified bytes failed: %w", err)
	}
	open = false
	if err = os.Rename(name, final); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("acquisition: publishing verified bytes to %q failed: %w", final, err)
	}
	return nil
}

// ctxRoundTripper attaches the acquisition context to every outgoing fetch so
// cancellation and deadlines propagate into the library's default fetcher.
type ctxRoundTripper struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t *ctxRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req.WithContext(t.ctx))
}

func (o *Options) requestTimeout() time.Duration {
	if o.RequestTimeout > 0 {
		return o.RequestTimeout
	}
	return defaultRequestTimeout
}

func (o *Options) validate(requireHTTPS bool) error {
	if len(o.TrustedRoot) == 0 {
		return errors.New("acquisition: trusted root metadata bytes are required")
	}
	if err := checkBaseURL(o.MetadataBaseURL, "metadata", requireHTTPS); err != nil {
		return err
	}
	if err := checkBaseURL(o.TargetBaseURL, "target", requireHTTPS); err != nil {
		return err
	}
	if err := requireExistingDir(o.CacheDir, "cache"); err != nil {
		return err
	}
	return requireExistingDir(o.OutputDir, "output")
}

func requireExistingDir(dir, role string) error {
	if dir == "" {
		return fmt.Errorf("acquisition: %s directory is required", role)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("acquisition: %s directory %q must already exist: %w", role, dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("acquisition: %s path %q is not a directory", role, dir)
	}
	return nil
}

func checkBaseURL(raw, role string, requireHTTPS bool) error {
	if raw == "" {
		return fmt.Errorf("acquisition: %s base URL is required", role)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("acquisition: %s base URL %q is invalid: %w", role, raw, err)
	}
	if requireHTTPS && u.Scheme != "https" {
		return fmt.Errorf("acquisition: %s base URL %q must use https", role, raw)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("acquisition: %s base URL %q must be a plain origin-based path URL", role, raw)
	}
	return nil
}

// validateTargetPath accepts only a normalized, relative, slash-separated
// path without traversal, so the published output name can never escape the
// output directory. TUF metadata authorization additionally refuses any path
// the repository has not signed.
func validateTargetPath(p string) error {
	if p == "" {
		return errors.New("acquisition: target path is required")
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("acquisition: target path %q must not use backslashes", p)
	}
	if path.IsAbs(p) {
		return fmt.Errorf("acquisition: target path %q must be relative", p)
	}
	if cleaned := path.Clean(p); cleaned != p {
		return fmt.Errorf("acquisition: target path %q must be normalized (%q)", p, cleaned)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("acquisition: target path %q must not contain traversal segments", p)
		}
	}
	return nil
}
