package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func TestRunReportsOutputWriteFailures(t *testing.T) {
	for _, mode := range []string{"version", "scrub"} {
		t.Run(mode, func(t *testing.T) {
			closed, err := os.CreateTemp(t.TempDir(), "closed")
			if err != nil {
				t.Fatal(err)
			}
			if err := closed.Close(); err != nil {
				t.Fatal(err)
			}
			args := []string{"--version"}
			output := filepath.Join(t.TempDir(), "scrubbed.log")
			if mode == "scrub" {
				args = []string{"--scrub", "-o", output, "../../testdata/provider-rpc.log"}
			}
			if err := run(args, closed, closed); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("want closed output error, got %v", err)
			}
			if mode == "scrub" {
				data, err := os.ReadFile(output)
				if err != nil || len(data) == 0 {
					t.Fatalf("summary failure must preserve completed output: %v", err)
				}
			}
		})
	}
}

func TestCommandDiagnosticsCannotControlTheTerminal(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "tfli")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	control := "\x1b]52;c;U0VDUkVU\a"
	for name, args := range map[string][]string{
		"input path":   {"--diagnose", filepath.Join(t.TempDir(), control+".log")},
		"output path":  {"--diagnose", "-o", filepath.Join(t.TempDir(), control, "report.txt"), "../../testdata/provider-rpc.log"},
		"flag":         {"--unknown-" + control},
		"flag newline": {"--unknown-\nFORGED" + control},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command(binary, args...).CombinedOutput()
			if err == nil {
				t.Fatal("invalid invocation succeeded")
			}
			if strings.ContainsAny(string(out), "\x1b\a") {
				t.Errorf("diagnostic emits terminal controls: %q", out)
			}
			if strings.Contains(string(out), "\nFORGED") {
				t.Errorf("argument injected a diagnostic row: %q", out)
			}
			if !strings.Contains(string(out), `\x1b]52;c;U0VDUkVU\a`) {
				t.Errorf("diagnostic does not identify the escaped argument: %q", out)
			}
		})
	}
}

func TestRunDiagnoseOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"tfli diagnostic report", "selected tier             reported", "RPC timing records     2: admitted 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDiagnoseEscapesComponentAndMessageControls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controls.log")
	line := "2026-09-08T00:00:00.000Z [DEBUG] source\bname: message\btext\n"
	if err := os.WriteFile(path, []byte(line+line), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := run([]string{"--diagnose", path}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), '\b') {
		t.Errorf("diagnose emitted a terminal control: %q", out.String())
	}
	for _, want := range []string{`source\bname`, `message\btext`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report does not display escaped text %q", want)
		}
	}
}

// The mixed fixture's plan output must register as real non-hclog content,
// driven by the plan block rather than by the fixture's comment header.
func TestRunReportsNonHclogContent(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "mixed-hcp.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if strings.Contains(out, "untimestamped lines  0 ") || strings.Contains(out, "untimestamped lines  1 ") {
		t.Errorf("non-hclog content not measured:\n%s", out)
	}
	if !strings.Contains(out, "long output blocks   1") {
		t.Errorf("plan output block not detected:\n%s", out)
	}
}

// The structured-output fixture must report structured lines and select the
// ui-reported tier as usable -- span.Sniffer counts completion-bearing
// UI-hook lines, so the report has a usable tier rather than falling
// through to the no-tier-usable structured-output guidance -- and it must
// never disclose the fixture's resource addresses.
func TestRunReportsStructuredOutputLog(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "structured-ui.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "structured lines     8") {
		t.Errorf("report missing structured line count:\n%s", out)
	}
	if !strings.Contains(out, "selected tier             ui-reported") {
		t.Errorf("report did not select the ui-reported tier:\n%s", out)
	}
	for _, leak := range []string{`module.module_name["key"].data.local_file.thing`, "aws_instance.example"} {
		if strings.Contains(out, leak) {
			t.Fatalf("report leaked the fixture's resource address %q:\n%s", leak, out)
		}
	}
}

func TestRunDiagnoseDistinguishesObservedSourcesFromMissingSources(t *testing.T) {
	const applyStart = `{"@level":"info","@message":"aws_instance.example: Creating...","@module":"terraform.ui","@timestamp":"2026-09-10T00:00:00Z","hook":{"resource":{"addr":"aws_instance.example","resource":"aws_instance.example","resource_type":"aws_instance","resource_name":"example","resource_key":null,"implied_provider":"aws"},"action":"create"},"type":"apply_start"}`
	const unmatchedRefresh = `{"@level":"info","@message":"aws_instance.example: Refresh complete","@module":"terraform.ui","@timestamp":"2026-09-10T00:00:00Z","hook":{"resource":{"addr":"aws_instance.example","resource":"aws_instance.example","resource_type":"aws_instance","resource_name":"example","resource_key":null,"implied_provider":"aws"}},"type":"refresh_complete"}`
	const rejectedResponse = "2026-09-10T00:00:01.000Z [TRACE] provider.aws: Received downstream response: tf_rpc=ApplyResourceChange tf_req_duration_ms=broken\n"

	cases := []struct {
		name       string
		input      string
		want       []string
		contradict string
	}{
		{
			name:  "rejected RPC duration with address context",
			input: applyStart + "\n" + rejectedResponse,
			want: []string{
				"RPC timing records     1: admitted 0, rejected 1",
				"response entries          1",
				"provider entries          1",
				"provider RPC evidence was observed, but no duration was admitted",
			},
			contradict: "no TRACE-level provider RPC entries",
		},
		{
			name:  "structured terminator without address context",
			input: unmatchedRefresh + "\n",
			want: []string{
				"structured lines     1",
				"unmatched terminators     1",
				"structured output was observed, but it yielded no usable",
			},
			contradict: "the terraform.ui stream, which the debug-logging",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "capture.log")
			if err := os.WriteFile(path, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr strings.Builder
			if err := run([]string{"--diagnose", path}, &stdout, &stderr); err != nil {
				t.Fatalf("run: %v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("diagnose wrote stderr: %q", stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("report missing %q:\n%s", want, stdout.String())
				}
			}
			if strings.Contains(stdout.String(), tc.contradict) {
				t.Errorf("report contradicts its observed source counts with %q:\n%s", tc.contradict, stdout.String())
			}
		})
	}
}

func TestRunReportsMissingFileClearly(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", "no-such-file.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
}

// A bare invocation (no --diagnose or --profile) opens the TUI rather than
// erroring. runProfile and runTUI are structurally identical up to a
// missing-file error (both call model.Load and return its error verbatim),
// so a test that only checks the error text cannot tell the two apart --
// it would pass unchanged if the default case dispatched to runProfile
// instead. Substituting runTUIFunc, the seam runTUI calls in place of
// tui.Run, is what actually distinguishes them: tui.Run needs a terminal
// and cannot run under go test, so this stub stands in for it.
func TestRunWithNoModeReachesTheTUIPath(t *testing.T) {
	original := runTUIFunc
	t.Cleanup(func() { runTUIFunc = original })

	var gotPath string
	runTUIFunc = func(l *model.Log, path string) error {
		gotPath = path
		return nil
	}

	var sb strings.Builder
	logPath := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	if err := run([]string{logPath}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotPath != logPath {
		t.Errorf("runTUIFunc called with path %q, want %q", gotPath, logPath)
	}
}

func TestRunWithNoModeOpensTheTUIPath(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"no-such-file.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for a missing file")
	}
	if !strings.Contains(err.Error(), "no-such-file.log") {
		t.Errorf("error does not name the file: %v", err)
	}
	if strings.Contains(err.Error(), "--diagnose") {
		t.Errorf("bare invocation still routed to the old usage error: %v", err)
	}
}

func TestRunProfileOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--profile", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sb.String(), "BY RESOURCE TYPE") {
		t.Errorf("output missing BY RESOURCE TYPE:\n%s", sb.String())
	}
}

func TestProfileAndStreamingDiagnoseReportTheSameCaptureQualityTotals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quality.log")
	const captured = "customer_secret_value"
	input := "2026-09-04T09:15:03.000Z [TRACE] provider.customer: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=25 customer_field=" + captured + "\n" +
		"2026-09-04T09:15:03.100Z [TRACE] provider.customer: Received downstream response: tf_rpc=ReadResource customer_field=" + captured + "\n"
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	outputs := make(map[string]string)
	for _, mode := range []string{"--profile", "--diagnose"} {
		var stdout, stderr strings.Builder
		if err := run([]string{mode, path}, &stdout, &stderr); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s wrote stderr: %q", mode, stderr.String())
		}
		outputs[mode] = stdout.String()
	}
	for _, want := range []string{
		"RPC timing records     2: admitted 1, rejected 1; duration 25ms",
		"RPC positioning        1 observations, 25ms",
		"nameable duration      unavailable / 25ms (no address context)",
		"duration_missing",
	} {
		for _, mode := range []string{"--profile", "--diagnose"} {
			if !strings.Contains(outputs[mode], want) {
				t.Errorf("%s missing shared quality fact %q:\n%s", mode, want, outputs[mode])
			}
		}
	}
	if strings.Contains(outputs["--diagnose"], captured) {
		t.Fatalf("diagnose disclosed captured values:\n%s", outputs["--diagnose"])
	}
}

func TestRunRejectsDiagnoseAndProfileTogether(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", "--profile", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for --diagnose and --profile together")
	}
	for _, want := range []string{"--diagnose", "--profile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestRunWritesToOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.txt")
	var sb strings.Builder
	err := run([]string{"--diagnose", "-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(data), "tfli diagnostic report") {
		t.Error("report file does not contain the report")
	}
}

func TestReportsPreserveTheirInputFile(t *testing.T) {
	for _, mode := range []string{"--diagnose", "--profile"} {
		for _, alias := range []string{"same path", "hard link", "symbolic link"} {
			t.Run(mode+"/"+alias, func(t *testing.T) {
				dir := t.TempDir()
				input := filepath.Join(dir, "capture.log")
				original := []byte("2026-09-08T00:00:00.000Z [INFO] Terraform started\n")
				if err := os.WriteFile(input, original, 0o600); err != nil {
					t.Fatal(err)
				}
				output := input
				if alias != "same path" {
					output = filepath.Join(dir, "report.txt")
					link := os.Link
					if alias == "symbolic link" {
						link = os.Symlink
					}
					if err := link(input, output); err != nil {
						t.Fatal(err)
					}
				}
				var stdout, stderr strings.Builder
				err := run([]string{mode, "-o", output, input}, &stdout, &stderr)
				if err == nil || !strings.Contains(err.Error(), "same file") {
					t.Errorf("run error = %v, want same-file rejection", err)
				}
				got, readErr := os.ReadFile(input)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(got) != string(original) {
					t.Errorf("input file was overwritten: %q", got)
				}
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Errorf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			})
		}
	}
}

// The -o file is the one artefact meant to leave the machine, so a failure
// to create it must be reported rather than silently falling back to
// standard output -- which would look like success while writing the report
// somewhere the user was not watching.
func TestRunReportsUnwritableOutputPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "no-such-dir", "report.txt")
	var sb strings.Builder
	err := run([]string{"--diagnose", "-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for an uncreatable output file")
	}
	if !strings.Contains(err.Error(), out) {
		t.Errorf("error does not name the output path: %v", err)
	}
	if sb.Len() != 0 {
		t.Errorf("report was written to stdout after -o failed:\n%s", sb.String())
	}
}

func TestRunRejectsWrongArgumentCount(t *testing.T) {
	for name, args := range map[string][]string{
		"none": {"--diagnose"},
		"two":  {"--diagnose", "a.log", "b.log"},
	} {
		t.Run(name, func(t *testing.T) {
			var sb strings.Builder
			err := run(args, &sb, io.Discard)
			if err == nil {
				t.Fatal("run returned nil error for the wrong argument count")
			}
			if !strings.Contains(err.Error(), "exactly one log file") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunPrintsVersion(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--version"}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(sb.String(), "tfli ") {
		t.Errorf("version output = %q, want it to start with \"tfli \"", sb.String())
	}
}

func TestRunReportsUnknownFlag(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--no-such-flag", "a.log"}, &sb, io.Discard); err == nil {
		t.Fatal("run returned nil error for an unknown flag")
	}
}

// The bare invocation is the default mode, so the usage text must show it.
// Telling a user that a mode flag is mandatory is false, and it is false
// about the one invocation they are most likely to reach for.
func TestUsageShowsTheBareInvocation(t *testing.T) {
	var stderr strings.Builder
	if err := run(nil, io.Discard, &stderr); err == nil {
		t.Fatal("run returned nil error for no arguments")
	}
	out := stderr.String()
	if !strings.Contains(out, "Usage: tfli <logfile>") {
		t.Errorf("usage does not show the bare invocation:\n%s", out)
	}
	if !strings.Contains(out, "full-screen interface") {
		t.Errorf("usage does not say what the bare invocation does:\n%s", out)
	}
}

// -o names an output file, and the interface writes no report at all.
// Accepting the flag and opening the interface anyway looks exactly like a
// report written somewhere the user was not watching, so it is refused --
// and the interface must not open behind the refusal either.
func TestOWithoutAModeFlagIsRejected(t *testing.T) {
	original := runTUIFunc
	t.Cleanup(func() { runTUIFunc = original })
	opened := false
	runTUIFunc = func(l *model.Log, path string) error {
		opened = true
		return nil
	}

	out := filepath.Join(t.TempDir(), "report.txt")
	var sb strings.Builder
	err := run([]string{"-o", out, filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error for -o without a mode flag")
	}
	if !strings.Contains(err.Error(), "-o") {
		t.Errorf("error does not name the flag it refused: %v", err)
	}
	for _, mode := range []string{"--diagnose", "--profile", "--scrub"} {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("error does not name supported output mode %s: %v", mode, err)
		}
	}
	if opened {
		t.Error("the interface opened anyway, so -o was accepted and then ignored")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("a file was created at %s despite the error", out)
	}
}

// Asking for help is not a failure. flag.ErrHelp arrives from fs.Parse after
// the usage text has already been written, so returning it prints
// "tfli: flag: help requested" beneath the help the user asked for and exits
// non-zero.
func TestHelpIsNotAnError(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			var stderr strings.Builder
			if err := run([]string{arg}, io.Discard, &stderr); err != nil {
				t.Errorf("run(%q) = %v, want nil", arg, err)
			}
			if !strings.Contains(stderr.String(), "Usage: tfli") {
				t.Errorf("run(%q) printed no usage text:\n%s", arg, stderr.String())
			}
		})
	}
}
