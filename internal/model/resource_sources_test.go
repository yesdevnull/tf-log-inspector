package model

import (
	"math"
	"reflect"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestDurationSourcesPreserveZerosAndLowerBounds(t *testing.T) {
	got := SummariseDurationSources([]span.Span{
		{DurationSource: span.SourceCLIElapsed, DurationMs: 0},
		{DurationSource: span.SourceRefreshWindow, DurationMs: 2250},
		{DurationSource: span.SourceUIElapsed, DurationMs: 1000},
		{DurationSource: span.SourceRefreshWindow, DurationMs: math.MaxUint32, DurationSaturated: true},
	})
	want := []DurationSourceSummary{
		{Source: span.SourceUIElapsed, Count: 1, DurationMs: 1000, MaxMs: 1000},
		{Source: span.SourceRefreshWindow, Count: 2, DurationMs: 4294969545, MaxMs: math.MaxUint32, DurationLowerBound: true},
		{Source: span.SourceCLIElapsed, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source summaries = %+v, want %+v", got, want)
	}
}

func TestLoadResourceSourcesPreservesAdmissionAndPhysicalIdentity(t *testing.T) {
	for _, tc := range []struct {
		file       string
		count      int
		duration   uint64
		positioned uint64
	}{
		{"resource-refresh.log", 2, 3250, 2},
		{"resource-cli.log", 3, 137000, 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			l, err := Load(fixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			q := l.CaptureQuality()
			if len(q.DurationSources) == 0 {
				t.Fatal("capture quality omitted duration sources")
			}
			if len(l.UISpans) != tc.count || q.UI.DurationMs != tc.duration || q.UI.Positioned != tc.positioned || q.UI.Admitted != uint64(tc.count) || len(l.RPCSpans) != 0 {
				t.Fatalf("spans=%+v quality=%+v", l.UISpans, q)
			}
			for _, s := range l.UISpans {
				loc, ok := l.SourceLocation(s.Entry)
				if !ok || loc.StartLine != uint64(s.Entry)+1 || loc.StartLine != loc.EndLine {
					t.Fatalf("completion location = %+v", loc)
				}
				if s.DurationSource == span.SourceCLIElapsed && (!l.UIOrigin.IsZero() || s.HasPosition()) {
					t.Fatalf("CLI acquired clock: %+v", s)
				}
			}
		})
	}
}

func TestObservationSourceIncludesRefreshStart(t *testing.T) {
	l, err := Load(fixture(t, "resource-refresh.log"))
	if err != nil {
		t.Fatal(err)
	}
	loc, ok := l.ObservationSource(l.UISpans[0])
	if !ok || loc.Entry != 2 || loc.StartLine != 2 || loc.EndLine != 3 {
		t.Fatalf("refresh location = %+v, %v", loc, ok)
	}
	if pos, ok := l.SourcePosition(2); !ok || pos.Entry != 1 || pos.EntryLine != 0 {
		t.Fatalf("start position = %+v, %v", pos, ok)
	}
}
