# Private entry and staged helper image — native verification

Native matrix: https://github.com/cercano-ai/Cercano/actions/runs/37730485142
Commit a4ce317e0c8c. Windows/Linux/macOS native fixtures pass, including private
main subprocess isolation, bootstrap staging, existing lifecycle/launch fixtures,
CLI protocol compilation and TUF/site fixtures; Unix race checks pass.

The agent recognizes its private updater mode before normal command/config/model
or agent startup. Inputs are installation ID and canonical positive operation ID,
not arbitrary paths/commands/URLs. Capability reporting uses the existing offline
version-command path and reports execution_ready:false. Normal version behavior
is unchanged. Valid private execution currently fails explicitly as unavailable;
it never creates live state or reports a fictional completed update.

Subprocess fixtures isolate HOME, USERPROFILE, APPDATA, LOCALAPPDATA and XDG
config/data/cache/state locations, including case-insensitive environment-name
handling. Filesystem noncreation is measured; early control flow bypasses normal
initialization. No general network packet-isolation claim is made by these tests.

The bootstrap primitive stages an existing caller-trusted executable using a
supplied digest and length. This comparison is integrity, not independent
publisher authentication. It performs no execution or live image selection.
Placement resolves filesystem aliases outside existing forbidden directories.
Source identity/binding is checked before and after the copy; verification reads
are bounded. Failure cleanup removes only identity-matching created objects and
an empty staging directory. Unexpected files/replaced directories are retained
with an explicit cleanup-incomplete error; caller roots are never recursively
removed. Windows staged images have an executable suffix.

Regression probes reproduced lexical alias escapes, unnoticed source replacement,
unsafe recursive cleanup and unbounded verification growth before their fixes.
Windows fixtures were corrected to use native absolute paths and recognize
OS-prevented renames only with known errors plus positive unchanged-source and
verified-copy evidence. No production delete-sharing protection was relaxed.

Still required: authoritative installed-image resolution/signature policy,
Windows ACL enforcement, revalidation at the actual execution boundary, real
backend/activation/journal integration, Update UI and installed-system acceptance.
Temporary fixture data only; no live installation or running agent was changed.
