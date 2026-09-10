// Package attrib attributes provider RPC spans to the Terraform resource
// addresses they belong to. This is inference, not observation: a provider
// RPC line carries a resource TYPE and never an address, so the address is
// correlated from Terraform's own structured-output stream, which does carry
// one. Everything this package produces is labelled with the confidence it
// was produced at, and nothing it produces is ever written back onto a
// span.Span -- Span records what the log stated.
package attrib

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

// ctxLine reads the envelope independently of the hook's schema. Action
// hooks carry an object in hook.action where resource hooks carry a string;
// both contribute timestamps even though only resource hooks bound contexts.
type ctxLine struct {
	Timestamp json.RawMessage `json:"@timestamp"`
	Type      json.RawMessage `json:"type"`
	Hook      json.RawMessage `json:"hook"`
}

// ctxHook decodes only resource lifecycle fields. Message and resource ID
// values are never decoded or retained in a Context.
type ctxHook struct {
	Resource *struct {
		Addr          string          `json:"addr"`
		Module        string          `json:"module"`
		ModuleKnown   bool            `json:"-"`
		ModuleInvalid bool            `json:"-"`
		Resource      string          `json:"resource"`
		ResourceType  string          `json:"resource_type"`
		ResourceName  string          `json:"resource_name"`
		ResourceKey   json.RawMessage `json:"resource_key"`
	} `json:"resource"`
	Action string `json:"action"`
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
	Entry         uint32
	Address       string
	Module        string // "" when the resource is not in a module
	ModuleKnown   bool
	ModuleInvalid bool
	Name          string
	// Key is "" when the resource has no index key, and otherwise already
	// in the syntax Terraform's own address puts inside the trailing
	// brackets -- bare for a count key, quoted for a for_each key (see
	// decodeKey) -- so building an address is Name (or Module) directly
	// against "[" + Key + "]", with nothing left for the caller to decide.
	Key          string
	ResourceType string
	Action       string // hook.action: read, create, update, delete
	IsData       bool   // the address names a data source, not a managed resource
	Start, End   time.Time
	Unclosed     bool // true when the context was never closed by a matching terminator before end-of-log; End is the log's last timestamp for end-of-log closures, but may be earlier if a duplicate-start closed it
}

// opensContext and closesContext name the hook types literally rather than
// by glob. A glob over "apply_*" would admit apply_progress, which carries a
// PARTIAL elapsed_seconds and must never terminate a context -- the same
// guard span.isCompletionType applies when building durations.
//
// refresh_start/refresh_complete are Terraform's plan-time drift-detection
// walk (PreRefresh/PostRefresh in hashicorp/terraform's
// internal/command/views/hook_json.go), a different hook pair from
// apply_start/apply_complete's action:"read" data-source read (PreApply with
// plans.Read). Both are real, and neither stands in for the other: a call
// correlated against the wrong pair's window would attribute time to work
// that never happened. There is no refresh_errored: refresh closes only via
// refresh_complete, or stays unclosed -- confirmed against hashicorp/
// terraform tag v1.14.9's internal/command/views/json/message_types.go,
// which defines no such constant.
func opensContext(t string) bool {
	switch t {
	case "apply_start", "refresh_start", "ephemeral_op_start", "provision_start":
		return true
	}
	return false
}

func closesContext(t string) bool {
	switch t {
	case "apply_complete", "apply_errored",
		"refresh_complete",
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

	evidence    ContextEvidence
	closedPairs int
	zeroExtent  uint64
	// closedOut marks that Contexts' close-out (draining c.open,
	// back-filling End, tallying zeroExtent) has already run, so a second
	// call is a plain getter rather than a second mutation. See Contexts'
	// own doc comment.
	closedOut bool
}

// ContextEvidence records independently observable limitations in context
// collection, retaining the first scanner ordinal for each reason.
type ContextEvidence struct {
	SyntaxErrors, SchemaErrors               span.IssueCount
	MissingTimestamps, InvalidTimestamps     span.IssueCount
	UnmatchedTerminators, IncompleteContexts span.IssueCount
}

func noteIssue(dst *span.IssueCount, entry uint32) {
	if dst.Count == 0 || entry < dst.FirstEntry {
		dst.FirstEntry = entry
	}
	dst.Count++
}

// Entry implements logfmt.Sink as a no-op: this collector only cares about
// structured lines, delivered via Structured.
func (c *ContextCollector) Entry(uint32, logfmt.Entry, string, logfmt.Fields) {}

// Structured implements logfmt.StructuredSink.
func (c *ContextCollector) Structured(ord uint32, _ logfmt.Entry, line string) {
	if !json.Valid([]byte(line)) {
		noteIssue(&c.evidence.SyntaxErrors, ord)
		return
	}
	var cl ctxLine
	if err := json.Unmarshal([]byte(line), &cl); err != nil {
		noteIssue(&c.evidence.SchemaErrors, ord)
		return
	}
	schema := false

	var typ string
	typeValid := true
	if len(cl.Type) > 0 {
		if !decodeContextString(cl.Type, &typ) {
			schema = true
			typeValid = false
		}
	}

	if c.counts == nil {
		c.counts = make(map[string]uint64)
	}
	if typeValid {
		c.counts[typ]++
	}

	ts, timestampOK, timestampSchema := contextTimestamp(cl.Timestamp)
	schema = schema || timestampSchema
	if !timestampOK {
		if len(cl.Timestamp) == 0 || bytes.Equal(bytes.TrimSpace(cl.Timestamp), []byte("null")) {
			noteIssue(&c.evidence.MissingTimestamps, ord)
		} else {
			var value string
			if decodeContextString(cl.Timestamp, &value) && value == "" {
				noteIssue(&c.evidence.MissingTimestamps, ord)
			} else {
				noteIssue(&c.evidence.InvalidTimestamps, ord)
			}
		}
		if schema {
			noteIssue(&c.evidence.SchemaErrors, ord)
		}
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

	if !opensContext(typ) && !closesContext(typ) {
		if schema {
			noteIssue(&c.evidence.SchemaErrors, ord)
		}
		return
	}
	hook, hookSchema, hookValid := parseContextHook(cl.Hook)
	schema = schema || hookSchema
	if schema {
		noteIssue(&c.evidence.SchemaErrors, ord)
	}
	if !hookValid {
		return
	}
	if hook == nil || hook.Resource == nil {
		return
	}
	r := hook.Resource
	key := r.Addr + "\x00" + hook.Action

	switch {
	case opensContext(typ):
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
			Entry:         ord,
			Address:       r.Addr,
			Module:        r.Module,
			ModuleKnown:   r.ModuleKnown,
			ModuleInvalid: r.ModuleInvalid,
			Name:          r.ResourceName,
			Key:           decodeKey(r.ResourceKey),
			ResourceType:  r.ResourceType,
			Action:        hook.Action,
			IsData:        strings.HasPrefix(r.Resource, "data."),
			Start:         ts,
			End:           ts,
			Unclosed:      true,
		})
		c.open[key] = len(c.ctxs) - 1

	case closesContext(typ):
		i, ok := c.open[key]
		if !ok {
			// A terminator with no start -- a log that begins mid-run.
			// Counted rather than synthesising a window whose start
			// nothing in the log states.
			noteIssue(&c.evidence.UnmatchedTerminators, ord)
			return
		}
		c.ctxs[i].End = ts
		c.ctxs[i].Unclosed = false
		delete(c.open, key)
		c.closedPairs++
	}
}

func contextTimestamp(raw json.RawMessage) (time.Time, bool, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return time.Time{}, false, false
	}
	var value string
	if !decodeContextString(raw, &value) {
		return time.Time{}, false, true
	}
	if value == "" {
		return time.Time{}, false, false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	return timestamp, err == nil, false
}

func decodeContextString(raw json.RawMessage, destination *string) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	return json.Unmarshal(raw, destination) == nil
}

func parseContextHook(raw json.RawMessage) (*ctxHook, bool, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, true, false
	}
	hook := &ctxHook{}
	schema := false
	valid := true
	if value := fields["action"]; len(value) > 0 {
		schema = !decodeContextString(value, &hook.Action)
		valid = !schema
	}
	resourceRaw := fields["resource"]
	if len(resourceRaw) == 0 || bytes.Equal(bytes.TrimSpace(resourceRaw), []byte("null")) {
		return hook, schema, true
	}
	var resourceFields map[string]json.RawMessage
	if err := json.Unmarshal(resourceRaw, &resourceFields); err != nil || resourceFields == nil {
		return hook, true, false
	}
	hook.Resource = new(struct {
		Addr          string          `json:"addr"`
		Module        string          `json:"module"`
		ModuleKnown   bool            `json:"-"`
		ModuleInvalid bool            `json:"-"`
		Resource      string          `json:"resource"`
		ResourceType  string          `json:"resource_type"`
		ResourceName  string          `json:"resource_name"`
		ResourceKey   json.RawMessage `json:"resource_key"`
	})
	for field, destination := range map[string]*string{
		"addr":          &hook.Resource.Addr,
		"resource":      &hook.Resource.Resource,
		"resource_type": &hook.Resource.ResourceType,
		"resource_name": &hook.Resource.ResourceName,
	} {
		if value := resourceFields[field]; len(value) > 0 && !decodeContextString(value, destination) {
			schema = true
			valid = false
		}
	}
	if value, present := resourceFields["module"]; present {
		hook.Resource.ModuleKnown = decodeContextString(value, &hook.Resource.Module)
		hook.Resource.ModuleInvalid = !hook.Resource.ModuleKnown
		if hook.Resource.ModuleInvalid {
			schema = true
		}
	}
	resourceKey := resourceFields["resource_key"]
	if validResourceKey(resourceKey) {
		hook.Resource.ResourceKey = resourceKey
	} else {
		schema = true
		valid = false
	}
	return hook, schema, valid
}

func validResourceKey(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return true
	}
	if value[0] == '"' {
		var decoded string
		return json.Unmarshal(value, &decoded) == nil
	}
	return value[0] == '-' || value[0] >= '0' && value[0] <= '9'
}

// decodeKey renders hook.resource.resource_key, which may be null, a JSON
// number, or a JSON string, as the bracket suffix Terraform's own address
// syntax puts it in: bare for a numeric (count) key, module.m.web[0], and
// still double-quoted for a string (for_each) key, module.m.web["mykey"].
// The two are NOT interchangeable syntax, and a string that happens to look
// numeric -- for_each = toset(["0"]) produces the JSON STRING "0", not the
// number 0 -- makes which one it was undecidable from the decoded value
// alone. It is only decidable here, from the raw JSON's own type, which is
// why this keeps the JSON string's quoting rather than stripping it: every
// caller of Context.Key and Attribution.Key builds an address by
// concatenating Name (or Module) directly against "[" + Key + "]", and does
// so without re-deriving what this function already knows.
//
// Null and empty values return an empty string, for a key-less resource.
func decodeKey(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return s
}

// Contexts returns the collected contexts in the order their start lines
// appeared. A context still open at end-of-log has its End set to the last
// timestamp seen and stays marked Unclosed, matching how an unclosed SPAN is
// handled -- measured to end-of-log and flagged, never discarded.
//
// The close-out this does -- draining c.open, back-filling End, tallying
// zero-extent contexts for ZeroExtentContexts -- runs once and is memoised:
// a second call is a plain getter over the first call's result, not a
// second mutation. Without that, a future caller that took a snapshot of
// the pre-close-out state (e.g. to correlate against it directly) could see
// every still-open context gain a zero-extent window under it on whichever
// call ran second, rather than the mutation being over and done with after
// the first.
func (c *ContextCollector) Contexts() []Context {
	if !c.closedOut {
		for key, i := range c.open {
			c.ctxs[i].End = c.lastTS
			delete(c.open, key)
		}
		for _, ctx := range c.ctxs {
			// A zero-extent window occupies no instant (see Context's
			// half-open-window doc comment) and is never a candidate for
			// anything. This is the case the end-of-log close-out above can
			// itself produce: a resource still running when the capture is
			// cut gets an End equal to the log's own last timestamp, which
			// is also its Start.
			if !ctx.Start.Before(ctx.End) {
				c.zeroExtent++
			}
			if ctx.Unclosed {
				noteIssue(&c.evidence.IncompleteContexts, ctx.Entry)
			}
		}
		c.closedOut = true
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

// LastTS is the last parseable @timestamp on any structured line, tracked the
// same unconditional way as firstTS -- on every line this collector sees,
// whether or not it carries a hook Terraform's own resource lifecycle uses.
// That makes it a true upper bound on the stream's wall clock even when the
// log's last event is one span.UIHookBuilder builds no span for (provision_*
// and refresh_* completions carry no elapsed_seconds -- see
// span.isCompletionType), which the max of built spans' own EndMs would
// otherwise understate.
func (c *ContextCollector) LastTS() time.Time { return c.lastTS }

// TypeCounts is the histogram of structured-output "type" values. Type names
// and counts only: it is rendered by --diagnose, which is masked for sharing.
func (c *ContextCollector) TypeCounts() map[string]uint64 { return c.counts }

// Malformed preserves the aggregate count exposed to existing callers.
func (c *ContextCollector) Malformed() uint64 {
	return c.evidence.SyntaxErrors.Count + c.evidence.SchemaErrors.Count
}

// UnmatchedTerminators reports terminators seen with no matching start,
// which is what a log captured from partway through a run produces.
func (c *ContextCollector) UnmatchedTerminators() uint64 {
	return c.evidence.UnmatchedTerminators.Count
}

// Evidence returns the completed context-quality facts.
func (c *ContextCollector) Evidence() ContextEvidence {
	c.Contexts()
	return c.evidence
}

// ZeroExtentContexts reports how many collected contexts have zero
// duration -- Start not strictly before End -- and so can never be a
// candidate for any span (see Context's half-open-window doc comment). It
// calls Contexts to force the close-out that can itself produce one, so the
// count is accurate whether or not a caller has fetched Contexts yet.
func (c *ContextCollector) ZeroExtentContexts() uint64 {
	c.Contexts()
	return c.zeroExtent
}

// CompletedPairs reports how many contexts were both opened and closed. It
// is NOT attribution's presence gate -- that is len(Contexts()) > 0 (see
// model.Log.HasAddressContext and diagnose.Build), since a context is
// appended the moment a _start hook is seen and a capture killed mid-run
// can carry real, still-open context with zero completed pairs. This is
// reported as a finer-grained figure alongside the unclosed-context count,
// not as a precondition for anything.
func (c *ContextCollector) CompletedPairs() int { return c.closedPairs }
