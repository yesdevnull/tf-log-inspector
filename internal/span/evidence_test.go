package span

import (
	"math"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestUIAdmissionDistinguishesMissingAndZero(t *testing.T) {
	input := "{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"apply_complete\",\"hook\":{\"resource\":{\"addr\":\"x.a\"}}}\n" +
		"{\"@level\":\"info\",\"@timestamp\":\"2026-01-01T00:00:01Z\",\"type\":\"apply_complete\",\"hook\":{\"resource\":{\"addr\":\"x.b\"},\"elapsed_seconds\":0}}\n"
	var comps, ids logfmt.Interner
	var b UIHookBuilder
	if _, err := logfmt.Scan(strings.NewReader(input), &comps, &ids, &b); err != nil {
		t.Fatal(err)
	}
	if got := b.Spans(); len(got) != 1 || got[0].Entry != 1 || got[0].DurationMs != 0 {
		t.Fatalf("admitted %+v; want only explicit zero", got)
	}
	e := b.Evidence()
	if e.Records != 2 || e.Rejected["duration_missing"] != (IssueCount{Count: 1, FirstEntry: 0}) {
		t.Fatalf("evidence = %+v", e)
	}
}

func TestUIDurationAdmissionMatrix(t *testing.T) {
	lines := []string{
		`{"@timestamp":"2026-01-01T00:00:00Z","type":"apply_complete","hook":{"elapsed_seconds":null}}`,
		`{"@timestamp":"2026-01-01T00:00:01Z","type":"apply_complete","hook":{"elapsed_seconds":-1}}`,
		`{"@timestamp":"2026-01-01T00:00:02Z","type":"apply_complete","hook":{"elapsed_seconds":-1e-400}}`,
		`{"@timestamp":"2026-01-01T00:00:03Z","type":"apply_complete","hook":{"elapsed_seconds":"1"}}`,
		`{"@timestamp":"2026-01-01T00:00:04Z","type":"apply_complete","hook":{"elapsed_seconds":{}}}`,
		`{"@timestamp":"2026-01-01T00:00:05Z","type":"apply_complete","hook":{"elapsed_seconds":[]}}`,
		`{"@timestamp":"2026-01-01T00:00:06Z","type":"apply_complete","hook":{"elapsed_seconds":true}}`,
		`{"@timestamp":"2026-01-01T00:00:07Z","type":"apply_complete","hook":{"elapsed_seconds":0.0004}}`,
		`{"@timestamp":"2026-01-01T00:00:08Z","type":"apply_complete","hook":false}`,
		`{"@timestamp":"bad","type":"apply_complete","hook":{"elapsed_seconds":1e300}}`,
		`{"@timestamp":"2026-01-01T00:00:09Z","type":"apply_complete","hook":{"elapsed_seconds":0}`,
	}
	for i := range lines {
		lines[i] = strings.Replace(lines[i], "{", `{"@level":"info",`, 1)
	}
	var b UIHookBuilder
	scanUIInto(t, strings.Join(lines, "\n")+"\n", &b)
	if got := b.Spans(); len(got) != 2 || got[0].DurationMs != 0 || got[1].DurationMs != math.MaxUint32 {
		t.Fatalf("spans = %+v, want rounded zero and saturated duration", got)
	}
	if got := b.Spans()[1].PositionReasons(); strings.Join(got, ",") != "timestamp_invalid,duration_saturated" {
		t.Fatalf("reasons = %v", got)
	}
	e := b.Evidence()
	if e.Records != 10 || e.SyntaxErrors != (IssueCount{Count: 1, FirstEntry: 10}) || e.SchemaErrors != (IssueCount{Count: 1, FirstEntry: 8}) {
		t.Fatalf("stage evidence = %+v", e)
	}
	wantRejected := map[string]uint64{"duration_null": 1, "duration_negative": 2, "duration_invalid": 4, "record_schema_invalid": 1}
	var rejected uint64
	for reason, want := range wantRejected {
		rejected += e.Rejected[reason].Count
		if e.Rejected[reason].Count != want {
			t.Errorf("Rejected[%q] = %+v, want count %d", reason, e.Rejected[reason], want)
		}
	}
	if admitted := uint64(len(b.Spans())); admitted+rejected != e.Records {
		t.Fatalf("admitted %d + rejected %d != records %d", admitted, rejected, e.Records)
	}
	if e.TimestampIssues["timestamp_invalid"].Count != 1 {
		t.Errorf("timestamp issues = %+v", e.TimestampIssues)
	}
}

func TestUITimestampAndSchemaEvidenceAreIndependent(t *testing.T) {
	input := `{"@level":"info","@timestamp":"2026-01-01T00:00:00Z","type":false}` + "\n" +
		`{"@level":"info","@timestamp":null,"type":"version"}` + "\n" +
		`{"@level":"info","@timestamp":"2026-01-01T00:00:01Z","type":"apply_complete","hook":{"elapsed_seconds":1,"action":false,"resource":{"addr":false,"resource_type":false,"implied_provider":false}}}` + "\n"
	var b UIHookBuilder
	scanUIInto(t, input, &b)
	e := b.Evidence()
	if _, ok := b.Origin(); !ok {
		t.Fatal("valid timestamp with malformed type did not establish origin")
	}
	if e.TimestampIssues["timestamp_missing"] != (IssueCount{Count: 1, FirstEntry: 1}) {
		t.Fatalf("timestamp issues = %+v", e.TimestampIssues)
	}
	if e.SchemaErrors.Count != 2 || e.SchemaErrors.FirstEntry != 0 {
		t.Fatalf("schema errors = %+v, want two records with first at zero", e.SchemaErrors)
	}
	if len(b.Spans()) != 1 || b.Spans()[0].DurationMs != 1000 {
		t.Fatalf("malformed metadata discarded valid duration: %+v", b.Spans())
	}
}

func TestRPCAdmissionAndClockEvidence(t *testing.T) {
	input := "2026-01-01T00:00:00.000Z [TRACE] p: Received downstream response: tf_req_duration_ms=10\n" +
		"not timestamped\n" +
		"2025-12-31T23:59:59.000Z [TRACE] p: Received downstream response: tf_req_duration_ms=20\n" +
		"2026-01-01T00:00:02.000Z [TRACE] p: Received downstream response: tf_req_duration_ms=-1\n" +
		"2026-01-01T00:00:03.000Z [TRACE] p: Received downstream response\n" +
		"2026-01-01T00:00:04.000Z [TRACE] p: Received downstream response: tf_req_duration_ms=4294967296\n" +
		"2026-01-01T00:00:05.000Z [TRACE] p: Received downstream response: tf_req_duration_ms=nope\n"
	var b ReportedBuilder
	scanInto(t, input, &b)
	got := b.Spans()
	if len(got) != 2 || got[0].DurationMs != 10 || got[1].DurationMs != 20 {
		t.Fatalf("spans = %+v", got)
	}
	if total := uint64(got[0].DurationMs) + uint64(got[1].DurationMs); total != 30 || total/uint64(len(got)) != 15 {
		t.Fatalf("total/mean = %d/%d, want 30/15", total, total/uint64(len(got)))
	}
	if !got[0].HasPosition() || got[1].TimestampStatus != logfmt.TimestampBeforeOrigin || got[1].StartMs != 0 || got[1].EndMs != 0 {
		t.Fatalf("positions = %+v", got)
	}
	e := b.Evidence()
	if e.Records != 6 || e.Rejected["duration_negative"].Count != 1 || e.Rejected["duration_missing"].Count != 1 || e.Rejected["duration_out_of_range"].Count != 1 || e.Rejected["duration_invalid"].Count != 1 {
		t.Fatalf("evidence = %+v", e)
	}
}

func TestUIOutOfRangeTimestampRetainsDuration(t *testing.T) {
	input := `{"@level":"info","@timestamp":"2026-01-01T00:00:00Z","type":"version"}` + "\n" +
		`{"@level":"info","@timestamp":"2026-03-01T00:00:00Z","type":"apply_complete","hook":{"elapsed_seconds":2}}` + "\n"
	var b UIHookBuilder
	scanUIInto(t, input, &b)
	got := b.Spans()
	if len(got) != 1 || got[0].DurationMs != 2000 || got[0].TimestampStatus != logfmt.TimestampOutOfRange || got[0].StartMs != 0 || got[0].EndMs != 0 {
		t.Fatalf("span = %+v", got)
	}
	if b.Evidence().TimestampIssues["timestamp_out_of_range"].Count != 1 {
		t.Fatalf("timestamp issues = %+v", b.Evidence().TimestampIssues)
	}
}

func TestEvidenceReturnsDetachedMaps(t *testing.T) {
	var b ReportedBuilder
	scanInto(t, "2026-01-01T00:00:00.000Z [TRACE] p: Received downstream response\n", &b)
	e := b.Evidence()
	e.Rejected["duration_missing"] = IssueCount{}
	if b.Evidence().Rejected["duration_missing"].Count != 1 {
		t.Fatal("Evidence returned builder-owned map")
	}
}

func TestReportedBuilderConsumesClockForEveryEntry(t *testing.T) {
	var b ReportedBuilder
	b.EntryClock(0, logfmt.ClockPosition{OffsetMs: 50, Status: logfmt.TimestampValid})
	b.Entry(0, logfmt.Entry{}, "ordinary", nil)
	b.Entry(1, logfmt.Entry{}, responseMarker, logfmt.ParseFields("tf_req_duration_ms=10", nil))
	if got := b.Spans(); len(got) != 1 || got[0].TimestampStatus != logfmt.TimestampMissing || got[0].HasPosition() {
		t.Fatalf("stale callback leaked to next entry: %+v", got)
	}
}
