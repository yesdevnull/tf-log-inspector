package span

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func refreshLine(typ, address, timestamp string) string {
	return fmt.Sprintf(`{"@level":"info","@timestamp":%q,"type":%q,"hook":{"resource":{"addr":%q,"resource_type":"aws_instance","implied_provider":"aws"}}}`+"\n", timestamp, typ, address)
}

func TestRefreshWindowUsesMatchedTimestamps(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, refreshLine("refresh_start", "aws_instance.example", "2026-09-11T00:00:01Z")+refreshLine("refresh_complete", "aws_instance.example", "2026-09-11T00:00:03.250Z"), &b)
	got := b.Spans()
	if len(got) != 1 {
		t.Fatalf("refresh observations = %v, want one 2250ms window", got)
	}
	if got[0].DurationMs != 2250 || got[0].StartMs != 0 || got[0].EndMs != 2250 || !got[0].HasPosition() || got[0].RPC != "refresh" {
		t.Fatalf("refresh observation = %+v", got[0])
	}
	if got[0].DurationSource != SourceRefreshWindow || !got[0].HasStartEntry || got[0].StartEntry != 0 || got[0].Entry != 1 {
		t.Fatalf("refresh provenance = %+v", got[0])
	}
}

func TestCLICompletionsKeepDurationsWithoutPositions(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, "Terraform will perform these actions:\n\x1b[32maws_instance.example: Creation complete after 2m16s [id=example]\x1b[0m\naws_instance.example: Destruction complete after 0s\n", &b)
	got := b.Spans()
	if len(got) != 2 {
		t.Fatalf("CLI observations = %v, want two independently linked completions", got)
	}
	if got[0].DurationMs != 136000 || got[1].DurationMs != 0 || got[0].HasPosition() || got[1].HasPosition() || got[0].Entry == got[1].Entry || got[0].RPC != "create" || got[1].RPC != "delete" {
		t.Fatalf("CLI observations = %+v", got)
	}
	if got[0].DurationSource != SourceCLIElapsed || got[0].HasStartEntry {
		t.Fatalf("CLI provenance = %+v", got[0])
	}
}

func TestRefreshWindowQuantisationAndSaturation(t *testing.T) {
	for _, tc := range []struct {
		start, end            string
		duration              uint32
		positioned, saturated bool
	}{
		{"2026-09-11T00:00:01.0009Z", "2026-09-11T00:00:01.0011Z", 0, true, false},
		{"2026-09-11T00:00:01Z", "2026-09-11T00:00:01Z", 0, true, false},
		{"2026-09-11T00:00:01Z", "2027-09-11T00:00:01Z", math.MaxUint32, false, true},
	} {
		var b UIHookBuilder
		scanUIInto(t, refreshLine("refresh_start", "aws_instance.a", tc.start)+refreshLine("refresh_complete", "aws_instance.a", tc.end), &b)
		got := b.Spans()
		if len(got) != 1 || got[0].DurationMs != tc.duration || got[0].DurationSaturated != tc.saturated || got[0].HasPosition() != tc.positioned {
			t.Fatalf("window = %+v", got)
		}
	}
}

func TestRefreshTrackingOverflowDoesNotResumeAmbiguousPairing(t *testing.T) {
	var b UIHookBuilder
	for i := 0; i < maxOpenRefresh+1; i++ {
		b.Structured(uint32(i), logfmt.Entry{}, refreshLine("refresh_start", fmt.Sprintf("aws_instance.r%d", i), "2026-09-11T00:00:01Z"))
	}
	b.Structured(maxOpenRefresh+1, logfmt.Entry{}, refreshLine("refresh_complete", "aws_instance.r0", "2026-09-11T00:00:02Z"))
	b.Structured(maxOpenRefresh+2, logfmt.Entry{}, refreshLine("refresh_start", "aws_instance.overflow", "2026-09-11T00:00:02Z"))
	b.Structured(maxOpenRefresh+3, logfmt.Entry{}, refreshLine("refresh_complete", "aws_instance.overflow", "2026-09-11T00:00:03Z"))
	if got := b.Spans(); len(got) != 1 || got[0].Address != "aws_instance.r0" {
		t.Fatalf("overflow observations = %+v", got)
	}
	e := b.Evidence()
	if e.Rejected["refresh_tracking_overflow"].Count != 2 || e.Rejected["refresh_incomplete"].Count != maxOpenRefresh-1 {
		t.Fatalf("overflow evidence = %+v", e)
	}
}

func TestCLIAdmissionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		line      string
		duration  uint32
		saturated bool
	}{
		{`module.m["a.b: c"].data.aws_ami.example[0]: Read complete after 1.250s`, 1250, false},
		{`aws_instance.example: Modifications complete after 1h2m3s`, 3723000, false},
		{`aws_instance.example: Read complete after 1s [id=id_0009`, 1000, false},
		{`aws_instance.example: Read complete after 1s [id=opaque: Read complete after 2s]`, 1000, false},
		{`aws_instance.example: Refresh complete after 999999999999999999999999h`, math.MaxUint32, true},
	} {
		t.Run(tc.line, func(t *testing.T) {
			var b UIHookBuilder
			scanUIInto(t, tc.line+"\n", &b)
			got := b.Spans()
			if len(got) != 1 || got[0].DurationMs != tc.duration || got[0].DurationSaturated != tc.saturated || got[0].HasPosition() {
				t.Fatalf("CLI observation = %+v", got)
			}
		})
	}
	for _, line := range []string{
		`aws_instance.example: Creation complete after -1s`,
		`aws_instance.example: Creation complete after 1s2m`,
		`aws_instance.example: Creation complete after NaNs`,
		`aws_instance.example: Creation complete after 2s trailing words`,
		`module.bad: Creation complete after 2s`,
		`aws_instance.example[-1]: Creation complete after 2s`,
		`prefix aws_instance.example: Creation complete after 2s`,
		`  aws_instance.example: Creation complete after 2s`,
		`aws_instance.example: Still creating... [2s elapsed]`,
		`{"message":"aws_instance.example: Creation complete after 2s"}`,
		`{"@level":"info","@timestamp":"2026-09-11T00:00:01Z","response":{"type":"refresh_complete","hook":{"resource":{"addr":"aws_instance.example"}}}}`,
	} {
		t.Run(line, func(t *testing.T) {
			var b UIHookBuilder
			scanUIInto(t, line+"\n", &b)
			if got := b.Spans(); len(got) != 0 {
				t.Fatalf("unexpected observations = %+v", got)
			}
		})
	}
}

func TestMixedStreamsSuppressCLIRegardlessOfOrder(t *testing.T) {
	cli := "aws_instance.example: Creation complete after 2s\n"
	for _, structured := range []string{
		refreshLine("refresh_start", "aws_instance.example", "2026-09-11T00:00:01Z"),
		"2026-09-11T00:00:01.000Z [DEBUG] provider.aws: response:\n",
		`{"@level":"debug","@timestamp":"2026-09-11T00:00:01Z","@module":"provider.aws","@message":"response"}` + "\n",
	} {
		for _, input := range []string{cli + structured, structured + cli} {
			var b UIHookBuilder
			scanUIInto(t, input, &b)
			if got := b.Spans(); len(got) != 0 {
				t.Fatalf("mixed observations = %+v", got)
			}
			if got := b.Evidence().Rejected["cli_suppressed"].Count; got != 1 {
				t.Fatalf("suppressed = %d", got)
			}
		}
	}
}

func TestRefreshPairingRejectsAmbiguousAndInvalidWindows(t *testing.T) {
	for _, tc := range []struct {
		name, input, reason string
		count               uint64
	}{
		{"unmatched", refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:01Z"), "refresh_unmatched", 1},
		{"incomplete", refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:01Z"), "refresh_incomplete", 1},
		{"backwards", refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:03Z") + refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:01Z"), "refresh_backwards", 1},
		{"repeated", refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:01Z") + refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:02Z") + refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:03Z"), "refresh_ambiguous", 1},
		{"invalid start", refreshLine("refresh_start", "aws_instance.a", "bad") + refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:03Z"), "refresh_timestamp_invalid", 1},
		{"missing address", refreshLine("refresh_start", "", "2026-09-11T00:00:01Z"), "refresh_address_invalid", 1},
		{"malformed address", refreshLine("refresh_start", "not address", "2026-09-11T00:00:01Z") + refreshLine("refresh_complete", "not address", "2026-09-11T00:00:02Z"), "refresh_address_invalid", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b UIHookBuilder
			scanUIInto(t, tc.input, &b)
			if got := b.Spans(); len(got) != 0 {
				t.Fatalf("unexpected windows = %+v", got)
			}
			if got := b.Evidence().Rejected[tc.reason].Count; got != tc.count {
				t.Fatalf("%s = %d; evidence %+v", tc.reason, got, b.Evidence())
			}
		})
	}
}

func TestRefreshPairingSupportsConcurrentAndRepeatedAddresses(t *testing.T) {
	var b UIHookBuilder
	input := refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:01Z") + refreshLine("refresh_start", "aws_instance.b", "2026-09-11T00:00:02Z") + refreshLine("refresh_complete", "aws_instance.b", "2026-09-11T00:00:03Z") + refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:04Z") + refreshLine("refresh_start", "aws_instance.a", "2026-09-11T00:00:05Z") + refreshLine("refresh_complete", "aws_instance.a", "2026-09-11T00:00:05Z")
	scanUIInto(t, input, &b)
	got := b.Spans()
	if len(got) != 3 || got[0].Address != "aws_instance.b" || got[0].DurationMs != 1000 || got[1].DurationMs != 3000 || got[2].DurationMs != 0 {
		t.Fatalf("windows = %+v", got)
	}
	if got := b.Evidence(); len(got.Rejected) != 0 || got.Records != 3 {
		t.Fatalf("evidence = %+v", got)
	}
}

func TestCLIRepeatedCompletionsAreIndependent(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, strings.Repeat("aws_instance.example: Read complete after 1s\n", 2), &b)
	if got := b.Spans(); len(got) != 2 {
		t.Fatalf("CLI completions = %+v", got)
	}
}

func TestRefreshAmbiguityPersistsUntilAllOpenStartsClose(t *testing.T) {
	var b UIHookBuilder
	for i, typ := range []string{"refresh_start", "refresh_start", "refresh_complete", "refresh_start", "refresh_complete", "refresh_complete"} {
		b.Structured(uint32(i), logfmt.Entry{}, refreshLine(typ, "aws_instance.a", "2026-09-11T00:00:01Z"))
	}
	if got := b.Spans(); len(got) != 0 {
		t.Fatalf("ambiguous observations = %+v", got)
	}
	if got := b.Evidence().Rejected["refresh_ambiguous"].Count; got != 3 {
		t.Fatalf("ambiguous completions = %d", got)
	}
}

func TestTimestampedProseDoesNotSuppressCLI(t *testing.T) {
	var b UIHookBuilder
	scanUIInto(t, "2026-09-11T00:00:01.000Z runner started\naws_instance.example: Read complete after 1s\n", &b)
	if got := b.Spans(); len(got) != 1 || got[0].Entry != 1 {
		t.Fatalf("CLI observations = %+v", got)
	}
}
