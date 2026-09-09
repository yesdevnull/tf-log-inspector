package logfmt

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestScanRetainsEntriesBeyondClockRange(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exact := base.Add(time.Duration(math.MaxUint32) * time.Millisecond)
	beyond := base.Add((time.Duration(math.MaxUint32) + 1) * time.Millisecond)
	in := base.Format(tsLayout) + " [INFO] first\n" +
		exact.Format(tsLayout) + " [INFO] exact\n" +
		beyond.Format(tsLayout) + " [INFO] beyond\n" +
		base.Add(time.Millisecond).Format(tsLayout) + " [INFO] continues\n"
	var c collector
	st, err := Scan(strings.NewReader(in), &Interner{}, &Interner{}, &c)
	if err != nil || st.Entries != 4 {
		t.Fatalf("entries=%d, err=%v; want all entries retained", st.Entries, err)
	}
	if c.entries[1].TSms != math.MaxUint32 {
		t.Errorf("exact-boundary TSms = %d, want %d", c.entries[1].TSms, uint32(math.MaxUint32))
	}
}

type collector struct {
	ords    []uint32
	entries []Entry
	msgs    []string
	rpcs    []string
}

type clockCollector struct {
	clocks      []ClockPosition
	clockOrds   []uint32
	entryOrds   []uint32
	clockCounts []int
}

func (c *clockCollector) EntryClock(ord uint32, position ClockPosition) {
	c.clockOrds = append(c.clockOrds, ord)
	c.clocks = append(c.clocks, position)
	c.clockCounts = append(c.clockCounts, len(c.entryOrds))
}

func (c *clockCollector) Entry(ord uint32, e Entry, msg string, f Fields) {
	c.entryOrds = append(c.entryOrds, ord)
}

func TestScanDeliversClockBeforeEveryEntry(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	beyond := base.Add((time.Duration(math.MaxUint32) + 1) * time.Millisecond)
	in := "leading\ncontinued leading\n" +
		base.Format(tsLayout) + " [INFO] core: first\ncontinuation one\ncontinuation two\n" +
		structuredVersionLine + "\n" +
		beyond.Format(tsLayout) + " [INFO] core: beyond\n" +
		base.Add(-time.Millisecond).Format(tsLayout) + " [INFO] core: backwards\n"
	var c clockCollector
	st, err := Scan(strings.NewReader(in), &Interner{}, &Interner{}, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []ClockPosition{
		{Status: TimestampMissing},
		{Status: TimestampValid},
		{Status: TimestampMissing},
		{Status: TimestampOutOfRange},
		{Status: TimestampBeforeOrigin},
	}
	if !slices.Equal(c.clocks, want) {
		t.Errorf("clock positions = %+v, want %+v", c.clocks, want)
	}
	if !slices.Equal(c.clockOrds, c.entryOrds) {
		t.Errorf("clock ordinals %v do not match entry ordinals %v", c.clockOrds, c.entryOrds)
	}
	for i, entriesSeen := range c.clockCounts {
		if entriesSeen != i {
			t.Errorf("clock %d arrived after %d Entry calls, want %d", i, entriesSeen, i)
		}
	}
	if st.TimestampOffsetsOutOfRange != 1 || st.BackwardsTimestamps != 1 {
		t.Errorf("out-of-range=%d backwards=%d, want 1 each", st.TimestampOffsetsOutOfRange, st.BackwardsTimestamps)
	}
}

func (c *collector) Entry(ord uint32, e Entry, msg string, f Fields) {
	c.ords = append(c.ords, ord)
	c.entries = append(c.entries, e)
	c.msgs = append(c.msgs, msg)
	rpc, _ := f.Get("tf_rpc")
	c.rpcs = append(c.rpcs, rpc)
}

func TestScanGroupsContinuationLines(t *testing.T) {
	in := "2026-08-29T10:34:43.124+0200 [INFO]  CLI command args: []string{\"version\"}\n" +
		"Terraform v1.16.0\n" +
		"on linux_amd64\n" +
		"2026-08-29T10:34:43.220+0200 [TRACE] statemgr.Filesystem: unlocking\n"

	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.entries[0].Lines != 3 {
		t.Errorf("entry 0 Lines = %d, want 3", c.entries[0].Lines)
	}
	if st.ContinuationLines != 2 {
		t.Errorf("ContinuationLines = %d, want 2", st.ContinuationLines)
	}
	if st.PhysicalLines != 4 {
		t.Errorf("PhysicalLines = %d, want 4", st.PhysicalLines)
	}
	if comps.Lookup(c.entries[1].Comp) != "statemgr.Filesystem" {
		t.Errorf("entry 1 component = %q", comps.Lookup(c.entries[1].Comp))
	}
}

// Continuation text must not reach the sink. This is the disclosure guarantee
// and the reason field parsing cannot fuse across a line boundary.
func TestScanSinkSeesOnlyHeaderLineMessage(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] provider.aws: Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=12\n" +
		"Terraform used the selected providers to generate the following\n" +
		"  + resource \"aws_subnet\" \"example\" {\n"

	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if strings.Contains(c.msgs[0], "Terraform used") || strings.Contains(c.msgs[0], "aws_subnet") {
		t.Errorf("continuation text reached the sink: %q", c.msgs[0])
	}
	if c.rpcs[0] != "ReadResource" {
		t.Errorf("tf_rpc = %q, want ReadResource", c.rpcs[0])
	}
}

func TestScanOffsetsCoverWholeEntry(t *testing.T) {
	first := "2026-08-29T10:34:43.219+0200 [ERROR] a: one\n"
	cont := "continued\n"
	in := first + cont + "2026-08-29T10:34:43.220+0200 [TRACE] b: two\n"

	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[0].Off != 0 {
		t.Errorf("entry 0 Off = %d, want 0", c.entries[0].Off)
	}
	if want := uint32(len(first) + len(cont)); c.entries[0].Len != want {
		t.Errorf("entry 0 Len = %d, want %d", c.entries[0].Len, want)
	}
	if want := uint64(len(first) + len(cont)); c.entries[1].Off != want {
		t.Errorf("entry 1 Off = %d, want %d", c.entries[1].Off, want)
	}
}

func TestScanRelativeTimestamps(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: first\n" +
		"2022-12-15T00:16:25.900Z [TRACE] a: second\n"
	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[0].TSms != 0 {
		t.Errorf("first TSms = %d, want 0", c.entries[0].TSms)
	}
	if c.entries[1].TSms != 5100 {
		t.Errorf("second TSms = %d, want 5100", c.entries[1].TSms)
	}
}

// A timestamp earlier than the first entry must clamp, not wrap to ~4.29e9.
func TestScanBackwardsTimestampClamps(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: first\n" +
		"2022-12-15T00:16:12.800Z [TRACE] a: earlier\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.entries[1].TSms != 0 {
		t.Errorf("backwards TSms = %d, want 0", c.entries[1].TSms)
	}
	if st.BackwardsTimestamps != 1 {
		t.Errorf("BackwardsTimestamps = %d, want 1", st.BackwardsTimestamps)
	}
}

func TestScanLeadingUnstructuredContent(t *testing.T) {
	in := "Terraform will perform the following actions:\n" +
		"  + resource \"aws_subnet\" \"example\" {\n" +
		"2022-12-15T00:16:20.800Z [TRACE] a: real entry\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.entries[0].Timestamped {
		t.Error("leading block should not be marked timestamped")
	}
	if st.UntimestampedLines != 2 {
		t.Errorf("UntimestampedLines = %d, want 2", st.UntimestampedLines)
	}
}

// UntimestampedLines must count every untimestamped physical line in the file,
// not merely a leading block, or it cannot measure an HCP log's non-hclog
// proportion -- the thing phase 1 exists to measure.
func TestScanCountsUntimestampedLinesThroughoutFile(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"plan output line 1\n" +
		"plan output line 2\n" +
		"2022-12-15T00:16:20.900Z [TRACE] a: two\n" +
		"plan output line 3\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.UntimestampedLines != 3 {
		t.Errorf("UntimestampedLines = %d, want 3", st.UntimestampedLines)
	}
	if st.ContinuationBytes == 0 {
		t.Error("ContinuationBytes = 0, want non-zero")
	}
}

func TestScanCountsLevelsAndDetectsANSI(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"2022-12-15T00:16:20.801Z [DEBUG] a: two\n" +
		"2022-12-15T00:16:20.802Z [TRACE] a: \x1b[31mthree\x1b[0m\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.ByLevel[LevelTrace] != 2 {
		t.Errorf("TRACE count = %d, want 2", st.ByLevel[LevelTrace])
	}
	if !st.SawANSI {
		t.Error("SawANSI = false, want true")
	}
	if c.msgs[2] != "three" {
		t.Errorf("message = %q, want ANSI stripped to %q", c.msgs[2], "three")
	}
}

func TestScanCRLFAndNoTrailingNewline(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\r\n" +
		"2022-12-15T00:16:20.801Z [TRACE] a: two"
	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.msgs[1] != "two" {
		t.Errorf("last message = %q, want two", c.msgs[1])
	}
}

// maxHeaderMsg truncation is a byte-index cut and must not split a
// multi-byte rune. The euro sign here is placed so its first byte falls at
// byte maxHeaderMsg-1 of the padded prefix, putting the truncation boundary
// squarely inside the rune.
func TestScanHeaderMsgTruncationPreservesValidUTF8(t *testing.T) {
	pad := strings.Repeat("a", maxHeaderMsg-1)
	msg := pad + "€" + strings.Repeat("b", 100)
	in := "2022-12-15T00:16:20.800Z [TRACE] a: " + msg + "\n"

	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.msgs[0]) > maxHeaderMsg {
		t.Errorf("msg length = %d, want <= %d", len(c.msgs[0]), maxHeaderMsg)
	}
	if !utf8.ValidString(c.msgs[0]) {
		t.Errorf("truncated msg is not valid UTF-8: %q", c.msgs[0])
	}
}

// Entry.Lines is a uint16 and must saturate rather than wrap. Len is a
// uint32 and does not saturate, so it must still account for every byte of
// every continuation line, including those beyond the point Lines pins.
func TestScanEntryLinesSaturates(t *testing.T) {
	header := "2022-12-15T00:16:20.800Z [TRACE] a: first\n"
	const contLine = "c\n"
	const contLines = 66000 // comfortably past math.MaxUint16 continuation lines

	var b strings.Builder
	b.WriteString(header)
	for i := 0; i < contLines; i++ {
		b.WriteString(contLine)
	}
	in := b.String()

	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(c.entries))
	}
	if c.entries[0].Lines != math.MaxUint16 {
		t.Errorf("Lines = %d, want %d (saturated)", c.entries[0].Lines, uint16(math.MaxUint16))
	}
	if st.LinesSaturated == 0 {
		t.Error("LinesSaturated = 0, want non-zero")
	}
	wantLen := uint32(len(header) + contLines*len(contLine))
	if c.entries[0].Len != wantLen {
		t.Errorf("Len = %d, want %d -- Len must not saturate even though Lines does", c.entries[0].Len, wantLen)
	}
}

// LongContinuationRuns must trigger only once a run exceeds longRun lines,
// not merely reach it.
func TestScanLongContinuationRunsThreshold(t *testing.T) {
	atThreshold := "2022-12-15T00:16:20.800Z [TRACE] a: short\n" +
		strings.Repeat("cont\n", longRun) +
		"2022-12-15T00:16:20.900Z [TRACE] a: next\n"

	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(atThreshold), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.LongContinuationRuns != 0 {
		t.Errorf("LongContinuationRuns = %d, want 0 for a run of exactly longRun lines", st.LongContinuationRuns)
	}

	overThreshold := "2022-12-15T00:16:20.800Z [TRACE] a: short\n" +
		strings.Repeat("cont\n", longRun+1) +
		"2022-12-15T00:16:20.900Z [TRACE] a: next\n"

	var comps2, reqIDs2 Interner
	var c2 collector
	st2, err := Scan(strings.NewReader(overThreshold), &comps2, &reqIDs2, &c2)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st2.LongContinuationRuns != 1 {
		t.Errorf("LongContinuationRuns = %d, want 1 for a run of longRun+1 lines", st2.LongContinuationRuns)
	}
}

// Structured-output detection: Terraform's machine-readable UI JSON stream
// (one JSON object per line) must be recognised, counted, and never fused
// with the hclog text around it.

const structuredVersionLine = `{"@level":"info","@message":"Terraform 1.14.9","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.113402+10:00","terraform":"1.14.9","type":"version","ui":"1.2"}`

const structuredHookLine = `{"@level":"info","@message":"module.module_name[\"key\"].data.local_file.thing: Refreshing...","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:02.556000+10:00","hook":{"resource":{"addr":"module.module_name[\"key\"].data.local_file.thing","module":"module.module_name[\"key\"]","resource":"data.local_file.thing","implied_provider":"local","resource_type":"local_file","resource_name":"thing","resource_key":null},"action":"read"},"type":"apply_start"}`

// A file of N structured lines must yield N entries, not collapse into one
// the way the real bug report did.
func TestScanStructuredLinesEachBecomeOwnEntry(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n" + structuredVersionLine + "\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(c.entries))
	}
	for i, e := range c.entries {
		if e.Timestamped {
			t.Errorf("entry %d Timestamped = true, want false", i)
		}
		// The severity is read off the line, so a structured capture's
		// levels count and filter the way an hclog capture's do. Both
		// fixtures are "@level":"info".
		if e.Level != LevelInfo {
			t.Errorf("entry %d Level = %v, want the level the line carries", i, e.Level)
		}
	}
	if st.StructuredLines != 3 {
		t.Errorf("StructuredLines = %d, want 3", st.StructuredLines)
	}
	if st.ByLevel[LevelInfo] != 3 {
		t.Errorf("ByLevel[INFO] = %d, want 3 -- a structured capture's severities must reach the report", st.ByLevel[LevelInfo])
	}
}

// A structured capture reports its errors. Before its severity was read, an
// error line was indistinguishable from every other -- so the level facet
// offered one value, the diagnostic report counted every line as UNKNOWN,
// and the raw log drew a failure exactly like the traffic around it.
func TestScanReadsEachStructuredLinesOwnSeverity(t *testing.T) {
	const structuredErrorLine = `{"@level":"error","@message":"Error: creating resource","@module":"terraform.ui","@timestamp":"2026-09-04T09:15:03.000000+10:00","type":"diagnostic"}`
	in := structuredVersionLine + "\n" + structuredErrorLine + "\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.entries))
	}
	if c.entries[0].Level != LevelInfo || c.entries[1].Level != LevelError {
		t.Errorf("levels are %v and %v, want INFO then ERROR", c.entries[0].Level, c.entries[1].Level)
	}
	if st.ByLevel[LevelError] != 1 {
		t.Errorf("ByLevel[ERROR] = %d, want 1", st.ByLevel[LevelError])
	}
}

// The test that matters most: a structured line's content -- which carries
// full resource and module addresses -- must never reach a sink.
func TestScanStructuredLineContentNeverReachesSink(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n"
	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	const addr = `module.module_name["key"].data.local_file.thing`
	for i, msg := range c.msgs {
		if msg != "" {
			t.Errorf("entry %d msg = %q, want empty", i, msg)
		}
		if strings.Contains(msg, addr) {
			t.Errorf("entry %d msg leaked the resource address", i)
		}
	}
}

func TestScanCountsStructuredLinesInMixedFile(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		structuredVersionLine + "\n" +
		"2022-12-15T00:16:20.900Z [TRACE] a: two\n" +
		structuredHookLine + "\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.StructuredLines != 2 {
		t.Errorf("StructuredLines = %d, want 2", st.StructuredLines)
	}
	if len(c.entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(c.entries))
	}
}

// Detection must fail toward under-counting: a line that starts with '{' but
// lacks the "@level"/"@timestamp" signature (e.g. a JSON fragment surfacing
// in plan output) is left for the existing continuation/default handling,
// not counted as structured output.
func TestScanJSONFragmentWithoutSignatureNotCountedStructured(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		`{"resource_changes":[]}` + "\n"
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.StructuredLines != 0 {
		t.Errorf("StructuredLines = %d, want 0", st.StructuredLines)
	}
}

// structuredCollector records every StructuredSink.Structured call, so tests
// can assert on the raw line text an opted-in sink receives. It also
// implements Sink (as a no-op) purely so it can be passed to Scan alongside
// other sinks -- real StructuredSink implementations, such as
// span.UIHookBuilder, do the same.
type structuredCollector struct {
	ords  []uint32
	lines []string
}

func (c *structuredCollector) Entry(ord uint32, e Entry, msg string, f Fields) {}

func (c *structuredCollector) Structured(ord uint32, e Entry, line string) {
	c.ords = append(c.ords, ord)
	c.lines = append(c.lines, line)
}

// A sink that opts into StructuredSink must receive the structured line's raw
// text and the same ordinal the entry was flushed with -- the ordinary Sink
// channel still sees only an empty message, so the two channels must agree
// on ordinal numbering.
func TestScanStructuredSinkReceivesRawLine(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n"
	var comps, reqIDs Interner
	var c collector
	var sc structuredCollector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c, &sc); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(sc.lines) != 2 {
		t.Fatalf("got %d Structured calls, want 2", len(sc.lines))
	}
	if sc.lines[0] != structuredVersionLine {
		t.Errorf("line 0 = %q, want %q", sc.lines[0], structuredVersionLine)
	}
	if sc.lines[1] != structuredHookLine {
		t.Errorf("line 1 = %q, want %q", sc.lines[1], structuredHookLine)
	}
	if sc.ords[0] != c.ords[0] || sc.ords[1] != c.ords[1] {
		t.Errorf("Structured ordinals %v do not match Entry ordinals %v", sc.ords, c.ords)
	}
	// The ordinary Sink channel must still see nothing of the content.
	if c.msgs[0] != "" || c.msgs[1] != "" {
		t.Errorf("ordinary sink saw non-empty message: %q, %q", c.msgs[0], c.msgs[1])
	}
}

// A sink that does not implement StructuredSink -- the normal case, and the
// diagnose Collector's case in particular -- must not be affected by a
// structured line's presence in the file: entry count and ordinals are
// unchanged from before this channel existed.
func TestScanStructuredLinesDoNotAffectOrdinarySinkOrdinals(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		structuredHookLine + "\n" +
		"2022-12-15T00:16:20.900Z [TRACE] a: two\n"
	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []uint32{0, 1, 2}
	if len(c.ords) != len(want) {
		t.Fatalf("got %d ordinals, want %d", len(c.ords), len(want))
	}
	for i, o := range want {
		if c.ords[i] != o {
			t.Errorf("ordinal %d = %d, want %d", i, c.ords[i], o)
		}
	}
}

func TestScanRealFixtures(t *testing.T) {
	cases := []struct {
		file           string
		wantMinEntries int
		wantANSI       bool
	}{
		{"../../testdata/provider-rpc.log", 2, false},
		{"../../testdata/core-only.log", 6, false},
		{"../../testdata/multiline-body.log", 2, false},
		{"../../testdata/mixed-hcp.log", 2, false},
		{"../../testdata/structured-ui.log", 6, false},
	}
	for _, c := range cases {
		f, err := os.Open(c.file)
		if err != nil {
			t.Fatalf("open %s: %v", c.file, err)
		}
		var comps, reqIDs Interner
		var col collector
		st, err := Scan(f, &comps, &reqIDs, &col)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err != nil {
			t.Fatalf("Scan %s: %v", c.file, err)
		}
		if st.Entries < uint64(c.wantMinEntries) {
			t.Errorf("%s: %d entries, want at least %d", c.file, st.Entries, c.wantMinEntries)
		}
		if st.SawANSI != c.wantANSI {
			t.Errorf("%s: SawANSI = %v, want %v", c.file, st.SawANSI, c.wantANSI)
		}
	}
}

// A tf_req_id on a CONTINUATION line is counted, and separately so is the
// entry whose header carried none.
//
// Fields are read from header lines only, so an id that a provider wrote
// onto a wrapped line is invisible to everything downstream -- including any
// feature that groups a call's entries by request id. The two counts size
// that blind spot from either end: how much of it there is, and how many
// entries it actually costs. An entry whose header carries the id as well
// loses nothing when its continuations are unread, which is why the
// continuation-only count is the one a design decision turns on.
func TestScanCountsRequestIdsOnContinuationLines(t *testing.T) {
	const ts = "2026-08-29T10:34:43.124+0200 [TRACE] provider.aws: "
	in := ts + "HTTP Response Received: @module=aws\n" +
		"  http.response.body= {}\n" +
		"  http.duration=10705 tf_req_id=abc123\n" +
		ts + "Received downstream response: tf_req_id=abc123 tf_rpc=ReadResource\n" +
		"  a continuation whose header already carried the id, tf_req_id=abc123\n" +
		ts + "no id anywhere on this one\n" +
		"  nor on its continuation\n"

	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got, want := st.Entries, uint64(3); got != want {
		t.Fatalf("Entries = %d, want %d -- the fixture did not group as intended", got, want)
	}
	if got, want := st.ContinuationReqIDLines, uint64(2); got != want {
		t.Errorf("ContinuationReqIDLines = %d, want %d", got, want)
	}
	// Only the first entry: the second carries the id on its header too, and
	// the third carries none at all.
	if got, want := st.ContinuationOnlyReqIDEntries, uint64(1); got != want {
		t.Errorf("ContinuationOnlyReqIDEntries = %d, want %d", got, want)
	}
}

// The count is an UPPER BOUND, and this pins the reason. ParseFields splits
// on whitespace, so a key spelled inside a JSON body value parses as a field
// and is counted -- there is no way to tell the two apart without decoding
// the body, which the scan deliberately never does.
//
// That is the honest measure rather than a defect, because any feature
// reading ids off continuation lines inherits exactly this blind spot: it
// would scope by the same parse and admit the same line. A count that came
// back small therefore settles the question outright; one that came back
// large would need this case ruled out before it could be trusted.
func TestScanCountsARequestIdQuotedInsideABodyAsWell(t *testing.T) {
	const ts = "2026-08-29T10:34:43.124+0200 [TRACE] provider.aws: "
	in := ts + "HTTP Response Received: @module=aws\n" +
		`  http.response.body= {"note":"the string tf_req_id=abc is inside this body"}` + "\n"

	var comps, reqIDs Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got, want := st.ContinuationOnlyReqIDEntries, uint64(1); got != want {
		t.Errorf("ContinuationOnlyReqIDEntries = %d, want %d -- the bound is meant to include this case", got, want)
	}
}

// An entry's tf_req_id is interned and resolvable, and it is interned in the
// request-id interner rather than the component one -- the two are separate
// vocabularies whose ids would otherwise be silently comparable.
func TestScanInternsRequestIds(t *testing.T) {
	const ts = "2026-08-29T10:34:43.124+0200 [TRACE] provider.aws: "
	in := ts + "Sending request downstream: tf_req_id=abc123\n" +
		ts + "Received downstream response: tf_req_id=abc123\n" +
		ts + "an entry with no request id at all\n"

	var comps, reqIDs Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &reqIDs, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(c.entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(c.entries))
	}
	if a, b := c.entries[0].ReqID, c.entries[1].ReqID; a != b {
		t.Errorf("two entries of one call interned to %d and %d", a, b)
	}
	if got := reqIDs.Lookup(c.entries[0].ReqID); got != "abc123" {
		t.Errorf("Lookup(%d) = %q, want %q", c.entries[0].ReqID, got, "abc123")
	}
	if got := c.entries[2].ReqID; got != 0 {
		t.Errorf("an entry with no tf_req_id has ReqID %d, want 0", got)
	}
	// The component interner must not have been used for it: "abc123" is
	// not a component, and an id resolving there would mean the two spaces
	// were shared.
	if got := comps.Lookup(c.entries[0].ReqID); got == "abc123" {
		t.Errorf("the request id was interned into the component interner")
	}
}

// The interleaved fixture is what makes a scope testable, and this pins the
// three properties later tasks rely on: two calls, their entries
// interleaved, and one id readable only from a continuation.
//
// A fixture whose calls did not overlap would let a scope that took a
// contiguous RUN of entries pass, which is the defect most likely to be
// written by accident.
func TestInterleavedFixtureHasTwoOverlappingCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "interleaved-calls.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var comps, reqIDs Interner
	var c collector
	st, err := Scan(bytes.NewReader(data), &comps, &reqIDs, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	const a = "aaaaaaaa-0001-4a1b-8c2d-000000000001"
	const b = "bbbbbbbb-0002-4a1b-8c2d-000000000002"
	var seq []string
	for _, e := range c.entries {
		seq = append(seq, reqIDs.Lookup(e.ReqID))
	}
	// Nine entries, not eight: the header comment block is untimestamped, and
	// Scan indexes interleaved non-hclog content rather than discarding it,
	// so it is entry 0 and carries no request id. The HTTP entry's id is on a
	// continuation and so also reads as "" -- the header-only limit the scope
	// ships with.
	want := []string{"", a, b, a, b, "", a, a, b}
	if !slices.Equal(seq, want) {
		t.Errorf("entry request ids = %v,\nwant                  %v", seq, want)
	}
	if got := st.ContinuationOnlyReqIDEntries; got != 1 {
		t.Errorf("ContinuationOnlyReqIDEntries = %d, want 1 -- the fixture must carry one id on a continuation", got)
	}
}
