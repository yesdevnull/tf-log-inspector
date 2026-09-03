package logfmt

import (
	"math"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

type collector struct {
	ords    []uint32
	entries []Entry
	msgs    []string
	rpcs    []string
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

	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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

	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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

	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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

func TestScanOrdinalsAreSequential(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: one\n" +
		"2022-12-15T00:16:20.801Z [TRACE] a: two\n" +
		"2022-12-15T00:16:20.802Z [TRACE] a: three\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for i, ord := range c.ords {
		if ord != uint32(i) {
			t.Errorf("ordinal %d = %d, want %d", i, ord, i)
		}
	}
}

func TestScanRelativeTimestamps(t *testing.T) {
	in := "2022-12-15T00:16:20.800Z [TRACE] a: first\n" +
		"2022-12-15T00:16:25.900Z [TRACE] a: second\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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

	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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

	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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

	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(atThreshold), &comps, &c)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if st.LongContinuationRuns != 0 {
		t.Errorf("LongContinuationRuns = %d, want 0 for a run of exactly longRun lines", st.LongContinuationRuns)
	}

	overThreshold := "2022-12-15T00:16:20.800Z [TRACE] a: short\n" +
		strings.Repeat("cont\n", longRun+1) +
		"2022-12-15T00:16:20.900Z [TRACE] a: next\n"

	var comps2 Interner
	var c2 collector
	st2, err := Scan(strings.NewReader(overThreshold), &comps2, &c2)
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
		if e.Level != LevelUnknown {
			t.Errorf("entry %d Level = %v, want LevelUnknown", i, e.Level)
		}
	}
	if st.StructuredLines != 3 {
		t.Errorf("StructuredLines = %d, want 3", st.StructuredLines)
	}
}

// The test that matters most: a structured line's content -- which carries
// full resource and module addresses -- must never reach a sink.
func TestScanStructuredLineContentNeverReachesSink(t *testing.T) {
	in := structuredVersionLine + "\n" + structuredHookLine + "\n"
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	st, err := Scan(strings.NewReader(in), &comps, &c)
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
	var comps Interner
	var c collector
	var sc structuredCollector
	if _, err := Scan(strings.NewReader(in), &comps, &c, &sc); err != nil {
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
	var comps Interner
	var c collector
	if _, err := Scan(strings.NewReader(in), &comps, &c); err != nil {
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
		var comps Interner
		var col collector
		st, err := Scan(f, &comps, &col)
		f.Close()
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
