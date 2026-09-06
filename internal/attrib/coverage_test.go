package attrib

import (
	"math"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestSummariseCountsAndTimesByConfidence(t *testing.T) {
	spans := []span.Span{
		{DurationMs: 100}, {DurationMs: 200}, {DurationMs: 300},
	}
	attribs := []Attribution{
		{Confidence: Contained}, {Confidence: Ambiguous}, {Confidence: Likely},
	}
	got := Summarise(spans, attribs)

	if got.Spans != 3 {
		t.Errorf("Spans = %d, want 3", got.Spans)
	}
	if got.TotalMs != 600 {
		t.Errorf("TotalMs = %d, want 600", got.TotalMs)
	}
	if got.ByConfidence[Contained] != 1 || got.ByConfidence[Likely] != 1 || got.ByConfidence[Ambiguous] != 1 {
		t.Errorf("ByConfidence = %v", got.ByConfidence)
	}
	if got.MsByConfidence[Contained] != 100 {
		t.Errorf("MsByConfidence[Contained] = %d, want 100", got.MsByConfidence[Contained])
	}
	if got.MsByConfidence[Likely] != 300 {
		t.Errorf("MsByConfidence[Likely] = %d, want 300", got.MsByConfidence[Likely])
	}
	// The one slot the two assertions above leave unchecked -- an
	// off-by-one in the ByConfidence/MsByConfidence indexing could shift
	// Ambiguous's figure onto a neighbour and still pass both.
	if got.MsByConfidence[Ambiguous] != 200 {
		t.Errorf("MsByConfidence[Ambiguous] = %d, want 200", got.MsByConfidence[Ambiguous])
	}
}

// The gate is on TIME, not on span count. This fixture inverts the two: two
// of three spans are nameable, but they carry only a tenth of the time.
func TestNameableShareIsTimeWeightedNotCountWeighted(t *testing.T) {
	spans := []span.Span{
		{DurationMs: 50}, {DurationMs: 50}, {DurationMs: 900},
	}
	attribs := []Attribution{
		{Confidence: Contained}, {Confidence: Likely}, {Confidence: Ambiguous},
	}
	got := Summarise(spans, attribs).NameableShare()
	if math.Abs(got-0.1) > 1e-9 {
		t.Errorf("NameableShare = %v, want 0.1 -- a count-weighted share would be 0.666", got)
	}
}

func TestNameableShareExcludesOverlapping(t *testing.T) {
	spans := []span.Span{{DurationMs: 100}, {DurationMs: 100}}
	attribs := []Attribution{{Confidence: Contained}, {Confidence: Overlapping}}
	if got := Summarise(spans, attribs).NameableShare(); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("NameableShare = %v, want 0.5", got)
	}
}

func TestNameableShareOfNothingIsZeroNotNaN(t *testing.T) {
	if got := Summarise(nil, nil).NameableShare(); got != 0 {
		t.Errorf("NameableShare = %v, want 0", got)
	}
}

func TestCandidateHistogram(t *testing.T) {
	spans := []span.Span{{DurationMs: 1}, {DurationMs: 1}, {DurationMs: 1}}
	attribs := []Attribution{
		{Candidates: 1, Confidence: Contained},
		{Candidates: 4, Confidence: Ambiguous},
		{Candidates: 4, Confidence: Ambiguous},
	}
	got := Summarise(spans, attribs).Candidates
	if got[1] != 1 || got[4] != 2 {
		t.Errorf("Candidates = %v, want {1:1, 4:2}", got)
	}
}

// Summarise must not be handed mismatched slices, but if it is, it must not
// index out of range: the parallel-table invariant is the caller's to keep
// and a panic here would blame the wrong code.
func TestSummariseStopsAtTheShorterSlice(t *testing.T) {
	got := Summarise([]span.Span{{DurationMs: 5}, {DurationMs: 5}}, []Attribution{{Confidence: Contained}})
	if got.Spans != 1 {
		t.Errorf("Spans = %d, want 1", got.Spans)
	}
}

// The mirror of TestSummariseStopsAtTheShorterSlice: more attributions than
// spans. min(len(spans), len(attribs)) picks len(spans) here, so replacing
// the min with len(attribs) alone -- which the other direction's test cannot
// catch, since there len(spans) is already the smaller of the two -- would
// still pass that test while indexing spans out of range here.
func TestSummariseStopsAtTheShorterSliceWhenAttribsIsLonger(t *testing.T) {
	got := Summarise([]span.Span{{DurationMs: 5}}, []Attribution{{Confidence: Contained}, {Confidence: Likely}})
	if got.Spans != 1 {
		t.Errorf("Spans = %d, want 1", got.Spans)
	}
}
