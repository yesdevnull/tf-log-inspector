// Package attrib attributes provider RPC spans to the Terraform resource
// addresses they belong to. This is inference, not observation: a provider
// RPC line carries a resource TYPE and never an address, so the address is
// correlated from Terraform's own structured-output stream, which does carry
// one. Everything this package produces is labelled with the confidence it
// was produced at, and nothing it produces is ever written back onto a
// span.Span -- Span records what the log stated.
package attrib

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

// ctxLine is the subset of Terraform's structured-output line schema this
// package needs. Like span.uiLine it deliberately has no field for
// "@message", "id_key" or "id_value": encoding/json ignores unknown keys
// rather than erroring on them, so those three -- which between them carry a
// resource's real id value -- are never materialised at all. The disclosure
// guarantee is a property of this struct's shape, not of code elsewhere
// remembering not to read them.
type ctxLine struct {
	Timestamp string `json:"@timestamp"`
	Type      string `json:"type"`
	Hook      *struct {
		Resource *struct {
			Addr         string          `json:"addr"`
			Module       string          `json:"module"`
			Resource     string          `json:"resource"`
			ResourceType string          `json:"resource_type"`
			ResourceName string          `json:"resource_name"`
			ResourceKey  json.RawMessage `json:"resource_key"`
		} `json:"resource"`
		Action string `json:"action"`
	} `json:"hook"`
}

// Context is one window during which Terraform was working on one resource
// address. Start and End are ABSOLUTE times, taken from the lines' own
// RFC3339Nano timestamps rather than from elapsed_seconds -- which is
// quantised to whole seconds (see the spec's open question 7) and would blur
// every boundary by up to a second in each direction.
//
// The window is half-open, [Start, End), matching model.PackLanes. A
// zero-extent window occupies no instant and is never a candidate for
// anything.
type Context struct {
	Address      string
	Module       string // "" when the resource is not in a module
	Name         string
	Key          string // "" when the resource has no index key
	ResourceType string
	Action       string // hook.action: read, create, update, delete
	IsData       bool   // the address names a data source, not a managed resource
	Start, End   time.Time
	Unclosed     bool // no terminator before end-of-log; End is the log's last timestamp
}

// opensContext and closesContext name the hook types literally rather than
// by glob. A glob over "apply_*" would admit apply_progress, which carries a
// PARTIAL elapsed_seconds and must never terminate a context -- the same
// guard span.isCompletionType applies when building durations.
func opensContext(t string) bool {
	switch t {
	case "apply_start", "ephemeral_op_start", "provision_start":
		return true
	}
	return false
}

func closesContext(t string) bool {
	switch t {
	case "apply_complete", "apply_errored",
		"ephemeral_op_complete", "ephemeral_op_errored",
		"provision_complete", "provision_errored":
		return true
	}
	return false
}

// ContextCollector accumulates address contexts during a scan. It satisfies
// logfmt.StructuredSink, and Entry (a no-op) so it also satisfies
// logfmt.Sink and can be passed to logfmt.Scan directly. The zero value is
// ready to use.
type ContextCollector struct {
	ctxs   []Context
	open   map[string]int    // address+action -> index into ctxs
	counts map[string]uint64 // hook type -> occurrences

	firstTS, lastTS time.Time
	haveTS          bool

	malformed   uint64
	unmatched   uint64
	closedPairs int
}

// Entry implements logfmt.Sink as a no-op: this collector only cares about
// structured lines, delivered via Structured.
func (c *ContextCollector) Entry(uint32, logfmt.Entry, string, logfmt.Fields) {}

// Structured implements logfmt.StructuredSink.
func (c *ContextCollector) Structured(_ uint32, _ logfmt.Entry, line string) {
	var cl ctxLine
	if err := json.Unmarshal([]byte(line), &cl); err != nil {
		c.malformed++
		return
	}

	if c.counts == nil {
		c.counts = make(map[string]uint64)
	}
	c.counts[cl.Type]++

	ts, err := time.Parse(time.RFC3339Nano, cl.Timestamp)
	if err != nil {
		// A line with no usable timestamp cannot bound a window. It is
		// still counted in the histogram above, so its absence from the
		// contexts is visible rather than silent.
		return
	}
	if !c.haveTS {
		c.firstTS, c.haveTS = ts, true
	}
	if ts.After(c.lastTS) {
		c.lastTS = ts
	}

	if cl.Hook == nil || cl.Hook.Resource == nil {
		return
	}
	r := cl.Hook.Resource
	key := r.Addr + "\x00" + cl.Hook.Action

	switch {
	case opensContext(cl.Type):
		if c.open == nil {
			c.open = make(map[string]int)
		}
		// A second start for a key already open closes the first here
		// rather than dropping either. Two windows on one address stay two
		// contexts: merging them would span the gap between them and
		// attribute calls made in that gap to a resource that was not
		// being worked on.
		if prev, ok := c.open[key]; ok {
			c.ctxs[prev].End = ts
			c.ctxs[prev].Unclosed = true
		}
		c.ctxs = append(c.ctxs, Context{
			Address:      r.Addr,
			Module:       r.Module,
			Name:         r.ResourceName,
			Key:          decodeKey(r.ResourceKey),
			ResourceType: r.ResourceType,
			Action:       cl.Hook.Action,
			IsData:       strings.HasPrefix(r.Resource, "data."),
			Start:        ts,
			End:          ts,
			Unclosed:     true,
		})
		c.open[key] = len(c.ctxs) - 1

	case closesContext(cl.Type):
		i, ok := c.open[key]
		if !ok {
			// A terminator with no start -- a log that begins mid-run.
			// Counted rather than synthesising a window whose start
			// nothing in the log states.
			c.unmatched++
			return
		}
		c.ctxs[i].End = ts
		c.ctxs[i].Unclosed = false
		delete(c.open, key)
		c.closedPairs++
	}
}

// decodeKey renders hook.resource.resource_key, which is null for a resource
// with no index, a JSON string for a for_each key and a JSON number for a
// count index. The literal is used for a number and the quotes are stripped
// from a string, so both read the way they do inside an address.
func decodeKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if unquoted, err := strconv.Unquote(s); err == nil {
		return unquoted
	}
	return s
}

// Contexts returns the collected contexts in the order their start lines
// appeared. A context still open at end-of-log has its End set to the last
// timestamp seen and stays marked Unclosed, matching how an unclosed SPAN is
// handled -- measured to end-of-log and flagged, never discarded.
func (c *ContextCollector) Contexts() []Context {
	for key, i := range c.open {
		c.ctxs[i].End = c.lastTS
		delete(c.open, key)
	}
	return c.ctxs
}

// FirstTS is the first parseable @timestamp on any structured line, and is
// this stream's baseline. It is reported by --diagnose against
// logfmt.Stats.FirstTS as the re-basing constant between the two streams --
// NOT as a check on clock agreement, which it cannot be: the two mark
// different events (the hclog stream brackets the whole Terraform process,
// terraform.ui only the plan phase within it), so their difference is real
// elapsed time plus any skew and the two are not separable.
func (c *ContextCollector) FirstTS() time.Time { return c.firstTS }

// TypeCounts is the histogram of structured-output "type" values. Type names
// and counts only: it is rendered by --diagnose, which is masked for sharing.
func (c *ContextCollector) TypeCounts() map[string]uint64 { return c.counts }

// Malformed reports structured lines that failed to decode as JSON.
func (c *ContextCollector) Malformed() uint64 { return c.malformed }

// UnmatchedTerminators reports terminators seen with no matching start,
// which is what a log captured from partway through a run produces.
func (c *ContextCollector) UnmatchedTerminators() uint64 { return c.unmatched }

// CompletedPairs reports how many contexts were both opened and closed. It
// is attribution's precondition: below one completed pair there is no
// address context in this log at all.
func (c *ContextCollector) CompletedPairs() int { return c.closedPairs }
