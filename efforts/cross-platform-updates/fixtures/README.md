# Isolated update fixtures

Copied from the approved Windows updater spike, not production Cercano.
Build version and unhealthy variants through ldflags; parent tests must own,
terminate and reap every subprocess. They do not start models, open an agent
socket or access user configuration. Holding one's own executable is a file-lock
probe, not an authoritative test for all running Windows executables. Parent
death does not guarantee automatic fixture termination. Signal behavior varies
by platform; native integration tests must assert the actual lifecycle.

The staging/manifest implementation and diagnostic workflow were NOT copied.
