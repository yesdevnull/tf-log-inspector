package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	var document struct {
		SchemaVersion uint8  `json:"schema_version"`
		Kind          string `json:"kind"`
		ToolVersion   string `json:"tool_version"`
		Input         struct {
			Basename string `json:"basename"`
			Bytes    uint64 `json:"bytes"`
		} `json:"input"`
		Tiers struct {
			RPC struct {
				Records    uint64 `json:"records"`
				Admitted   uint64 `json:"admitted"`
				Rejected   uint64 `json:"rejected"`
				DurationMs uint64 `json:"duration_ms"`
			} `json:"rpc"`
		} `json:"tiers"`
		Quality struct {
			ProviderEntries uint64 `json:"provider_entries"`
			StructuredLines uint64 `json:"structured_lines"`
		} `json:"quality"`
		RPCObservations []struct {
			Source *struct {
				StartLine uint64 `json:"start_line"`
				EndLine   uint64 `json:"end_line"`
			} `json:"source"`
		} `json:"rpc_observations"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("second decode error = %v, want EOF", err)
	}
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
	if len(document.RPCObservations) != 21 {
		t.Fatalf("JSON observation count = %d, want 21", len(document.RPCObservations))
	}
	first, last := document.RPCObservations[0].Source, document.RPCObservations[20].Source
	if first == nil || first.StartLine != 1 || first.EndLine != 1 || last == nil || last.StartLine != 21 || last.EndLine != 21 {
		t.Fatalf("source lines = first %+v, last %+v", first, last)
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
