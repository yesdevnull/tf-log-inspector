package logfmt

import (
	"os"
	"strings"
	"testing"
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
