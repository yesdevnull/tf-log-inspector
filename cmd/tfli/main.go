// Command tfli inspects Terraform TF_LOG output.
//
// --diagnose reports a log's structure with its content masked, safe to
// share back to this project. --profile reports real timing and resource
// addresses for the user's own eyes; its output is NOT masked and must never
// be treated as shareable the way a diagnose report is.
// --scrub writes a separate log with consistent fake identifying values;
// review that candidate before sharing because detection is heuristic.
//
// A bare invocation opens the full-screen interface, which discloses more
// again: real addresses as --profile does, plus the log's own lines rendered
// verbatim in its raw log view. It is the easiest invocation and the least
// shareable one.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/attrib"
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
		fmt.Fprintln(os.Stderr, "tfli:", logfmt.DisplayText(err.Error()))
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tfli", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		doDiagnose = fs.Bool("diagnose", false, "report the log's structure and exit (output is masked, safe to share)")
		doProfile  = fs.Bool("profile", false, "rank resource types and calls by time (output is NOT masked)")
		doScrub    = fs.Bool("scrub", false, "write a log with consistent fake identifying values")
		valuesPath = fs.String("scrub-values", "", "additional literal identifying values, one per line")
		outPath    = fs.String("o", "", "write the selected report or scrubbed log to this file")
		showVer    = fs.Bool("version", false, "print the version and exit")
	)
	usage := func() {
		// Usage is best-effort, matching flag.PrintDefaults' error handling.
		_, _ = fmt.Fprintf(stderr, "Usage: tfli <logfile>                                  open the interface\n")
		_, _ = fmt.Fprintf(stderr, "       tfli --diagnose|--profile [-o report.txt] <logfile>\n")
		_, _ = fmt.Fprintf(stderr, "       tfli --scrub [--scrub-values values.txt] -o sanitised.log <logfile>\n\n")
		_, _ = fmt.Fprintf(stderr, "Analyse a Terraform TF_LOG file. For an HCP Terraform workspace,\n")
		_, _ = fmt.Fprintf(stderr, "enable debug logging on a run and download its raw log.\n\n")
		_, _ = fmt.Fprintf(stderr, "With no mode flag tfli opens the full-screen interface, which shows\n")
		_, _ = fmt.Fprintf(stderr, "real resource addresses and the log's own lines: see the README's\n")
		_, _ = fmt.Fprintf(stderr, "\"What each mode discloses\" before sharing a session.\n\n")
		fs.PrintDefaults()
	}
	// Flag errors embed raw arguments. Print their escaped text separately
	// from trusted usage formatting so an argument cannot inject commands
	// or diagnostic rows. Flag parsing itself must remain silent.
	fs.Usage = func() {}
	parseErr := fs.Parse(args)
	fs.SetOutput(stderr)
	fs.Usage = usage
	if err := parseErr; err != nil {
		// Asking for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
			return nil
		}
		_, _ = fmt.Fprintln(stderr, logfmt.DisplayText(err.Error())) // Preserve the flag error if diagnostics fail.
		fs.Usage()
		return err
	}

	if *showVer {
		_, err := fmt.Fprintln(stdout, "tfli", version)
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one log file argument")
	}
	valuesSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "scrub-values" {
			valuesSet = true
		}
	})
	modeCount := 0
	for _, selected := range []bool{*doDiagnose, *doProfile, *doScrub} {
		if selected {
			modeCount++
		}
	}
	if modeCount > 1 {
		return errors.New("pass only one of --diagnose, --profile, or --scrub")
	}
	if valuesSet && !*doScrub {
		return errors.New("--scrub-values applies only to --scrub")
	}
	switch {
	case *doScrub:
		if *outPath == "" {
			return errors.New("--scrub requires -o")
		}
		return runScrub(fs.Arg(0), *outPath, *valuesPath, stderr)
	case *doProfile:
		return runProfile(fs.Arg(0), *outPath, stdout)
	case *doDiagnose:
		return runDiagnose(fs.Arg(0), *outPath, stdout)
	default:
		// The interface writes no report, so -o names a file that would never
		// be created. Accepting the flag and opening the interface anyway
		// looks exactly like a report written somewhere the user was not
		// watching, which is how people lose work.
		if *outPath != "" {
			return errors.New("-o applies only to --diagnose, --profile, or --scrub")
		}
		return runTUI(fs.Arg(0))
	}
}

// writeReport sends render's output to outPath if set, otherwise to stdout.
// Both --diagnose and --profile funnel their report through this so neither
// mode can regress the -o handling on its own.
func writeReport(stdout io.Writer, inputPath, outPath string, render func(io.Writer) error) error {
	w := stdout
	var out *os.File
	if outPath != "" {
		inputInfo, err := os.Stat(inputPath)
		if err != nil {
			return fmt.Errorf("checking %s: %w", inputPath, err)
		}
		// Open without truncation so the identity check applies to the
		// descriptor we actually write, including symbolic and hard links.
		out, err = os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE, 0o666)
		if err != nil {
			return fmt.Errorf("creating %s: %w", outPath, err)
		}
		outputInfo, err := out.Stat()
		if err != nil {
			_ = out.Close() // Preserve the Stat error; no output has been written.
			return fmt.Errorf("checking %s: %w", outPath, err)
		}
		if os.SameFile(inputInfo, outputInfo) {
			_ = out.Close() // No output has been written to the input file.
			return fmt.Errorf("input %s and output %s are the same file", inputPath, outPath)
		}
		if outputInfo.Mode().IsRegular() {
			if err := out.Truncate(0); err != nil {
				_ = out.Close() // Preserve the Truncate error.
				return fmt.Errorf("truncating %s: %w", outPath, err)
			}
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
	defer func() { _ = f.Close() }() // Closing a read-only input cannot lose report data.

	var comps, reqIDs logfmt.Interner
	collector := diagnose.NewCollector(&comps)
	sniffer := span.NewSniffer(&comps)
	var builder span.ReportedBuilder
	// UIHookBuilder costs nothing extra on an hclog log: it implements
	// logfmt.StructuredSink, so Scan only ever calls it for structured-output
	// lines, of which an hclog log has none.
	var uiBuilder span.UIHookBuilder
	// ContextCollector is the other logfmt.StructuredSink: it collects the
	// address-attribution windows Build correlates against the RPC spans, so
	// --diagnose can measure coverage without ever naming an address.
	var cc attrib.ContextCollector

	started := time.Now()
	// Scan wraps r in its own 256KB bufio.Reader (internal/logfmt/scan.go), so
	// wrapping f again here would only add a second, redundant buffer.
	stats, err := logfmt.Scan(f, &comps, &reqIDs, collector, sniffer, &builder, &uiBuilder, &cc)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", path, err)
	}
	elapsed := time.Since(started)
	rpcSpans := builder.Spans()
	uiSpans := uiBuilder.Spans()
	contexts := cc.Contexts()
	var attributions []attrib.Attribution
	if len(contexts) > 0 {
		attributions = attrib.Correlate(rpcSpans, stats.FirstTS, contexts)
	}
	var uiOrigin time.Time
	if origin, ok := uiBuilder.Origin(); ok {
		uiOrigin = origin
	}
	quality := model.BuildCaptureQuality(model.CaptureQualityInput{
		Stats: stats, Caps: sniffer.Report(), RPCSpans: rpcSpans, UISpans: uiSpans,
		RPCEvidence: builder.Evidence(), UIEvidence: uiBuilder.Evidence(), UIOrigin: uiOrigin,
		Contexts: contexts, ContextEvidence: cc.Evidence(), Attributions: attributions,
		ComponentOverflow: comps.Overflowed(), RequestIDOverflow: reqIDs.Overflowed(),
	})

	report := diagnose.Build(stats, sniffer.Report(), rpcSpans, uiSpans, quality,
		uiBuilder.Malformed(), uiBuilder.BackwardsTimestamps(), uiBuilder.Saturated(), &cc,
		collector, &comps, elapsed)

	return writeReport(stdout, path, outPath, report.Render)
}

// runProfile loads path and writes the unmasked performance report.
func runProfile(path, outPath string, stdout io.Writer) error {
	l, err := model.Load(path)
	if err != nil {
		return err
	}
	return writeReport(stdout, path, outPath, func(w io.Writer) error {
		return profile.Render(w, l, profile.TextOptions{Limit: profile.DefaultLimit})
	})
}

// runTUIFunc is the seam a test substitutes to confirm the TUI path was
// actually reached, since tui.Run itself needs a real terminal and cannot
// run under go test.
var runTUIFunc = tui.Run

// runTUI loads path and opens the full-screen interface. It has no --diagnose
// or --profile equivalent flag: passing no mode flag is what selects it.
func runTUI(path string) error {
	l, err := model.Load(path)
	if err != nil {
		return err
	}
	return runTUIFunc(l, path)
}
