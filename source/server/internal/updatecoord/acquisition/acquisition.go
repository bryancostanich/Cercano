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
//   - Verified bytes are written to a uniquely named, library-owned
//     temporary file inside the caller-provided output directory and left
//     there under that owned name: the owned file is never renamed onto any
//     other filename, so an existing file that happens to share the
//     target's basename can never be overwritten. Only files created by
//     this package (the single owned download file) are ever created or
//     removed by this package. The cache directory itself is never deleted
//     here and no non-library entry in either directory is modified,
//     moved, or deleted; note that go-tuf refresh deliberately updates
//     the metadata files it owns inside the cache's metadata
//     subdirectory — the cache is library-owned mutable state, not an
//     immutable snapshot. go-tuf manages its own metadata/targets
//     subdirectories inside the provided cache directory, including
//     cleanup of its own failed downloads.
//   - The cache and output roots must be absolute paths to real (non
//     symlink) directories, and the library refuses to run before any
//     existing metadata/targets cache child that is a symlink or not a
//     directory, and before any existing entry inside those library-owned
//     children that is not a regular file: the pinned go-tuf v2.4.2 reads
//     cached metadata with os.ReadFile and writes cached targets with
//     os.WriteFile, both of which follow a symlink leaf (or block on a
//     FIFO), so an unsafe leaf would redirect the library's reads or
//     writes outside the caller's cache. Refusal happens before the
//     library touches the cache and never deletes, resets, or rewrites
//     anything in it. "Private" is the caller's provisioning prerequisite
//     for these roots; this package makes no access-control promise about
//     them and does not defend against a hostile process racing the same
//     user's cache.
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
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
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
	// state (metadata and verified target cache). It must be an absolute
	// path to a real directory that is not itself a symlink; its existing
	// metadata/targets children, if present, must be real directories. It
	// is not created and never deleted here; go-tuf refresh updates the
	// metadata files it owns inside it, and no other existing entry in it
	// is modified, moved, or deleted. Privacy of this directory is the
	// caller's provisioning prerequisite, not an access-control promise
	// made or verified here.
	CacheDir string
	// OutputDir is a trusted, existing, private directory into which the
	// verified target bytes are published under a uniquely named,
	// library-owned file. It must be an absolute path to a real directory
	// that is not itself a symlink. It is not created and not cleaned here;
	// only the owned download file is created and only that file is removed
	// on failure. Privacy of this directory is the caller's provisioning
	// prerequisite, not an access-control promise made or verified here.
	OutputDir string
	// RequestTimeout bounds the WHOLE acquisition — every metadata and
	// target fetch, including retries — as one overall deadline. Zero uses
	// defaultRequestTimeout. Negative values and values above
	// maxRequestTimeout are rejected: a single verified acquisition is
	// expected to complete well inside that ceiling, and an oversized
	// value is treated as a misconfiguration. The caller's context
	// remains an additional bound.
	RequestTimeout time.Duration
	// MaxTargetBytes optionally lowers the per-target signed-length bound.
	// Zero uses hardMaxTargetBytes. Values above hardMaxTargetBytes are
	// rejected: because go-tuf downloads into memory, this bound is a hard
	// in-memory safety cap, not a caller-tunable transport limit.
	MaxTargetBytes int64
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
	defaultRequestTimeout = 30 * time.Second
	// maxRequestTimeout is the largest overall acquisition bound this
	// package accepts; anything above it is a misconfiguration, not a
	// meaningful deadline for one verified acquisition.
	maxRequestTimeout    = 10 * time.Minute
	fetchRetryInterval   = 100 * time.Millisecond
	fetchRetryAttempts   = 3
	ownedTempFilePattern = ".cercano-acquire-*"
	cacheMetadataSubdir  = "metadata"
	cacheTargetsSubdir   = "targets"
	verifiedFileMode     = 0o600
	// hardMaxTargetBytes is the maximum signed target length this package
	// will download. go-tuf v2 materializes a downloaded target fully in
	// memory before verification, so this constant bounds worst-case memory
	// use per acquisition no matter what signed metadata authorizes. It is
	// a conservative in-memory cap (256 MiB), not a statement about
	// legitimate update sizes.
	hardMaxTargetBytes = 256 << 20
	maxRedirects       = 5
)

// osClose is a hook so tests can force the failure path after the owned
// file's contents are flushed; production always uses os.File.Close.
var osClose = (*os.File).Close

// ErrNoSignedSHA256 reports that signed metadata authorized the target
// without the SHA-256 digest this package requires for its receipt.
var ErrNoSignedSHA256 = errors.New("signed metadata carries no sha256 digest for the target")

// Acquire refreshes trusted metadata from the configured HTTPS source and
// downloads the requested relative target path, returning a receipt only
// after the bytes verified against the signed length and SHA-256 are durably
// written into OutputDir. The context bounds the whole acquisition; a
// canceled context fails closed with no output file left behind.
func Acquire(ctx context.Context, opts Options, targetRelPath string) (*Receipt, error) {
	if ctx == nil {
		return nil, errors.New("acquisition: context is required")
	}
	if err := opts.validate(true); err != nil {
		return nil, err
	}
	return acquire(ctx, opts, targetRelPath, &http.Client{})
}

// acquire performs the acquisition with a caller-provided HTTP client. It is
// unexported so tests can inject a transport against a local signed fixture;
// it is not a public insecure-HTTP switch. The caller is responsible for
// HTTPS enforcement of the configured source URLs (Acquire applies it);
// acquire still validates every other input and, like Acquire, enforces the
// same overall time bounds and the same on-the-wire policy — HTTPS and no
// URL userinfo on every request and redirect, a hard redirect cap, and
// refusal of any downgrade before the insecure hop is attempted. It fails
// closed, and it never mutates the caller-provided http.Client.
func acquire(ctx context.Context, opts Options, targetRelPath string, client *http.Client) (*Receipt, error) {
	if ctx == nil {
		return nil, errors.New("acquisition: context is required")
	}
	if err := opts.validate(false); err != nil {
		return nil, err
	}
	if err := validateTargetPath(targetRelPath); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("acquisition: HTTP client is required")
	}
	// The same overall bound as the exported API is applied here, so the
	// unexported test path cannot bypass cancellation or the timeout.
	ctx, cancel := context.WithTimeout(ctx, opts.requestTimeout())
	defer cancel()
	cfg, err := config.New(opts.MetadataBaseURL, opts.TrustedRoot)
	if err != nil {
		return nil, fmt.Errorf("acquisition: metadata configuration rejected: %w", err)
	}
	cfg.RemoteTargetsURL = opts.TargetBaseURL
	cfg.LocalMetadataDir = filepath.Join(opts.CacheDir, cacheMetadataSubdir)
	cfg.LocalTargetsDir = filepath.Join(opts.CacheDir, cacheTargetsSubdir)
	// Refuse unsafe existing cache children BEFORE the library creates or
	// writes anything: a symlinked or non-directory metadata/targets child
	// would redirect the library's writes outside the caller's cache. No
	// existing entry is removed or modified on refusal.
	if err = validateCacheLayout(opts.CacheDir); err != nil {
		return nil, err
	}
	if err = cfg.EnsurePathsExist(); err != nil {
		return nil, fmt.Errorf("acquisition: cannot use cache directory %q: %w", opts.CacheDir, err)
	}
	// Never mutate the caller's http.Client: use a shallow clone so the
	// caller's transport (including a custom test transport) is preserved
	// and referenced, not replaced, while all policy is applied to the
	// clone alone.
	fetchClient := *client
	// Bind every outgoing request to this acquisition's context so
	// cancellation and the deadline propagate into every fetch, and
	// enforce HTTPS with no URL userinfo on every request the client
	// sends — initial requests and every redirect hop alike.
	fetchClient.Transport = &ctxRoundTripper{ctx: ctx, base: client.Transport}
	// Refuse every redirect before the (possibly insecure or
	// credential-bearing) hop is attempted, and cap the chain.
	fetchClient.CheckRedirect = checkRedirect
	// Bounded, explicit retry policy. The go-tuf default fetcher wraps
	// every fetch in backoff.Retry and never marks any error permanent, so
	// whenever retries are configured (SetRetry/SetRetryOptions) it retries
	// every error — including deterministic HTTP status responses such as
	// the 404 its own root-version walk relies on as its expected probe
	// terminator, length mismatches, and (with a policy-checking client)
	// redirect denials; only its default options (MaxTries 1) keep it to a
	// single attempt. Ours retries only transport-level transient
	// failures; HTTP statuses, size-bound refusals, wire-policy denials,
	// malformed URLs, and cancellation are deterministic and non-retryable.
	cfg.Fetcher = &boundedFetcher{ctx: ctx, client: &fetchClient}
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
	// Refuse before any target bytes are fetched: a target signed without a
	// usable SHA-256 digest can never produce a receipt, and a signed
	// length above the configured bound must never be downloaded.
	digest, ok := info.Hashes["sha256"]
	if !ok || len(digest) != sha256.Size {
		return nil, fmt.Errorf("acquisition: target %q: %w", targetRelPath, ErrNoSignedSHA256)
	}
	if err = opts.checkTargetLength(info.Length); err != nil {
		return nil, fmt.Errorf("acquisition: target %q: %w", targetRelPath, err)
	}
	_, data, err := u.DownloadTarget(info, "", "")
	if err != nil {
		return nil, fmt.Errorf("acquisition: verified download refused for %q: %w", targetRelPath, err)
	}
	// Defense in depth: re-check the signed digest and length against the
	// exact bytes that will be written, independent of library internals.
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], digest) || info.Length != int64(len(data)) {
		return nil, fmt.Errorf("acquisition: target %q: downloaded bytes fail the signed length or sha256: %w",
			targetRelPath, &metadata.ErrLengthOrHashMismatch{})
	}
	// Publish under a unique library-owned name inside the output
	// directory. The owned file is never renamed onto another filename, so
	// preexisting files (including one matching the target's basename)
	// can never be overwritten.
	outputPath, err := writeVerified(ctx, opts.OutputDir, data)
	if err != nil {
		return nil, err
	}
	return &Receipt{
		TargetPath: targetRelPath,
		OutputPath: outputPath,
		Length:     info.Length,
		SHA256:     digest,
	}, nil
}

// writeVerified writes the verified bytes to a uniquely named, library-owned
// file inside dir and returns its path. The owned file is never renamed onto
// any other filename, so preexisting files in dir can never be overwritten.
// On any failure — including a failing Close or a canceled context — only the
// owned file itself is removed; no other file is touched.
func writeVerified(ctx context.Context, dir string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("acquisition: canceled before publishing: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ownedTempFilePattern)
	if err != nil {
		return "", fmt.Errorf("acquisition: cannot create a temporary file in the output directory: %w", err)
	}
	name := tmp.Name()
	published := false
	defer func() {
		if !published {
			// Cleanup only the file this package created. Close is retried
			// (and its error ignored) because a failed Close can leave the
			// descriptor in an unusable state, yet the owned file must not
			// leak.
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(verifiedFileMode); err != nil {
		return "", fmt.Errorf("acquisition: cannot protect the temporary file: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return "", fmt.Errorf("acquisition: writing verified bytes failed: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return "", fmt.Errorf("acquisition: syncing verified bytes failed: %w", err)
	}
	if err = osClose(tmp); err != nil {
		return "", fmt.Errorf("acquisition: closing verified bytes failed: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return "", fmt.Errorf("acquisition: canceled before publishing: %w", err)
	}
	published = true
	return name, nil
}

// targetBytesCap is the per-target download bound in force for these
// options: the caller's MaxTargetBytes when set, otherwise the hard
// in-memory cap.
func (o *Options) targetBytesCap() int64 {
	if o.MaxTargetBytes > 0 {
		return o.MaxTargetBytes
	}
	return hardMaxTargetBytes
}

// checkTargetLength refuses a signed target length that is negative or above
// the configured bound, before any target bytes are fetched.
func (o *Options) checkTargetLength(length int64) error {
	if length < 0 {
		return fmt.Errorf("acquisition: signed target length %d is negative", length)
	}
	if length > o.targetBytesCap() {
		return fmt.Errorf("acquisition: signed target length %d exceeds the download bound of %d bytes",
			length, o.targetBytesCap())
	}
	return nil
}

// errUnsafeURL is the non-retryable denial error for any request or
// redirect destination that violates the on-the-wire policy (non-HTTPS
// scheme, embedded credentials, or a missing host). Denials are never
// retried: a policy violation is deterministic, not transient.
type errUnsafeURL struct {
	url    string
	reason string
}

func (e *errUnsafeURL) Error() string {
	return fmt.Sprintf("acquisition: refusing request to %q: %s", e.url, e.reason)
}

// scrubURL renders u with any userinfo removed, so error messages can
// never echo a raw (possibly credential-bearing) URL.
func scrubURL(u url.URL) string {
	u.User = nil
	return u.String()
}

// redactedURL renders a raw URL string without its userinfo for error
// messages; if it cannot be parsed, a placeholder is returned rather than
// echoing possibly credential-bearing input.
func redactedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable URL>"
	}
	return scrubURL(*u)
}

// validateURLPathEncoding enforces the path-encoding policy on any URL
// this package requests or joins a target path into: no backslash, no
// invalid percent escape, no percent-encoded separator (which any URL
// consumer would decode into a different path than configured or signed),
// no control byte, and no '.'/'..' traversal segment in the decoded path.
// Percent-encoded control bytes are caught by the decoded scan.
func validateURLPathEncoding(u *url.URL) error {
	escaped := u.EscapedPath()
	if strings.ContainsRune(escaped, '\\') {
		return errors.New("path must not contain a backslash")
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return errors.New("path contains an invalid percent escape")
	}
	lower := strings.ToLower(escaped)
	for _, enc := range []string{"%2f", "%5c"} {
		if strings.Contains(lower, enc) {
			return errors.New("path contains a percent-encoded path separator")
		}
	}
	for _, r := range decoded {
		if r < 0x20 || r == 0x7f {
			return errors.New("path contains a control byte")
		}
	}
	for _, seg := range strings.Split(decoded, "/") {
		if seg == "." || seg == ".." {
			return errors.New("path must not contain traversal segments")
		}
	}
	return nil
}

// validateRequestURL enforces the wire policy on a request destination:
// HTTPS, no embedded userinfo credentials, a host, no query or fragment,
// and unambiguous path encoding. It is applied to the initial request and
// to every redirect destination before the hop happens.
func validateRequestURL(u *url.URL) error {
	if u == nil {
		return &errUnsafeURL{url: "<nil>", reason: "destination URL is missing"}
	}
	safe := scrubURL(*u)
	switch u.Scheme {
	case "https":
	case "":
		return &errUnsafeURL{url: safe, reason: "destination has no scheme"}
	default:
		return &errUnsafeURL{url: safe, reason: "destination must use https, got " + u.Scheme}
	}
	if u.User != nil {
		return &errUnsafeURL{url: safe, reason: "destination must not embed credentials in the URL"}
	}
	if u.Host == "" {
		return &errUnsafeURL{url: safe, reason: "destination has no host"}
	}
	if u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" {
		return &errUnsafeURL{url: safe, reason: "destination must not carry a query or fragment"}
	}
	if err := validateURLPathEncoding(u); err != nil {
		return &errUnsafeURL{url: safe, reason: err.Error()}
	}
	return nil
}

// checkRedirect is the http.Client redirect policy: it refuses any
// downgrade (https to http), any credential-bearing destination, and any
// chain longer than maxRedirects — all BEFORE the refused hop is attempted,
// so no insecure request is ever sent.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("acquisition: redirect limit of %d hops exceeded for %s",
			maxRedirects, redactedURL(via[0].URL.String()))
	}
	if err := validateRequestURL(req.URL); err != nil {
		return err
	}
	return nil
}

// ctxRoundTripper attaches the acquisition context to every outgoing fetch so
// cancellation and deadlines propagate into every request, and enforces the
// wire policy on every request the client sends, including redirect hops.
type ctxRoundTripper struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t *ctxRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateRequestURL(req.URL); err != nil {
		return nil, err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req.WithContext(t.ctx))
}

// boundedFetcher is the go-tuf fetcher used for all metadata and target
// downloads. The library's DefaultFetcher wraps every fetch in backoff.Retry
// and never marks any error permanent, so whenever retries are configured
// (SetRetry/SetRetryOptions) it retries every error — including
// deterministic HTTP status responses such as the 404 the updater's own
// root-version walk relies on as its expected probe terminator, length
// mismatches, and (with a policy-checking client) redirect denials; only its
// default MaxTries(1) options keep it to a single attempt. This fetcher keeps
// the bounded retry budget for transport-level transient failures only and
// never retries deterministic HTTP status responses, size-bound refusals,
// wire-policy denials, malformed URLs, or a canceled or expired acquisition
// context.
type boundedFetcher struct {
	ctx    context.Context
	client *http.Client
}

// fetchBytesCeiling is the global finite ceiling applied to EVERY fetch —
// metadata and targets alike. The per-fetch length bound is signed data:
// metadata lengths for delegated roles come from the signed snapshot and
// target lengths from signed targets metadata, so a compromised signing
// role could authorize an arbitrarily large (or negative, or int64-
// overflowing) bound that this package never otherwise inspects. With this
// ceiling every fetch's read limit is a finite usable value, and
// maxLength+1 can never overflow int64 (which would turn the LimitReader
// bound negative and silently read nothing — or worse, disable the only
// defense). It equals the hard target cap so no legitimate fetch that
// Options already permits is refused.
const fetchBytesCeiling = hardMaxTargetBytes

// errFetchLengthBound is the deterministic, non-retryable refusal raised
// before any HTTP request when the length bound for a fetch is negative or
// above the global finite ceiling.
type errFetchLengthBound struct {
	maxLength int64
}

func (e *errFetchLengthBound) Error() string {
	return fmt.Sprintf("acquisition: fetch length bound %d is negative or above the finite ceiling of %d bytes",
		e.maxLength, fetchBytesCeiling)
}

// checkFetchLengthBound validates the per-fetch length bound before any
// HTTP request is issued or any body byte is read.
func checkFetchLengthBound(maxLength int64) error {
	if maxLength < 0 || maxLength > fetchBytesCeiling {
		return &errFetchLengthBound{maxLength: maxLength}
	}
	return nil
}

// DownloadFile fetches urlPath, refusing to return more than maxLength bytes.
func (f *boundedFetcher) DownloadFile(urlPath string, maxLength int64, _ time.Duration) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= fetchRetryAttempts; attempt++ {
		if attempt > 1 {
			// Pause between retries, but never sleep past a canceled
			// context or expired deadline.
			select {
			case <-f.ctx.Done():
				return nil, f.ctx.Err()
			case <-time.After(fetchRetryInterval):
			}
		}
		data, err := f.fetchOnce(urlPath, maxLength)
		if err == nil {
			return data, nil
		}
		if isNonRetryableFetchError(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchOnce performs a single bounded download attempt. The per-fetch
// length bound is re-validated here — before any HTTP request — because it
// is signed data; see fetchBytesCeiling.
func (f *boundedFetcher) fetchOnce(urlPath string, maxLength int64) ([]byte, error) {
	if err := f.ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkFetchLengthBound(maxLength); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, urlPath, nil)
	if err != nil {
		return nil, err
	}
	if err = validateRequestURL(req.URL); err != nil {
		return nil, err
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, &metadata.ErrDownloadHTTP{StatusCode: res.StatusCode, URL: redactedURL(urlPath)}
	}
	if header := res.Header.Get("Content-Length"); header != "" {
		length, err := strconv.ParseInt(header, 10, 0)
		if err != nil {
			return nil, err
		}
		if length > maxLength {
			return nil, &metadata.ErrDownloadLengthMismatch{Msg: fmt.Sprintf(
				"download failed for %s, length %d is larger than expected %d", redactedURL(urlPath), length, maxLength)}
		}
	}
	// maxLength was validated against the finite ceiling above, so
	// maxLength+1 cannot overflow and the read stays inside the hard bound.
	data, err := io.ReadAll(io.LimitReader(res.Body, maxLength+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxLength {
		return nil, &metadata.ErrDownloadLengthMismatch{Msg: fmt.Sprintf(
			"download failed for %s, length %d is larger than expected %d", redactedURL(urlPath), len(data), maxLength)}
	}
	return data, nil
}

// isNonRetryableFetchError reports whether err is a deterministic refusal
// (wire-policy denial, malformed URL, an HTTP status response, a refused
// size bound, or a canceled/expired context) that must never be retried.
func isNonRetryableFetchError(err error) bool {
	var unsafe *errUnsafeURL
	if errors.As(err, &unsafe) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// An HTTP status is the server's deterministic answer, not a transient
	// transport failure — the updater's own root-version walk relies on a
	// 404 as its expected "no newer version" probe terminator, and retrying
	// it (or any other status) would only burn the bounded retry budget.
	var httpStatus *metadata.ErrDownloadHTTP
	if errors.As(err, &httpStatus) {
		return true
	}
	// A refused size bound is signed-data nonsense, not a transient
	// condition; it cannot succeed on a later attempt.
	var lengthBound *errFetchLengthBound
	if errors.As(err, &lengthBound) {
		return true
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// A request that could not even be built is a misconfiguration,
		// not a transient failure.
		if _, perr := url.ParseRequestURI(uerr.URL); perr != nil {
			return true
		}
	}
	return false
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
	// The overall acquisition bound is rejected when negative (nonsense) or
	// above the accepted ceiling (a misconfiguration, not a deadline).
	if o.RequestTimeout < 0 || o.RequestTimeout > maxRequestTimeout {
		return fmt.Errorf("acquisition: RequestTimeout %s is outside the allowed range 0..%s",
			o.RequestTimeout, maxRequestTimeout)
	}
	// The size bound is a hard in-memory cap because go-tuf downloads
	// targets fully into memory; callers may lower it but never raise it
	// above hardMaxTargetBytes.
	if o.MaxTargetBytes < 0 || o.MaxTargetBytes > hardMaxTargetBytes {
		return fmt.Errorf("acquisition: MaxTargetBytes %d is outside the allowed range 0..%d",
			o.MaxTargetBytes, hardMaxTargetBytes)
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

// requireExistingDir validates a caller-provided cache or output root: it
// must be an absolute path to an existing directory that is itself not a
// symlink. Lstat is used deliberately so the root is inspected exactly as
// given — Stat would follow a symlink alias and silently accept a root
// whose every write lands outside the intended directory. Privacy of the
// root is the caller's provisioning prerequisite; this package neither
// checks nor promises any access control on it.
func requireExistingDir(dir, role string) error {
	if dir == "" {
		return fmt.Errorf("acquisition: %s directory is required", role)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("acquisition: %s directory %q must be an absolute path", role, dir)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("acquisition: %s directory %q must already exist: %w", role, dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("acquisition: %s directory %q is a symlink; aliased roots are refused", role, dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("acquisition: %s path %q is not a directory", role, dir)
	}
	return nil
}

// validateCacheLayout rejects a cache directory whose library-owned
// metadata or targets child already exists as anything other than a real
// directory: a symlink there would redirect the library's writes outside
// the caller's cache, and a regular file there cannot hold the library's
// state. It additionally rejects any existing entry DIRECTLY INSIDE those
// children that is not a regular file: the pinned go-tuf v2.4.2 reads
// cached metadata with os.ReadFile and writes cached targets with
// os.WriteFile, both of which follow a symlink leaf (or block on a FIFO),
// so an unsafe leaf would redirect or stall the library's own reads and
// writes from inside the cache. The leaf scan is bounded: a cache holding
// more than maxCacheSubtreeEntries entries per child cannot be validated
// within fixed work and is refused. It runs BEFORE EnsurePathsExist so the
// library never creates or writes through an unsafe child. Refusal never
// removes, resets, or modifies any existing entry: the cache is left
// exactly as the caller provided it.
func validateCacheLayout(cacheDir string) error {
	for _, sub := range []string{cacheMetadataSubdir, cacheTargetsSubdir} {
		child := filepath.Join(cacheDir, sub)
		fi, err := os.Lstat(child)
		if err != nil {
			if os.IsNotExist(err) {
				// Absent children are simply created later by the library.
				continue
			}
			return fmt.Errorf("acquisition: cache directory child %q is not usable: %w", child, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("acquisition: cache directory child %q is a symlink; symlinked cache children are refused", child)
		}
		if !fi.IsDir() {
			return fmt.Errorf("acquisition: cache directory child %q is not a directory", child)
		}
		if err := requireRegularCacheLeaves(child); err != nil {
			return err
		}
	}
	return nil
}

// maxCacheSubtreeEntries bounds the validation traversal of a library-owned
// cache subdirectory. The pinned go-tuf v2.4.2 lays down only a handful of
// flat files in these directories; a cache holding more entries than this
// cannot be validated within a fixed amount of work, so it is refused.
const maxCacheSubtreeEntries = 1024

// requireRegularCacheLeaves refuses any existing entry directly inside a
// library-owned cache subdirectory that is not a regular file. The pinned
// library creates only flat regular files here (metadata role files via
// CreateTemp+Rename, which does not follow a leaf symlink; cached targets
// via os.WriteFile of the url.PathEscaped target name, which does), and it
// reads cached metadata with os.ReadFile — so a directory, symlink, FIFO,
// device, or socket leaf is not library state and would redirect or block
// the library's reads and writes. Existing regular files are left in place
// for the library to overwrite; refusal never mutates the cache.
func requireRegularCacheLeaves(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("acquisition: cache directory %q cannot be opened for validation: %w", dir, err)
	}
	defer d.Close()
	examined := 0
	for {
		infos, rerr := d.Readdir(64)
		for _, fi := range infos {
			examined++
			if examined > maxCacheSubtreeEntries {
				return fmt.Errorf("acquisition: cache directory %q holds more than %d entries; such a cache cannot be validated safely", dir, maxCacheSubtreeEntries)
			}
			if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
				return fmt.Errorf("acquisition: cache entry %q is not a regular file; unsafe existing library-owned cache content is refused before use", filepath.Join(dir, fi.Name()))
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("acquisition: cache directory %q cannot be validated: %w", dir, rerr)
		}
	}
}

// checkBaseURL validates a configured HTTPS source URL. It must be a
// plain origin-based URL: a host, no userinfo, no query, no fragment, and a
// path free of ambiguous encodings (backslashes, invalid or
// separator-encoding percent escapes, control bytes, traversal segments).
// Error messages never echo the raw URL: a configured URL may embed
// credentials, and errors must not leak them.
func checkBaseURL(raw, role string, requireHTTPS bool) error {
	if raw == "" {
		return fmt.Errorf("acquisition: %s base URL is required", role)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("acquisition: %s base URL is not a parseable URL", role)
	}
	safe := scrubURL(*u)
	if requireHTTPS && u.Scheme != "https" {
		return fmt.Errorf("acquisition: %s base URL %q must use https", role, safe)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("acquisition: %s base URL %q must be a plain origin-based path URL", role, safe)
	}
	if err := validateURLPathEncoding(u); err != nil {
		return fmt.Errorf("acquisition: %s base URL %q: %s", role, safe, err.Error())
	}
	return nil
}

// windowsReservedNames are the DOS device names that Windows resolves to
// devices case-insensitively, with or without an extension, in any path
// segment position (including directory segments). A target path using
// one of them would name different things on different platforms, so
// they are refused here.
var windowsReservedNames = func() map[string]bool {
	m := map[string]bool{
		"con": true, "prn": true, "aux": true, "nul": true,
		"conin$": true, "conout$": true,
	}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("com%d", i)] = true
		m[fmt.Sprintf("lpt%d", i)] = true
	}
	return m
}()

// isHex reports whether b is an ASCII hex digit.
func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// validateTargetPath accepts only a relative, slash-separated, already
// normalized path that carries no platform or on-the-wire ambiguity, so a
// path authorized by signed metadata can never be reinterpreted as a
// different path by any platform or by URL decoding. The path is untrusted
// input and is never normalized here — anything not already clean is
// refused. It rejects: absolute paths; traversal or empty segments (any
// path that is not exactly its own Clean); backslashes; percent escapes
// (any '%' followed by two hex digits — %2f, %5c, %2e%2e and friends
// become different paths once the library joins them into a URL); control
// bytes (NUL through 0x1F and DEL); colons (drive-letter and Windows
// alternate-data-stream ambiguity); Windows reserved device names; and
// segments ending in a dot or space (Windows strips those). Non-ASCII
// Unicode filenames are valid and accepted as-is.
func validateTargetPath(p string) error {
	if p == "" {
		return errors.New("acquisition: target path is required")
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("acquisition: target path %q must not use backslashes", p)
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '%' && i+2 < len(p) && isHex(p[i+1]) && isHex(p[i+2]) {
			return fmt.Errorf("acquisition: target path %q must not contain percent escapes; they are ambiguous once joined into a URL", p)
		}
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("acquisition: target path %q contains a control byte", p)
		}
		if r == ':' {
			return fmt.Errorf("acquisition: target path %q contains a colon (drive-letter or alternate-data-stream ambiguity)", p)
		}
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
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return fmt.Errorf("acquisition: target path %q has a segment ending in a dot or space, which Windows strips", p)
		}
		base := seg
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReservedNames[strings.ToLower(base)] {
			return fmt.Errorf("acquisition: target path %q uses the reserved device name %q", p, base)
		}
	}
	return nil
}
