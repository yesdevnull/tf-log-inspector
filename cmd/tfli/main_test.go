package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDiagnoseOnFixture(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "provider-rpc.log")}, &sb, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	for _, want := range []string{"tfli diagnostic report", "selected tier             reported", "spans built          2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
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

// The structured-output fixture must report structured lines and the
// structured-specific EXTRACTION guidance, and must never disclose the
// fixture's resource address.
func TestRunReportsStructuredOutputLog(t *testing.T) {
	var sb strings.Builder
	if err := run([]string{"--diagnose", filepath.Join("..", "..", "testdata", "structured-ui.log")}, &sb, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "structured lines     5") {
		t.Errorf("report missing structured line count:\n%s", out)
	}
	if !strings.Contains(out, "structured output (terraform.ui JSON)") {
		t.Errorf("report missing structured-output guidance:\n%s", out)
	}
	if strings.Contains(out, `module.module_name["key"].data.local_file.thing`) {
		t.Fatalf("report leaked the fixture's resource address:\n%s", out)
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

func TestRunRequiresDiagnoseInPhase1(t *testing.T) {
	var sb strings.Builder
	err := run([]string{"some.log"}, &sb, io.Discard)
	if err == nil {
		t.Fatal("run returned nil error when --diagnose was omitted")
	}
	if !strings.Contains(err.Error(), "--diagnose") {
		t.Errorf("error does not mention --diagnose: %v", err)
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
