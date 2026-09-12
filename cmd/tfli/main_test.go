package main

import (
	"bytes"
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
		"limit":        {"--profile", "--limit=1\nFORGED" + control, "../../testdata/provider-rpc.log"},
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
	if !strings.Contains(out, "selected tier             resource") {
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
	const providerEntry = "2026-09-10T00:00:01.000Z [TRACE] provider.aws: configuring provider client\n"

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
		{
			name:  "non-RPC provider entry with address context",
			input: applyStart + "\n" + providerEntry,
			want: []string{
				"response entries          0",
				"provider entries          1",
				"provider entries were observed, but no RPC duration was admitted",
			},
			contradict: "provider RPC evidence was observed",
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

func TestLimitRejectedOutsideProfileBeforeOpeningInput(t *testing.T) {
	for _, args := range [][]string{
		{"--limit=0", "missing.log"},
		{"--diagnose", "--limit=20", "missing.log"},
		{"--scrub", "--limit=1", "-o", filepath.Join(t.TempDir(), "unused.log"), "missing.log"},
	} {
		var stdout, stderr strings.Builder
		err := run(args, &stdout, &stderr)
		if err == nil || err.Error() != "--limit applies only to --profile or --compare" {
			t.Fatalf("args %v: error %v", args, err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatal("unexpected output before mode validation")
		}
	}
}

func TestNegativeProfileLimitDoesNotOpenOrTruncateOutput(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "report.txt")
	const sentinel = "keep this report\n"
	if err := os.WriteFile(output, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	err := run([]string{"--profile", "--limit=-1", "-o", output, filepath.Join(dir, "missing.log")}, &stdout, &stderr)
	if err == nil || err.Error() != "--limit must be non-negative" {
		t.Fatalf("error = %v, want negative-limit rejection", err)
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != sentinel {
		t.Fatalf("output changed before limit validation: %q", got)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestLimitParseErrorsAreReportedOnce(t *testing.T) {
	for name, value := range map[string]string{
		"malformed": "many",
		"overflow":  "999999999999999999999999999999999999999999999999999999",
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := run([]string{"--profile", "--limit=" + value, "missing.log"}, &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "invalid value") || !strings.Contains(err.Error(), value) {
				t.Fatalf("error = %v, want invalid value naming %q", err, value)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", stdout.String())
			}
			if got := strings.Count(stderr.String(), "invalid value"); got != 1 {
				t.Fatalf("invalid-value diagnostic count = %d, want 1:\n%s", got, stderr.String())
			}
		})
	}
}

func TestProfileLimitControlsEveryListWithoutChangingTotals(t *testing.T) {
	const awsCall = "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5\n"
	const googleCall = "2022-12-15T00:16:21.400Z [TRACE] provider.google: Received downstream response: tf_resource_type=google_compute_instance tf_rpc=ApplyResourceChange tf_provider_addr=registry.terraform.io/hashicorp/google tf_req_duration_ms=8\n"
	path := filepath.Join(t.TempDir(), "profile.log")
	if err := os.WriteFile(path, []byte(strings.Repeat(awsCall, 10)+strings.Repeat(googleCall, 11)), 0o600); err != nil {
		t.Fatal(err)
	}
	section := func(t *testing.T, report, start, end string) string {
		t.Helper()
		startAt, endAt := strings.Index(report, start), strings.Index(report, end)
		if startAt < 0 || endAt < 0 || startAt >= endAt {
			t.Fatalf("cannot find report section %q to %q:\n%s", start, end, report)
		}
		return report[startAt:endAt]
	}

	cases := []struct {
		name                string
		args                []string
		wantHeadings        []string
		wantTypeRows        int
		wantProviderRows    int
		wantObservationRows int
	}{
		{name: "default", args: []string{"--profile", path}, wantHeadings: []string{"BY RESOURCE TYPE\n", "BY PROVIDER\n", "SLOWEST CALLS (top 20 of 21)\n"}, wantTypeRows: 2, wantProviderRows: 2, wantObservationRows: 20},
		{name: "one", args: []string{"--profile", "--limit=1", path}, wantHeadings: []string{"BY RESOURCE TYPE (top 1 of 2)\n", "BY PROVIDER (top 1 of 2)\n", "SLOWEST CALLS (top 1 of 21)\n"}, wantTypeRows: 1, wantProviderRows: 1, wantObservationRows: 1},
		{name: "all", args: []string{"--profile", "--limit=0", path}, wantHeadings: []string{"BY RESOURCE TYPE\n", "BY PROVIDER\n", "SLOWEST CALLS\n"}, wantTypeRows: 2, wantProviderRows: 2, wantObservationRows: 21},
	}
	reports := make(map[string]string, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if err := run(tc.args, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
			report := stdout.String()
			reports[tc.name] = report
			for _, want := range tc.wantHeadings {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("missing %q:\n%s", want, stdout.String())
				}
			}

			typeSection := section(t, report, "BY RESOURCE TYPE", "BY PROVIDER")
			providerSection := section(t, report, "BY PROVIDER", "SLOWEST CALLS")
			callsSection := section(t, report, "SLOWEST CALLS", "CONCURRENCY")
			if got := strings.Count(typeSection, "  google_compute_instance") + strings.Count(typeSection, "  aws_subnet"); got != tc.wantTypeRows {
				t.Errorf("resource-type row count = %d, want %d:\n%s", got, tc.wantTypeRows, typeSection)
			}
			if got := strings.Count(providerSection, "registry.terraform.io/hashicorp/"); got != tc.wantProviderRows {
				t.Errorf("provider row count = %d, want %d:\n%s", got, tc.wantProviderRows, providerSection)
			}
			if got := strings.Count(callsSection, "    source: line "); got != tc.wantObservationRows {
				t.Errorf("observation row count = %d, want %d:\n%s", got, tc.wantObservationRows, callsSection)
			}
		})
	}

	for _, report := range reports {
		for _, want := range []string{
			"RPC timing records     21: admitted 21, rejected 0; duration 138ms",
			"RPC positioning        21 observations, 138ms; excluded 0 observations, 0ms",
			"positioned observations 21 of 21 admitted",
			"positioned reported-duration sum 138ms",
		} {
			if !strings.Contains(report, want) {
				t.Errorf("limited report changed complete total %q:\n%s", want, report)
			}
		}
	}
}

func TestProfileLimitControlsIntervalsAndResourcesWithoutChangingMetrics(t *testing.T) {
	render := func(t *testing.T, path, limit string) string {
		t.Helper()
		var stdout, stderr strings.Builder
		if err := run([]string{"--profile", "--limit=" + limit, path}, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %q", stderr.String())
		}
		return stdout.String()
	}
	section := func(t *testing.T, report, start, end string) string {
		t.Helper()
		startAt, endAt := strings.Index(report, start), strings.Index(report, end)
		if startAt < 0 || endAt < 0 || startAt >= endAt {
			t.Fatalf("cannot find report section %q to %q:\n%s", start, end, report)
		}
		return report[startAt:endAt]
	}

	t.Run("qualifying intervals", func(t *testing.T) {
		path := filepath.Join("..", "..", "testdata", "timeline-many-stalls.log")
		limited, all := render(t, path, "1"), render(t, path, "0")
		if !strings.Contains(limited, "QUALIFYING INTERVALS (top 1 of 2)\n") || !strings.Contains(all, "QUALIFYING INTERVALS\n") {
			t.Fatalf("interval headings do not show limited and unlimited modes:\nLIMITED:\n%s\nALL:\n%s", limited, all)
		}
		for name, report := range map[string]string{"limited": limited, "all": all} {
			intervals := section(t, report, "QUALIFYING INTERVALS", "  Observed gaps")
			wantRows := 2
			if name == "limited" {
				wantRows = 1
			}
			if got := strings.Count(intervals, "ms-"); got != wantRows {
				t.Errorf("%s interval row count = %d, want %d:\n%s", name, got, wantRows, intervals)
			}
			for _, want := range []string{"peak concurrency 2", "busy union 17.0s", "busy / window 85.0%", "positioned reported-duration sum 17.2s"} {
				if !strings.Contains(report, want) {
					t.Errorf("%s report changed metric %q:\n%s", name, want, report)
				}
			}
		}
	})

	t.Run("slowest resources", func(t *testing.T) {
		path := filepath.Join("..", "..", "testdata", "structured-ui.log")
		limited, all := render(t, path, "1"), render(t, path, "0")
		if !strings.Contains(limited, "SLOWEST RESOURCES (top 1 of 2)\n") || !strings.Contains(all, "SLOWEST RESOURCES\n") {
			t.Fatalf("resource headings do not show limited and unlimited modes:\nLIMITED:\n%s\nALL:\n%s", limited, all)
		}
		for name, report := range map[string]string{"limited": limited, "all": all} {
			resources := section(t, report, "SLOWEST RESOURCES", "CONCURRENCY")
			wantRows := 2
			if name == "limited" {
				wantRows = 1
			}
			if got := strings.Count(resources, " (observed resource)\n"); got != wantRows {
				t.Errorf("%s resource row count = %d, want %d:\n%s", name, got, wantRows, resources)
			}
			for _, want := range []string{"resource timing records     2: admitted 2, rejected 0; duration 2.5s", "resource positioning        2 observations, 2.5s; excluded 0 observations, 0ms", "positioned reported-duration sum 2.5s"} {
				if !strings.Contains(report, want) {
					t.Errorf("%s report changed metric %q:\n%s", name, want, report)
				}
			}
		}
	})
}

func TestLimitPreservesHelpAndVersionPrecedence(t *testing.T) {
	var help strings.Builder
	if err := run([]string{"--help", "--limit=1"}, io.Discard, &help); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(help.String(), "maximum rows per text report list") || !strings.Contains(help.String(), "default 20") || !strings.Contains(help.String(), "0 means all") {
		t.Fatalf("help does not document the limit contract:\n%s", help.String())
	}

	var versionOut, versionErr strings.Builder
	if err := run([]string{"--version", "--limit=1"}, &versionOut, &versionErr); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(versionOut.String(), "tfli ") || versionErr.Len() != 0 {
		t.Fatalf("version output: stdout=%q stderr=%q", versionOut.String(), versionErr.String())
	}
}

func TestProfileFormatValidationPrecedesFileAccess(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--format=text"}, "--format applies only to --profile or --compare"},
		{[]string{"--diagnose", "--format=json"}, "--format applies only to --profile or --compare"},
		{[]string{"--scrub", "--format=text"}, "--format applies only to --profile or --compare"},
		{[]string{"--profile", "--format=yaml"}, "--format must be text or json"},
		{[]string{"--profile", "--format="}, "--format must be text or json"},
		{[]string{"--profile", "--format=JSON"}, "--format must be text or json"},
		{[]string{"--profile", "--format=json\x1b[31m"}, "--format must be text or json"},
		{[]string{"--profile", "--format=json", "--limit=0"}, "--limit is not supported with --format json"},
		{[]string{"--profile", "--format=json", "--limit=20"}, "--limit is not supported with --format json"},
		{[]string{"--profile", "--format=json", "--limit=1", "--limit=20"}, "--limit is not supported with --format json"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		output := filepath.Join(dir, "report.json")
		if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := append(append([]string{}, tc.args...), "-o", output, filepath.Join(dir, "missing.log"))
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("%v: error = %v, want %q", args, err, tc.want)
		}
		data, readErr := os.ReadFile(output)
		if readErr != nil || string(data) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("validation touched output: data=%q readErr=%v stdout=%q stderr=%q", data, readErr, stdout.Bytes(), stderr.Bytes())
		}
	}
}

func TestProfileFormatRepeatedFlagsUseLastValueAndRetainPresence(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--profile", "--format=json", "--format=text", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(stdout.Bytes(), []byte("tfli profile report\n")) || stderr.Len() != 0 {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout.Bytes(), stderr.Bytes())
	}

	err := run([]string{"--format=json", "--format=text", "missing.log"}, io.Discard, io.Discard)
	if err == nil || err.Error() != "--format applies only to --profile or --compare" {
		t.Fatalf("explicit repeated format outside profile: %v", err)
	}
}

func TestProfileFormatPreservesHelpAndVersionPrecedence(t *testing.T) {
	var help bytes.Buffer
	if err := run([]string{"--help", "--format=json"}, io.Discard, &help); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !bytes.Contains(help.Bytes(), []byte("profile or comparison output format: text or json")) {
		t.Fatalf("help does not document format:\n%s", help.Bytes())
	}
	if !bytes.Contains(help.Bytes(), []byte("tfli --profile [--format text] [--limit N]")) {
		t.Fatalf("usage does not show profile format:\n%s", help.Bytes())
	}

	var versionOut, versionErr bytes.Buffer
	if err := run([]string{"--version", "--format=json"}, &versionOut, &versionErr); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got, want := versionOut.String(), "tfli "+version+"\n"; got != want || versionErr.Len() != 0 {
		t.Fatalf("version output: stdout=%q, want %q; stderr=%q", got, want, versionErr.Bytes())
	}
}

func TestNegativeAndInvalidLimitsPrecedeProfileFormatValidation(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		err := run([]string{"--profile", "--format=json", "--limit=-1", "missing.log"}, io.Discard, io.Discard)
		if err == nil || err.Error() != "--limit must be non-negative" {
			t.Fatalf("error = %v, want negative-limit rejection", err)
		}
	})
	for name, value := range map[string]string{
		"malformed": "many",
		"overflow":  "999999999999999999999999999999999999999999999999999999",
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := run([]string{"--profile", "--format=json", "--limit=" + value, "missing.log"}, io.Discard, &stderr)
			if err == nil || !strings.Contains(err.Error(), "invalid value") {
				t.Fatalf("error = %v, want parser rejection", err)
			}
			if got := strings.Count(stderr.String(), "invalid value"); got != 1 {
				t.Fatalf("invalid-value diagnostic count = %d, want 1:\n%s", got, stderr.String())
			}
		})
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

func TestReportsPreserveTheirInputFileIncludingJSON(t *testing.T) {
	for _, mode := range []struct {
		name string
		args []string
	}{
		{name: "diagnose", args: []string{"--diagnose"}},
		{name: "profile text", args: []string{"--profile"}},
		{name: "profile JSON", args: []string{"--profile", "--format=json"}},
	} {
		for _, alias := range []string{"same path", "hard link", "symbolic link"} {
			t.Run(mode.name+"/"+alias, func(t *testing.T) {
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
				before, readErr := os.ReadFile(input)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var stdout, stderr strings.Builder
				args := append(append([]string{}, mode.args...), "-o", output, input)
				err := run(args, &stdout, &stderr)
				if err == nil || !strings.Contains(err.Error(), "same file") {
					t.Errorf("run error = %v, want same-file rejection", err)
				}
				got, readErr := os.ReadFile(input)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(before, original) || !bytes.Equal(got, before) {
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

func TestHelpQualifiesDiagnoseOutputBeforeSharing(t *testing.T) {
	var stderr strings.Builder
	if err := run([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "output is masked; review before sharing") {
		t.Fatalf("diagnose help lacks review qualification:\n%s", out)
	}
	for _, forbidden := range []string{"safe to share", "shareable"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Fatalf("help implies diagnose is %q:\n%s", forbidden, out)
		}
	}
}
