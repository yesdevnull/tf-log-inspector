// Command tfli inspects Terraform TF_LOG output.
//
// Phase 1 supports only --diagnose, which reports a log's structure so the
// format can be confirmed against real data before further features are built.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/diagnose"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// version is overridden at build time; the zero value is fine for go install.
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "tfli:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tfli", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		doDiagnose = fs.Bool("diagnose", false, "report the log's structure and exit")
		outPath    = fs.String("o", "", "write the report to this file instead of standard output")
		showVer    = fs.Bool("version", false, "print the version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: tfli --diagnose [-o report.txt] <logfile>\n\n")
		fmt.Fprintf(stderr, "Analyse a Terraform TF_LOG file. For an HCP Terraform workspace,\n")
		fmt.Fprintf(stderr, "enable debug logging on a run and download its raw log.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVer {
		fmt.Fprintln(stdout, "tfli", version)
		return nil
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one log file argument")
	}
	if !*doDiagnose {
		return errors.New("phase 1 supports only --diagnose; pass --diagnose <logfile>")
	}

	path := fs.Arg(0)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var comps logfmt.Interner
	collector := diagnose.NewCollector(&comps)
	sniffer := span.NewSniffer(&comps)
	var builder span.ReportedBuilder
	// UIHookBuilder costs nothing extra on an hclog log: it implements
	// logfmt.StructuredSink, so Scan only ever calls it for structured-output
	// lines, of which an hclog log has none.
	var uiBuilder span.UIHookBuilder

	started := time.Now()
	// Scan wraps r in its own 256KB bufio.Reader (internal/logfmt/scan.go), so
	// wrapping f again here would only add a second, redundant buffer.
	stats, err := logfmt.Scan(f, &comps, collector, sniffer, &builder, &uiBuilder)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", path, err)
	}
	elapsed := time.Since(started)

	report := diagnose.Build(stats, sniffer.Report(), builder.Spans(), uiBuilder.Spans(), collector, &comps, elapsed)

	w := stdout
	var out *os.File
	if *outPath != "" {
		out, err = os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", *outPath, err)
		}
		w = out
	}

	renderErr := report.Render(w)
	if out == nil {
		return renderErr
	}
	// Check Close's error too: an ENOSPC or similar surfacing only at close
	// would otherwise silently truncate the one artefact meant to leave the
	// machine. A Render error takes priority -- Close is still attempted, but
	// its error is only returned when Render itself succeeded.
	if closeErr := out.Close(); closeErr != nil && renderErr == nil {
		return fmt.Errorf("closing %s: %w", *outPath, closeErr)
	}
	return renderErr
}
