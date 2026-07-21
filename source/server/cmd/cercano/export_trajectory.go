package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/trajectory"
)

const exportTrajectoryUsage = `Usage: cercano export trajectory --conversation <id> --out <path> [options]

Export a complete conversation as a Cercano trajectory bundle. If --out ends in
.zip, the bundle is packaged as a zip archive; otherwise a directory bundle is
written.

Options:
  --conversation <id>     Conversation id to export (required)
  --out <path>            Output directory or .zip path (required)
  --zip                   Force zip output even when --out does not end in .zip
  --redact default|none   Redaction mode (default: default)
  --include-logs          Reserved for future log artifact export
  --overwrite             Replace an existing output path
`

func runExport(args []string) {
	if len(args) == 0 || args[0] != "trajectory" {
		fmt.Fprint(os.Stderr, "usage: cercano export trajectory --conversation <id> --out <path>\n")
		os.Exit(2)
	}
	runExportTrajectory(args[1:])
}

func runExportTrajectory(args []string) {
	fs := flag.NewFlagSet("export trajectory", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	convID := fs.String("conversation", "", "conversation id to export")
	out := fs.String("out", "", "output directory or .zip path")
	zipOut := fs.Bool("zip", false, "force zip output")
	redact := fs.String("redact", "default", "redaction mode: default or none")
	includeLogs := fs.Bool("include-logs", false, "include logs (reserved)")
	overwrite := fs.Bool("overwrite", false, "replace existing output")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *help {
		fmt.Print(exportTrajectoryUsage)
		return
	}
	if strings.TrimSpace(*convID) == "" || strings.TrimSpace(*out) == "" {
		fmt.Fprint(os.Stderr, exportTrajectoryUsage)
		os.Exit(2)
	}
	mode := trajectory.RedactionMode(*redact)
	if mode != trajectory.RedactDefault && mode != trajectory.RedactNone {
		fmt.Fprintf(os.Stderr, "invalid --redact %q (want default or none)\n", *redact)
		os.Exit(2)
	}
	format := trajectory.FormatInfer
	if *zipOut {
		format = trajectory.FormatZip
	}
	dbPath, err := conversation.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "conversation db:", err)
		os.Exit(1)
	}
	store, err := conversation.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open conversation db:", err)
		os.Exit(1)
	}
	defer store.Close()
	exp := trajectory.Exporter{Store: store}
	res, err := exp.Export(context.Background(), trajectory.Options{
		ConversationID: *convID,
		OutPath:        *out,
		Format:         format,
		Redaction:      mode,
		IncludeLogs:    *includeLogs,
		Overwrite:      *overwrite,
		Version:        version,
		Now:            time.Now(),
	}, func(phase, message string) {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", phase, message)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "export trajectory:", err)
		os.Exit(1)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	fmt.Println(res.OutputPath)
}
