# OpenAI Default Model

- [x] Replace the built-in OpenAI gpt-5.5 default with gpt-6-astra in model tables, ChatGPT sign-in, and setup recommendations.
- [x] Verify old unpinned defaults follow the catalog and explicit pins remain intact.
- [x] Run focused tests and build the server and CLI.

Validation: config regression tests failed before the change and passed after it.
`go test ./pkg/config ./internal/server -count=1` and CLI
`go test ./internal/wizard -count=1` passed. Both binaries built successfully.
No live provider request was made.
