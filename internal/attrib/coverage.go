package attrib

import "github.com/yesdevnull/tf-log-inspector/internal/span"

// confidenceCount is how many Confidence values there are. It is used with
// the guard in Summarise to prevent out-of-range panic when indexing
// ByConfidence and MsByConfidence. Drift is caught at test time by
// TestConfidenceCountMatchesEnum, which ties the constant to the Confidence
// enum in correlate.go.
const confidenceCount = 5

// Coverage is the published distribution of attribution outcomes. It is what
// --diagnose reports and what gates view 3, and it carries both a count and
// a TIME total per confidence value -- the two answer different questions and
// a distribution that resolved every short call and no long one would look
// healthy by count alone.
type Coverage struct {
	Spans   int
	TotalMs uint64

	ByConfidence   [confidenceCount]int
	MsByConfidence [confidenceCount]uint64

	// Candidates maps a candidate count to how many spans saw that many.
	// It is counts of candidates, never the candidates themselves, so it is
	// safe for --diagnose's masked output.
	Candidates map[uint32]int
}

// Summarise builds the distribution over a span slice and its parallel
// attribution table. It stops at the shorter of the two: keeping them
// parallel is the caller's invariant, and panicking here would blame this
// code for a mistake made elsewhere.
func Summarise(spans []span.Span, attribs []Attribution) Coverage {
	c := Coverage{Candidates: make(map[uint32]int)}
	n := min(len(spans), len(attribs))
	for i := 0; i < n; i++ {
		s, a := spans[i], attribs[i]
		c.Spans++
		c.TotalMs += uint64(s.DurationMs)
		if int(a.Confidence) < confidenceCount {
			c.ByConfidence[a.Confidence]++
			c.MsByConfidence[a.Confidence] += uint64(s.DurationMs)
		}
		c.Candidates[a.Candidates]++
	}
	return c
}

// NameableShare is the share of span TIME that resolved to a verdict which
// puts a name on the screen -- Contained or Likely. Overlapping is excluded:
// it names an address, but on evidence too weak to build a ranked view on.
//
// This is the number phase 5's gate is expressed against: view 3 ships as a
// view when it is at least 0.5. That threshold is a judgement recorded in
// the spec, not a measurement.
func (c Coverage) NameableShare() float64 {
	if c.TotalMs == 0 {
		return 0
	}
	nameable := c.MsByConfidence[Contained] + c.MsByConfidence[Likely]
	return float64(nameable) / float64(c.TotalMs)
}
