package profile

import (
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

func render(t *testing.T, path string) string {
	t.Helper()
	l, err := model.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var sb strings.Builder
	if err := Render(&sb, l); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

// Unlike --diagnose, this report shows real addresses. A reader who assumes
// otherwise could paste a confidential resource address somewhere it does not
// belong, so the warning is part of the contract, not decoration.
func TestReportWarnsThatOutputIsUnmasked(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "not masked") {
		t.Errorf("report does not warn that it is unmasked:\n%s", out)
	}
}

func TestReportShowsResourceTypeJoin(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "BY RESOURCE TYPE") {
		t.Errorf("report missing the resource-type join:\n%s", out)
	}
	if !strings.Contains(out, "aws_instance") {
		t.Errorf("report does not name a resource type present in the fixture:\n%s", out)
	}
}

// UI-hook figures are whole seconds carrying up to a second of error each, so
// a report that ranks them must say so -- the same caveat --diagnose carries.
func TestReportStatesUIHookResolution(t *testing.T) {
	out := render(t, "../../testdata/structured-ui.log")
	if !strings.Contains(out, "whole seconds") {
		t.Errorf("report does not state the UI-hook timing resolution:\n%s", out)
	}
}

func TestReportShowsProviderRollup(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "BY PROVIDER") {
		t.Errorf("report missing the provider rollup:\n%s", out)
	}
}

func TestReportShowsConcurrencyForRPCSpans(t *testing.T) {
	out := render(t, "../../testdata/provider-rpc.log")
	if !strings.Contains(out, "CONCURRENCY") {
		t.Errorf("report missing the concurrency section:\n%s", out)
	}
	if !strings.Contains(out, "peak") {
		t.Errorf("concurrency section does not report a peak:\n%s", out)
	}
}

// A log with neither tier must say so rather than printing empty headings.
func TestReportSaysSoWhenThereAreNoSpans(t *testing.T) {
	out := render(t, "../../testdata/core-only.log")
	if !strings.Contains(out, "no spans") {
		t.Errorf("report does not explain an empty result:\n%s", out)
	}
}
