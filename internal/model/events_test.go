package model

import (
	"strings"
	"testing"
)

func TestResourceEventsKeepPhysicalSourceAndUnfinishedProgress(t *testing.T) {
	input := "heading\naws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\naws_instance.b: Refreshing state... [id=b]\n"
	l := loadResponseLog(t, input)
	if len(l.Events) != 3 || len(l.Incomplete) != 1 {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
	for i, e := range l.Events {
		if e.Location.StartLine != uint64(i+2) || !e.Timestamp.IsZero() {
			t.Fatalf("event source/clock = %+v", e)
		}
		position, ok := l.SourcePosition(e.Location.StartLine)
		if !ok || position.Entry != e.Location.Entry || !strings.Contains(string(l.Data[e.Location.StartByte:e.Location.EndByte]), e.Address) {
			t.Fatalf("event source does not resolve: %+v", e)
		}
	}
	if l.Incomplete[0].LastProgress == nil || l.Incomplete[0].LastProgress.Location.StartLine != 3 || len(l.UISpans) != 0 {
		t.Fatalf("incomplete evidence = %+v, spans=%+v", l.Incomplete, l.UISpans)
	}
}

func TestResourceEventsDoNotGuessRepeatedStarts(t *testing.T) {
	l := loadResponseLog(t, "aws_instance.a: Creating...\naws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\naws_instance.a: Creation complete after 12s\naws_instance.b: Read complete after 0s\n")
	if len(l.Events) != 5 || len(l.Incomplete) != 2 || len(l.UISpans) != 2 {
		t.Fatalf("events=%+v incomplete=%+v spans=%+v", l.Events, l.Incomplete, l.UISpans)
	}
	for _, op := range l.Incomplete {
		if !op.Ambiguous || op.LastProgress != nil {
			t.Fatalf("guessed ambiguous association: %+v", op)
		}
	}
}

func TestStructuredResourceOutcomesAndExactActionPairing(t *testing.T) {
	input := `{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"aws_instance.a"},"action":"create"}}
{"@level":"info","type":"apply_complete","hook":{"resource":{"addr":"aws_instance.a"},"action":"delete","elapsed_seconds":0}}
{"@level":"info","type":"apply_progress","@timestamp":"2026-09-14T00:00:10Z","hook":{"resource":{"addr":"aws_instance.a"},"action":"create"}}
{"@level":"warn","type":"diagnostic","diagnostic":{"address":"aws_instance.a","severity":"warning","summary":"warning text","detail":"detail"}}
{"@level":"info","type":"resource_drift","change":{"resource":{"addr":"aws_instance.a"},"action":"update"}}
{"@level":"info","type":"planned_change","change":{"resource":{"addr":"aws_instance.a"},"action":"delete"}}
{"@level":"info","type":"change_summary","changes":{"add":0,"change":0,"remove":1,"operation":"plan"}}
`
	l := loadResponseLog(t, input)
	if len(l.Events) != 7 || len(l.Incomplete) != 1 || l.Incomplete[0].LastProgress == nil {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
	if len(l.Outcomes.PlannedChanges) != 1 || len(l.Outcomes.Drift) != 1 || len(l.Outcomes.Diagnostics) != 1 || len(l.Outcomes.Summaries) != 1 {
		t.Fatalf("outcomes=%+v", l.Outcomes)
	}
	if !strings.Contains(l.Outcomes.Diagnostics[0].Message, "detail") || l.Outcomes.Summaries[0].Message == "" || l.Events[2].Timestamp.IsZero() {
		t.Fatalf("lost event details: %+v", l.Events)
	}
}

func TestResourceEventsRejectLookalikesAndSuppressMixedCLIRender(t *testing.T) {
	input := `{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"aws_instance.a"},"action":"create"}}
aws_instance.a: Creating...
aws_instance.a: Creation complete after 1s
{"response":{"type":"planned_change","change":{"resource":{"addr":"aws_instance.fake"},"action":"create"}}}
prefix aws_instance.fake: Creating...
{"@level":"info","type":"planned_change","change":false}
{"@level":"info","type":"diagnostic","diagnostic":false}
`
	l := loadResponseLog(t, input)
	if len(l.Events) != 1 || len(l.Incomplete) != 1 || len(l.Outcomes.PlannedChanges) != 0 || len(l.Outcomes.Summaries) != 0 {
		t.Fatalf("false evidence admitted: %+v", l.Events)
	}
}

func TestResourceEventCLISummaryPreservesObservedZeros(t *testing.T) {
	l := loadResponseLog(t, "2026-09-14T00:00:00Z [INFO] runner: header\n\x1b[32mPlan: 52 to add, 0 to change, 0 to destroy.\x1b[0m\nPlan: unknown\n")
	if len(l.Outcomes.Summaries) != 1 {
		t.Fatalf("summaries=%+v", l.Outcomes.Summaries)
	}
	e := l.Outcomes.Summaries[0]
	if e.Summary == nil || e.Summary.Add == nil || *e.Summary.Add != 52 || e.Summary.Change == nil || *e.Summary.Change != 0 || e.Summary.Remove == nil || *e.Summary.Remove != 0 || e.Summary.Import != nil || e.Summary.Operation != "plan" || e.Location.StartLine != 2 {
		t.Fatalf("summary=%+v event=%+v", e.Summary, e)
	}
}

func TestCLIResourceCompletionClosesOnlyItsExactAddress(t *testing.T) {
	l := loadResponseLog(t, "module.m[\"a:b\"].aws_instance.a: Creating...\r\nmodule.m[\"a:b\"].aws_instance.a: Still creating... [10s elapsed]\r\naws_instance.a: Creating...\r\nmodule.m[\"a:b\"].aws_instance.a: Creation complete after 11s")
	if len(l.Events) != 4 || len(l.Incomplete) != 1 || l.Incomplete[0].Start.Address != "aws_instance.a" || l.Events[3].Location.EndByte != uint64(len(l.Data)) {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
}

func TestResourceEventLifecycleEndsAndUnavailableOutcomes(t *testing.T) {
	for _, ending := range []string{"complete", "errored"} {
		t.Run(ending, func(t *testing.T) {
			l := loadResponseLog(t, `{"@level":"info","type":"refresh_start","hook":{"resource":{"addr":"aws_instance.a"}}}`+"\n"+`{"@level":"info","type":"refresh_`+ending+`","hook":{"resource":{"addr":"aws_instance.a"}}}`+"\n")
			if len(l.Events) != 2 || len(l.Incomplete) != 0 || len(l.Outcomes.Summaries) != 0 {
				t.Fatalf("events=%+v incomplete=%+v outcomes=%+v", l.Events, l.Incomplete, l.Outcomes)
			}
		})
	}
	l := loadResponseLog(t, `{"@level":"info","type":"change_summary","changes":{"add":-1}}`+"\n"+`{"@level":"info","type":"change_summary","changes":{}}`+"\n")
	if len(l.Outcomes.Summaries) != 0 {
		t.Fatalf("invalid/empty summary admitted: %+v", l.Outcomes)
	}
}

func TestCLIPlannedChangesAreAnchoredAndDoNotInventTotals(t *testing.T) {
	input := "  # aws_instance.a will be created\n  # aws_instance.b will be updated in-place\n  # aws_instance.c will be destroyed\n  # aws_instance.d must be replaced\n  # data.aws_instance.e will be read during apply\n"
	l := loadResponseLog(t, input+"quoted # aws_instance.fake will be created\n  # bad address will be created\n  # aws_instance.fake may be created\n")
	if len(l.Outcomes.PlannedChanges) != 5 || len(l.Outcomes.Summaries) != 0 {
		t.Fatalf("outcomes=%+v", l.Outcomes)
	}
	for i, action := range []string{"create", "update", "delete", "replace", "read"} {
		if e := l.Outcomes.PlannedChanges[i]; e.Action != action || e.Location.StartLine != uint64(i+1) {
			t.Fatalf("change=%+v", e)
		}
	}
}

func TestCLIExplicitNoChangesIsObservedZeroSummary(t *testing.T) {
	l := loadResponseLog(t, "No changes. Your infrastructure matches the configuration.\n")
	if len(l.Outcomes.Summaries) != 1 {
		t.Fatalf("summaries=%+v", l.Outcomes.Summaries)
	}
	s := l.Outcomes.Summaries[0].Summary
	if s == nil || s.Add == nil || *s.Add != 0 || s.Change == nil || *s.Change != 0 || s.Remove == nil || *s.Remove != 0 {
		t.Fatalf("summary=%+v", s)
	}
}

func TestCLIRefreshRetainsScrubbedTruncatedID(t *testing.T) {
	l := loadResponseLog(t, "aws_instance.a: Refreshing state... [id=masked\n")
	if len(l.Events) != 1 || l.Events[0].Kind != EventStart || l.Events[0].Action != "refresh" || len(l.Incomplete) != 0 {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
}
