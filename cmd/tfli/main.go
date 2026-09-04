// Command tfli inspects Terraform TF_LOG output.
//
// --diagnose reports a log's structure with its content masked, safe to
// share back to this project. --profile reports real timing and resource
// addresses for the user's own eyes; its output is NOT masked and must never
// be treated as shareable the way a diagnose report is.
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
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/profile"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
	"github.com/yesdevnull/tf-log-inspector/internal/tui"
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
		doDiagnose = fs.Bool("diagnose", false, "report the log's structure and exit (output is masked, safe to share)")
		doProfile  = fs.Bool("profile", false, "rank resource types and calls by time (output is NOT masked)")
		outPath    = fs.String("o", "", "write the report to this file instead of standard output")
		showVer    = fs.Bool("version", false, "print the version and exit")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: tfli --diagnose|--profile [-o report.txt] <logfile>\n\n")
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
	switch {
	case *doDiagnose && *doProfile:
		return errors.New("pass only one of --diagnose or --profile, not both")
	case *doProfile:
		return runProfile(fs.Arg(0), *outPath, stdout)
	case *doDiagnose:
		return runDiagnose(fs.Arg(0), *outPath, stdout)
	default:
		return runTUI(fs.Arg(0))
	}
}

// writeReport sends render's output to outPath if set, otherwise to stdout.
// Both --diagnose and --profile funnel their report through this so neither
// mode can regress the -o handling on its own.
func writeReport(stdout io.Writer, outPath string, render func(io.Writer) error) error {
	w := stdout
	var out *os.File
	if outPath != "" {
		var err error
		out, err = os.Create(outPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", outPath, err)
		}
		w = out
	}

	renderErr := render(w)
	if out == nil {
		return renderErr
	}
	// Check Close's error too: an ENOSPC or similar surfacing only at close
	// would otherwise silently truncate the one artefact meant to leave the
	// machine. A Render error takes priority -- Close is still attempted, but
	// its error is only returned when Render itself succeeded.
	if closeErr := out.Close(); closeErr != nil && renderErr == nil {
		return fmt.Errorf("closing %s: %w", outPath, closeErr)
	}
	return renderErr
}

// runDiagnose scans path and writes the masked structural report.
func runDiagnose(path, outPath string, stdout io.Writer) error {
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

	report := diagnose.Build(stats, sniffer.Report(), builder.Spans(), uiBuilder.Spans(),
		uiBuilder.Malformed(), uiBuilder.BackwardsTimestamps(), uiBuilder.Saturated(),
		collector, &comps, elapsed)

	return writeReport(stdout, outPath, report.Render)
}

// runProfile loads path and writes the unmasked performance report.
func runProfile(path, outPath string, stdout io.Writer) error {
	l, err := model.Load(path)
	if err != nil {
		return err
	}
	return writeReport(stdout, outPath, func(w io.Writer) error {
		return profile.Render(w, l)
	})
}

// runTUIFunc is the seam a test substitutes to confirm the TUI path was
// actually reached, since tui.Run itself needs a real terminal and cannot
// run under go test.
var runTUIFunc = tui.Run

// runTUI loads path and opens the full-screen interface. It has no --diagnose
// or --profile equivalent flag: passing neither is what selects it.
func runTUI(path string) error {
	l, err := model.Load(path)
	if err != nil {
		return err
	}
	return runTUIFunc(l, path)
}
