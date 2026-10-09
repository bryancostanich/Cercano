# Verified acquisition, archive preflight and staging — native acceptance

Latest native matrix:
https://github.com/cercano-ai/Cercano/actions/runs/37886657025
Commit 5ca8ad883dc0. Windows/Linux/macOS pass; prior staged extraction run
37884888552 also passed. Unix race gates pass. These are temporary fixture tests,
not installed-system update acceptance.

The acquisition adapter uses the approved go-tuf library for signature, version,
expiry and target verification. Output publication does not replace caller files.
Fetches are bounded; redirects cannot downgrade HTTPS or add credentials; unsafe
cache objects and ambiguous target paths refuse. A persistent-cache test rejects
a correctly signed older timestamp across separate acquisition calls.

Archive preflight supports tar.gz and zip. Parsing consumes the same bounded,
immutable compressed snapshot used for digest verification. Explicit framing,
checksum, compressed/decompressed/member/count bounds and full portable namespace
checks run before writes. Missing tar terminators cannot be disguised by zero
payload bytes. Required/optional files and implicit directories cannot collide.
No architecture/version validation of executable contents is implied here.

Extraction creates a new confined staging tree, not a live version selection.
Only preflight-approved members are copied, under trusted permissions policy,
with hashes and bounds rechecked. Error cleanup is nonrecursive and preserves
unknown/replaced objects. Native Linux exposed inode reuse: creation handles now
pin ownership identities through cleanup. Windows ordering and POSIX-only mode
assertions were corrected; mode bits are not Windows ACL evidence.

The new privdir primitive has native Windows fixture coverage for protected
creation/readback, permissive existing-directory refusal without rewriting its
permissions, real-token identity and bounded classification cases. It is not yet
wired into the production bootstrap/selection paths. Different-user UAC and
complete installation-context enforcement remain unproven; no stronger claim is
made for skipped host-capability fixtures.

Still pending: authoritative image/platform/version validation, permission-guard
integration, durable activation journal and atomic selection/recovery, actual
helper backend wiring, package backend completion, Update UI and installed-system
rehearsals. No current agent or installation was modified, no main merge or
production release/feed/signing operation occurred.
