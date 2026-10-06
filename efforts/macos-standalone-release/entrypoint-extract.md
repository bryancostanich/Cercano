# CLI Version Handling Analysis

## source/clients/cli/main.go lines 24-55

```go
// version is set at build time via -ldflags "-X main.version=...".
// Falls back to "dev" for local builds.
var version = "dev"

func init() {
	// Normalize: strip leading "v" so the print format "v%s" doesn't double up.
	version = strings.TrimPrefix(version, "v")
}

func main() {
	resumeShort := flag.Bool("r", false, "Open the conversation history picker on launch (alias for --resume)")
	resumeLong := flag.Bool("resume", false, "Open the conversation history picker on launch")
	setupShort := flag.Bool("s", false, "Open the setup wizard on launch (alias for --setup)")
	setupLong := flag.Bool("setup", false, "Open the setup wizard on launch")
	mdtest := flag.Bool("mdtest", false, "Launch the TUI with a markdown doc pre-loaded for render testing (optional file path as a positional arg; built-in sample if omitted)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cercano-cli v%s\n", version)
		if info := update.CheckForUpdate(version); info != nil {
			if info.UpdateAvailable {
				fmt.Printf("\nA newer version is available: v%s\n", info.LatestVersion)
				fmt.Printf("  Upgrade: %s\n", info.UpgradeCommand())
				fmt.Printf("  Release: %s\n", info.ReleaseURL)
			} else {
				fmt.Println("(up to date)")
			}
		}
		return
	}
```

## source/server/cmd/cercano/main.go matches

Line 1035:
```go
		if info := update.CheckForUpdate(version); info != nil {
```

Line 1059:
```go
		// Used by `cercano-cli` auto-launch and by IDE extensions that want
```

Line 1125:
```go
	showVersion := flag.Bool("version", false, "Print version and exit")
```

Line 1130:
```go
	if *showVersion {
```

Line 1132:
```go
		if info := update.CheckForUpdate(version); info != nil {
```

Line 1157:
```go
	// separate `cercano-cli` binary (source/clients/cli), which dials this
```

Line 1515:
```go
	path, err := exec.LookPath("llama-server")
```

Line 1554:
```go
	systemPython, err := exec.LookPath("python3")
```

Line 1594:
```go
	exePath, _ := os.Executable()
```

## Conclusion

**Yes, CLI version handling calls update.CheckForUpdate.**

Both the CLI (`source/clients/cli/main.go`) and server (`source/server/cmd/cercano/main.go`) contain calls to `update.CheckForUpdate(version)` when the `--version` flag is used. The CLI shows version information and update availability, while the server also has similar version checking logic.