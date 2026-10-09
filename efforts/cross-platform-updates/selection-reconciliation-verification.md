# Read-only selection reconciliation — verification

Native matrix:
https://github.com/cercano-ai/Cercano/actions/runs/37961867593
Source commit f8936c6b48aa99969b2faabba8997db384697b42.
Windows, Linux and macOS passed. A separate clean `git archive HEAD` export passed
state/activation tests, race tests and vet before the native run.

The activation package parses bounded, strict selection descriptors and compares
validated observations with journal intent. Unknown observations are distinct
from positively observed absence. Invalid/future descriptors and a target selected
without recorded switch intent require manual recovery rather than automatic
health/cleanup recommendations. Selection metadata is not runtime health proof.
A restore-needed result is not authorization to guess the prior directory or
perform a filesystem mutation.

The earlier native run 37960653179 failed because validator exports existed in
the working tree but were omitted from the commit. The required exports were
committed without changing their validation rules; the committed-tree export
verification prevents those local edits from masking that failure again. An
unrelated schema formatting edit was preserved, not folded into this fix.

This slice is read-only. No selection writer, recovery executor, live installation
switch, agent stop or user-data cleanup was invoked. Atomic publication and
crash-boundary integration remain pending; the full updater is not complete.
