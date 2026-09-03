// Command tfli inspects Terraform TF_LOG output.
//
// Phase 1 supports only --diagnose, which reports a log's structure so the
// format can be confirmed against real data before further features are built.
package main

import (
	"bufio"
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

	started := time.Now()
	stats, err := logfmt.Scan(bufio.NewReaderSize(f, 1<<20), &comps, collector, sniffer, &builder)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", path, err)
	}
	elapsed := time.Since(started)

	report := diagnose.Build(stats, sniffer.Report(), builder.Spans(), collector, &comps, elapsed)

	w := stdout
	if *outPath != "" {
		out, err := os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", *outPath, err)
		}
		defer out.Close()
		w = out
	}
	return report.Render(w)
}
