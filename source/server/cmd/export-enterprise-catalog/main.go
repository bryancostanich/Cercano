package main

import (
	"bytes"
	"cercano/source/server/internal/catalogexport"
	"flag"
	"fmt"
	"os"
)

func main() {
	check := flag.Bool("check", false, "fail if the checked-in snapshot differs")
	output := flag.String("output", "../enterpriseapi/catalog/catalog.json", "snapshot path")
	flag.Parse()
	raw, err := catalogexport.Build(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		old, err := os.ReadFile(*output)
		if err != nil || !bytes.Equal(raw, old) {
			fmt.Fprintln(os.Stderr, "enterprise catalog is stale; run go run ./cmd/export-enterprise-catalog from source/server")
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*output, raw, 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
