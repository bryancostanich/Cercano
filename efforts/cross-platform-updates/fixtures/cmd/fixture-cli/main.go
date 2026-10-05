// fixture-cli is a stand-in for the real Windows cercano-cli.exe, built from
// this spike's own source. It only reports the version it was built with;
// command-line exes are short-lived, which is exactly the property the
// experiments rely on when reasoning about file locks.
package main

import (
	"fmt"
	"os"
)

// Set by -ldflags "-X main.version=..." when the fixture is built.
var version = "0.0.0-dev"

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: fixture-cli --version")
	os.Exit(2)
}
