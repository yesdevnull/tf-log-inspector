package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type cliJSONProfile struct {
	SchemaVersion uint8  `json:"schema_version"`
	Kind          string `json:"kind"`
	ToolVersion   string `json:"tool_version"`
	Input         struct {
		Basename string `json:"basename"`
		Bytes    uint64 `json:"bytes"`
	} `json:"input"`
	Tiers struct {
		RPC cliJSONTier `json:"rpc"`
		UI  cliJSONTier `json:"ui"`
	} `json:"tiers"`
	Quality struct {
		ProviderEntries uint64 `json:"provider_entries"`
		StructuredLines uint64 `json:"structured_lines"`
		Reconstruction  struct {
			State       string  `json:"state"`
			Responses   *int    `json:"responses"`
			Diagnostics *int    `json:"diagnostics"`
			Code        *string `json:"code"`
		} `json:"reconstruction"`
	} `json:"quality"`
	RPCObservations []cliJSONObservation `json:"rpc_observations"`
	UIObservations  []cliJSONObservation `json:"ui_observations"`
	Timeline        struct {
		Tier   *string `json:"tier"`
		Status string  `json:"status"`
	} `json:"timeline"`
}

type cliJSONTier struct {
	DurationAvailable  bool   `json:"duration_available"`
	Records            uint64 `json:"records"`
	Admitted           uint64 `json:"admitted"`
	Rejected           uint64 `json:"rejected"`
	DurationMs         uint64 `json:"duration_ms"`
	DurationLowerBound bool   `json:"duration_lower_bound"`
}

type cliJSONObservation struct {
	Source *struct {
		StartLine uint64 `json:"start_line"`
		EndLine   uint64 `json:"end_line"`
	} `json:"source"`
	Address string `json:"address"`
}

func decodeCLIJSONProfile(t *testing.T, data []byte) cliJSONProfile {
	t.Helper()
	var document cliJSONProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second decode error = %v, want EOF", err)
	}
	return document
}

func TestRunProfileJSONProducesOneCompleteDocument(t *testing.T) {
	const call = "2026-09-11T00:00:00.000Z [TRACE] provider.aws: Received downstream response: tf_resource_type=aws_instance tf_rpc=ReadResource tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5\n"
	path := filepath.Join(t.TempDir(), "complete-profile.log")
	if err := os.WriteFile(path, []byte(strings.Repeat(call, 21)), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--profile", "--format=json", path}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.Bytes())
	}

	document := decodeCLIJSONProfile(t, stdout.Bytes())
	if document.SchemaVersion != 1 || document.Kind != "profile" || document.ToolVersion != version {
		t.Fatalf("identity = schema %d, kind %q, version %q", document.SchemaVersion, document.Kind, document.ToolVersion)
	}
	if document.Input.Basename != "complete-profile.log" || document.Input.Bytes != uint64(len(call)*21) {
		t.Fatalf("input = %+v", document.Input)
	}
	if document.Tiers.RPC.Records != 21 || document.Tiers.RPC.Admitted != 21 || document.Tiers.RPC.Rejected != 0 || document.Tiers.RPC.DurationMs != 105 {
		t.Fatalf("RPC totals = %+v", document.Tiers.RPC)
	}
	if document.Quality.ProviderEntries != 21 || document.Quality.StructuredLines != 0 {
		t.Fatalf("quality totals = %+v", document.Quality)
	}
	if reconstruction := document.Quality.Reconstruction; reconstruction.State != "not_checked" || reconstruction.Responses != nil || reconstruction.Diagnostics != nil || reconstruction.Code != nil {
		t.Fatalf("lazy reconstruction = %+v", reconstruction)
	}
	if len(document.RPCObservations) != 21 {
		t.Fatalf("JSON observation count = %d, want 21", len(document.RPCObservations))
	}
	first, last := document.RPCObservations[0].Source, document.RPCObservations[20].Source
	if first == nil || first.StartLine != 1 || first.EndLine != 1 || last == nil || last.StartLine != 21 || last.EndLine != 21 {
		t.Fatalf("source lines = first %+v, last %+v", first, last)
	}
}

func TestRunProfileJSONRealFixtureWorkflows(t *testing.T) {
	type expectation struct {
		fixture, timelineTier, timelineStatus                      string
		bytes, rpcRecords, rpcAdmitted, rpcRejected, rpcDurationMs uint64
		uiRecords, uiAdmitted, uiRejected, uiDurationMs            uint64
		providerEntries, structuredLines                           uint64
		rpcSourceLines, uiSourceLines                              []uint64
		uiLowerBound                                               bool
	}
	cases := []expectation{
		{fixture: "provider-rpc.log", timelineTier: "rpc", timelineStatus: "complete", bytes: 1152, rpcRecords: 2, rpcAdmitted: 2, rpcDurationMs: 6, providerEntries: 2, rpcSourceLines: []uint64{2, 3}},
		{fixture: "structured-ui.log", timelineTier: "ui", timelineStatus: "complete", bytes: 4259, uiRecords: 2, uiAdmitted: 2, uiDurationMs: 2500, structuredLines: 8, uiSourceLines: []uint64{4, 9}},
		{fixture: "two-tier.log", timelineTier: "rpc", timelineStatus: "complete", bytes: 4995, rpcRecords: 3, rpcAdmitted: 3, rpcDurationMs: 410, uiRecords: 3, uiAdmitted: 3, uiDurationMs: 6000, providerEntries: 3, structuredLines: 7, rpcSourceLines: []uint64{17, 21, 23}, uiSourceLines: []uint64{16, 20, 22}},
		{fixture: "core-only.log", timelineStatus: "unavailable", bytes: 588},
		{fixture: "resources-long-lower-bound.log", timelineTier: "ui", timelineStatus: "unavailable", bytes: 891, uiRecords: 1, uiAdmitted: 1, uiDurationMs: 4294967295, structuredLines: 2, uiSourceLines: []uint64{3}, uiLowerBound: true},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			input := filepath.Join("..", "..", "testdata", tc.fixture)
			var stdout, stderr bytes.Buffer
			if err := run([]string{"--profile", "--format=json", input}, &stdout, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.Bytes())
			}

			output := filepath.Join(t.TempDir(), "profile.json")
			var fileStdout, fileStderr bytes.Buffer
			if err := run([]string{"--profile", "--format=json", "-o", output, input}, &fileStdout, &fileStderr); err != nil {
				t.Fatal(err)
			}
			if fileStdout.Len() != 0 || fileStderr.Len() != 0 {
				t.Fatalf("unexpected diagnostic output: stdout=%q stderr=%q", fileStdout.Bytes(), fileStderr.Bytes())
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) {
				t.Fatal("output file is not JSON")
			}
			if !bytes.Equal(data, stdout.Bytes()) {
				t.Fatal("stdout and -o JSON bytes differ")
			}
			if bytes.Contains(data, []byte(filepath.Dir(output))) {
				t.Fatalf("JSON contains absolute temporary directory %q", filepath.Dir(output))
			}

			document := decodeCLIJSONProfile(t, data)
			if document.SchemaVersion != 1 || document.Kind != "profile" || document.ToolVersion != version {
				t.Fatalf("identity = schema %d, kind %q, version %q", document.SchemaVersion, document.Kind, document.ToolVersion)
			}
			if document.Input.Basename != tc.fixture || document.Input.Bytes != tc.bytes {
				t.Fatalf("input = %+v, want basename %q and %d bytes", document.Input, tc.fixture, tc.bytes)
			}
			assertCLIJSONTier(t, "RPC", document.Tiers.RPC, tc.rpcRecords, tc.rpcAdmitted, tc.rpcRejected, tc.rpcDurationMs, false)
			assertCLIJSONTier(t, "UI", document.Tiers.UI, tc.uiRecords, tc.uiAdmitted, tc.uiRejected, tc.uiDurationMs, tc.uiLowerBound)
			if document.Quality.ProviderEntries != tc.providerEntries || document.Quality.StructuredLines != tc.structuredLines {
				t.Fatalf("quality = %+v, want provider entries %d and structured lines %d", document.Quality, tc.providerEntries, tc.structuredLines)
			}
			assertCLIJSONSourceLines(t, "RPC", document.RPCObservations, tc.rpcSourceLines)
			assertCLIJSONSourceLines(t, "UI", document.UIObservations, tc.uiSourceLines)
			if document.Timeline.Status != tc.timelineStatus {
				t.Fatalf("timeline status = %q, want %q", document.Timeline.Status, tc.timelineStatus)
			}
			if tc.timelineTier == "" {
				if document.Timeline.Tier != nil {
					t.Fatalf("timeline tier = %q, want null", *document.Timeline.Tier)
				}
			} else if document.Timeline.Tier == nil || *document.Timeline.Tier != tc.timelineTier {
				t.Fatalf("timeline tier = %v, want %q", document.Timeline.Tier, tc.timelineTier)
			}
		})
	}
}

func assertCLIJSONTier(t *testing.T, name string, got cliJSONTier, records, admitted, rejected, durationMs uint64, lowerBound bool) {
	t.Helper()
	if got.Records != records || got.Admitted != admitted || got.Rejected != rejected || got.DurationMs != durationMs {
		t.Fatalf("%s tier = %+v, want records %d, admitted %d, rejected %d, duration %dms", name, got, records, admitted, rejected, durationMs)
	}
	if got.DurationAvailable != (admitted > 0) || got.DurationLowerBound != lowerBound {
		t.Fatalf("%s duration state = available %t, lower bound %t", name, got.DurationAvailable, got.DurationLowerBound)
	}
}

func assertCLIJSONSourceLines(t *testing.T, name string, observations []cliJSONObservation, want []uint64) {
	t.Helper()
	if len(observations) != len(want) {
		t.Fatalf("%s observations = %d, want %d", name, len(observations), len(want))
	}
	for i, line := range want {
		if observations[i].Source == nil || observations[i].Source.StartLine != line || observations[i].Source.EndLine != line {
			t.Fatalf("%s observation %d source = %+v, want line %d", name, i, observations[i].Source, line)
		}
	}
}

func TestRunProfileJSONInputIdentityUsesOnlyBasename(t *testing.T) {
	original := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	firstDir, secondDir := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := os.Mkdir(firstDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondDir, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(firstDir, "capture.log")
	second := filepath.Join(secondDir, "capture.log")
	renamed := filepath.Join(secondDir, "renamed-\n-\x1b-\"-日本語.log")
	for _, path := range []string{first, second, renamed} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	render := func(path string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := run([]string{"--profile", "--format=json", path}, &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %q", stderr.Bytes())
		}
		return stdout.Bytes()
	}
	firstJSON, secondJSON, renamedJSON := render(first), render(second), render(renamed)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("identical input with the same basename produced different JSON bytes")
	}

	renamedDocument := decodeCLIJSONProfile(t, renamedJSON)
	if renamedDocument.Input.Basename != filepath.Base(renamed) {
		t.Fatalf("renamed basename = %q, want %q", renamedDocument.Input.Basename, filepath.Base(renamed))
	}
	var firstComplete, renamedComplete map[string]any
	if err := json.Unmarshal(firstJSON, &firstComplete); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(renamedJSON, &renamedComplete); err != nil {
		t.Fatal(err)
	}
	firstInput, firstOK := firstComplete["input"].(map[string]any)
	renamedInput, renamedOK := renamedComplete["input"].(map[string]any)
	if !firstOK || !renamedOK {
		t.Fatal("decoded JSON input metadata is not an object")
	}
	firstInput["basename"] = ""
	renamedInput["basename"] = ""
	if !reflect.DeepEqual(firstComplete, renamedComplete) {
		t.Fatal("renaming identical input changed profile data beyond input.basename")
	}
	if bytes.Contains(renamedJSON, []byte(root)) {
		t.Fatalf("JSON contains absolute temporary directory %q", root)
	}
}

func TestRunProfileJSONRoundTripsIdentifierControls(t *testing.T) {
	const identifier = "module.example[\"line\nESC\x1b雪\"].test_resource.quoted"
	resource := map[string]any{"addr": identifier, "module": "module.example", "resource_type": "test_resource", "implied_provider": "test"}
	lines := make([][]byte, 2)
	for i, kind := range []string{"apply_start", "apply_complete"} {
		hook := map[string]any{"resource": resource, "action": "create"}
		if kind == "apply_complete" {
			hook["elapsed_seconds"] = 1
		}
		var err error
		lines[i], err = json.Marshal(map[string]any{"@level": "info", "@module": "terraform.ui", "@timestamp": "2026-09-11T00:00:00Z", "@message": "synthetic", "type": kind, "hook": hook})
		if err != nil {
			t.Fatal(err)
		}
	}
	input := filepath.Join(t.TempDir(), "identifiers.log")
	contents := append(append(lines[0], '\n'), append(lines[1], '\n')...)
	if err := os.WriteFile(input, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--profile", "--format=json", input}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.Bytes())
	}
	document := decodeCLIJSONProfile(t, stdout.Bytes())
	if len(document.UIObservations) != 1 || document.UIObservations[0].Address != identifier {
		t.Fatalf("decoded address = %q, want %q", document.UIObservations[0].Address, identifier)
	}
}

type profileJSONShortWriter struct{}

func (profileJSONShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestRunProfileJSONPropagatesInputOutputAndWriterErrors(t *testing.T) {
	input := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	t.Run("stdout error", func(t *testing.T) {
		closed, err := os.CreateTemp(t.TempDir(), "closed")
		if err != nil {
			t.Fatal(err)
		}
		if err := closed.Close(); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		err = run([]string{"--profile", "--format=json", input}, closed, &stderr)
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("run error = %v, want %v", err, os.ErrClosed)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %q", stderr.Bytes())
		}
	})

	cases := []struct {
		name   string
		args   []string
		stdout io.Writer
		want   error
	}{
		{name: "missing input", args: []string{"--profile", "--format=json", filepath.Join(t.TempDir(), "missing.log")}, want: os.ErrNotExist},
		{name: "output is directory", args: []string{"--profile", "--format=json", "-o", t.TempDir(), input}},
		{name: "short stdout", args: []string{"--profile", "--format=json", input}, stdout: profileJSONShortWriter{}, want: io.ErrShortWrite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if tc.stdout == nil {
				tc.stdout = &stdout
			}
			var stderr bytes.Buffer
			err := run(tc.args, tc.stdout, &stderr)
			if err == nil {
				t.Fatal("run returned nil error")
			}
			if tc.name == "output is directory" {
				if !strings.Contains(err.Error(), tc.args[3]) {
					t.Fatalf("error %q does not name output directory", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("run error = %v, want %v", err, tc.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.Bytes())
			}
			if stdout.Len() != 0 {
				t.Fatalf("failure wrote stdout: %q", stdout.Bytes())
			}
		})
	}
}

func TestProfileTextFormatMatchesDefaultAndHonoursLimit(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "two-rpcs.log")
	render := func(args ...string) []byte {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := run(append(args, path), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("unexpected stderr: %q", stderr.Bytes())
		}
		return stdout.Bytes()
	}
	omitted := render("--profile")
	explicit := render("--profile", "--format=text")
	if !bytes.Equal(omitted, explicit) {
		t.Fatal("explicit text format changed default profile bytes")
	}
	limited := render("--profile", "--format=text", "--limit=1")
	if !bytes.Contains(limited, []byte("SLOWEST CALLS (top 1 of 2)")) {
		t.Fatalf("text limit was not applied:\n%s", limited)
	}
}
