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

func TestUnboxedDiagnosticRetainsJSONBodyBeforeStructuredOutput(t *testing.T) {
	input := "Error: API rejected request\n\nResponse body:\n{\"code\":\"AccessDenied\",\"message\":\"Missing permission\"}\nContact the administrator.\n{\"@level\":\"info\",\"type\":\"version\",\"@message\":\"Terraform version\"}\n{\"@level\":\"info\",\"type\":\"change_summary\",\"changes\":{\"add\":0}}\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", l.Outcomes.Diagnostics)
	}
	diagnostic := l.Outcomes.Diagnostics[0]
	if !strings.Contains(diagnostic.Message, `"code":"AccessDenied"`) || !strings.Contains(diagnostic.Message, "Contact the administrator.") || diagnostic.Location.EndLine != 5 {
		t.Fatalf("diagnostic lost body or consumed structured output: %+v", diagnostic)
	}
	if len(l.Outcomes.Summaries) != 1 {
		t.Fatal("independent summary was swallowed")
	}
}

func TestCLIDiagnosticBlockRetainsOriginalSourceRange(t *testing.T) {
	input := "\x1b[33m╷\x1b[0m\n\x1b[33m│ Warning: Deprecated setting\x1b[0m\n│\n│   with aws_instance.example,\n│   on main.tf line 3, in resource \"aws_instance\" \"example\":\n│    3: legacy = true\n│\n│ Use the replacement setting.\n╵\naws_instance.example: Creating...\nPlan: 1 to add, 0 to change, 0 to destroy.\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	got := l.Outcomes.Diagnostics[0]
	if got.Address != "aws_instance.example" || got.Severity != "warning" || got.Source != "cli" {
		t.Fatalf("diagnostic identity = %#v", got)
	}
	if got.Location.StartLine != 1 || got.Location.EndLine != 9 || got.Location.StartByte != 0 || got.Location.EndByte != uint64(strings.Index(input, "aws_instance.example: Creating...")) {
		t.Fatalf("diagnostic source = %#v", got.Location)
	}
	if !strings.Contains(got.Message, "Deprecated setting") || !strings.Contains(got.Message, "Use the replacement setting.") {
		t.Fatalf("diagnostic message = %q", got.Message)
	}
	if len(l.Events) != 3 || l.Events[1].Kind != EventStart || l.Events[2].Kind != EventChangeSummary {
		t.Fatalf("adjacent evidence swallowed: %#v", l.Events)
	}
}

func TestCLIDiagnosticBlockStartsAtRunnerOwnedOpeningRule(t *testing.T) {
	input := "\x1b[33m2026-09-14T00:00:11.000Z [INFO] runner: ╷\x1b[0m\n│ Warning: Runner warning\n│\n│   with aws_instance.example,\n│ Detail\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 1 || l.Outcomes.Diagnostics[0].Location.StartLine != 1 || l.Outcomes.Diagnostics[0].Location.EndLine != 6 {
		t.Fatalf("runner diagnostic = %#v", l.Outcomes.Diagnostics)
	}
}

func TestCLIDiagnosticsRetainAddresslessEvidence(t *testing.T) {
	input := "╷\n│ Warning: General warning\n│\n│ Applies to the whole configuration.\n╵\nError: General error\n\nApplies to the whole run.\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	boxed, unboxed := l.Outcomes.Diagnostics[0], l.Outcomes.Diagnostics[1]
	if boxed.Address != "" || boxed.Severity != "warning" || boxed.Location.StartLine != 1 || boxed.Location.EndLine != 5 {
		t.Fatalf("boxed diagnostic = %#v", boxed)
	}
	if unboxed.Address != "" || unboxed.Severity != "error" || unboxed.Location.StartLine != 6 || unboxed.Location.EndLine != 8 {
		t.Fatalf("unboxed diagnostic = %#v", unboxed)
	}
}

func TestUnterminatedBoxedDiagnosticStopsBeforeIndependentEvidence(t *testing.T) {
	input := "╷\n│ Warning: Truncated warning\n│\n│   with aws_instance.warning,\n│ Detail without a closing rule\naws_instance.lifecycle: Creating...\nPlan: 1 to add, 0 to change, 0 to destroy.\n{\"@level\":\"info\",\"type\":\"resource_drift\",\"change\":{\"resource\":{\"addr\":\"aws_instance.drift\"},\"action\":\"update\"}}\n2026-09-14T00:00:11.000Z [INFO] runner: ╷\n│ Error: Later diagnostic\n│\n│   with aws_instance.later,\n│ later detail\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Events) != 5 {
		t.Fatalf("events = %#v", l.Events)
	}
	wantKinds := []EventKind{EventDiagnostic, EventStart, EventChangeSummary, EventDrift, EventDiagnostic}
	for i, want := range wantKinds {
		if l.Events[i].Kind != want {
			t.Errorf("event %d kind = %q, want %q", i, l.Events[i].Kind, want)
		}
	}
	if got := l.Events[0].Location; got.StartLine != 1 || got.EndLine != 5 {
		t.Fatalf("truncated diagnostic source = %#v", got)
	}
	if got := l.Events[4].Location; got.StartLine != 9 || got.EndLine != 14 {
		t.Fatalf("later diagnostic source = %#v", got)
	}
}

func TestUnterminatedBoxedDiagnosticStopsBeforeRunnerOwnedDiagnostic(t *testing.T) {
	input := "╷\n│ Warning: Truncated warning\n│\n│ first detail\n2026-09-14T00:00:11.000Z [INFO] runner: ╷\n│ Error: Later diagnostic\n│\n│   with aws_instance.later,\n│ later detail\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	if got := l.Outcomes.Diagnostics[0].Location; got.StartLine != 1 || got.EndLine != 4 {
		t.Fatalf("truncated diagnostic source = %#v", got)
	}
	if got := l.Outcomes.Diagnostics[1].Location; got.StartLine != 5 || got.EndLine != 10 {
		t.Fatalf("runner diagnostic source = %#v", got)
	}
}

func TestUnboxedDiagnosticStopsBeforeRunnerOwnedBoxedDiagnostic(t *testing.T) {
	input := "Warning: First warning\nfirst detail\n2026-09-14T00:00:11.000Z [INFO] runner: ╷\n│ Error: Later diagnostic\n│\n│ later detail\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	if got := l.Outcomes.Diagnostics[0].Location; got.StartLine != 1 || got.EndLine != 2 {
		t.Fatalf("unboxed diagnostic source = %#v", got)
	}
	if got := l.Outcomes.Diagnostics[1].Location; got.StartLine != 3 || got.EndLine != 7 {
		t.Fatalf("runner diagnostic source = %#v", got)
	}
}

func TestCLIDiagnosticRejectsProviderOwnedLookalike(t *testing.T) {
	input := "2026-09-14T00:00:11.000Z [DEBUG] provider.example: ╷\n│ Warning: Provider payload\n│\n│   with aws_instance.fake,\n│ Provider detail\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 0 {
		t.Fatalf("provider diagnostic admitted: %#v", l.Outcomes.Diagnostics)
	}
}

func TestUnboxedDiagnosticStopsBeforeProviderOwnedEntry(t *testing.T) {
	input := "Warning: Real CLI warning\nCLI detail\n2026-09-14T00:00:11.000Z [DEBUG] provider.example: response body:\n  with aws_instance.fake,\nprovider-only payload\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	got := l.Outcomes.Diagnostics[0]
	if got.Address != "" || got.Message != "Warning: Real CLI warning\nCLI detail" {
		t.Fatalf("provider entry contributed diagnostic evidence: %#v", got)
	}
	if got.Location.StartLine != 1 || got.Location.EndLine != 2 || got.Location.EndByte != uint64(strings.Index(input, "2026-09-14")) {
		t.Fatalf("diagnostic source = %#v", got.Location)
	}
}

func TestUnterminatedBoxedDiagnosticStopsBeforeProviderOwnedEntry(t *testing.T) {
	input := "╷\n│ Warning: Truncated CLI warning\n│ CLI detail\n2026-09-14T00:00:11.000Z [DEBUG] provider.example: response body:\n│   with aws_instance.fake,\n╵\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	got := l.Outcomes.Diagnostics[0]
	if got.Address != "" || got.Message != "Warning: Truncated CLI warning\nCLI detail" {
		t.Fatalf("provider entry contributed diagnostic evidence: %#v", got)
	}
	if got.Location.StartLine != 1 || got.Location.EndLine != 3 || got.Location.EndByte != uint64(strings.Index(input, "2026-09-14")) {
		t.Fatalf("diagnostic source = %#v", got.Location)
	}
}

func TestUnboxedAdjacentDiagnosticsStopBeforeOtherEvidence(t *testing.T) {
	input := "Warning: First warning\n\n  with aws_instance.first,\n  on first.tf line 1:\n\nfirst detail\nError: Second problem\n\n  with aws_instance.second,\n\nsecond detail\naws_instance.second: Creating...\nPlan: 1 to add, 0 to change, 0 to destroy.\n"
	l := loadResponseLog(t, input)
	if len(l.Outcomes.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", l.Outcomes.Diagnostics)
	}
	first, second := l.Outcomes.Diagnostics[0], l.Outcomes.Diagnostics[1]
	if first.Location.StartLine != 1 || first.Location.EndLine != 6 || first.Address != "aws_instance.first" || first.Severity != "warning" {
		t.Fatalf("first diagnostic = %#v", first)
	}
	if second.Location.StartLine != 7 || second.Location.EndLine != 11 || second.Address != "aws_instance.second" || second.Severity != "error" {
		t.Fatalf("second diagnostic = %#v", second)
	}
	if len(l.Events) != 4 || l.Events[2].Kind != EventStart || l.Events[3].Kind != EventChangeSummary {
		t.Fatalf("adjacent evidence swallowed: %#v", l.Events)
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

func TestCLIOutcomesDistinguishProviderPayloadFromRunnerOutput(t *testing.T) {
	const output = "  # aws_instance.example will be created\nPlan: 12 to add, 0 to change, 0 to destroy.\nNo changes. Your infrastructure matches the configuration.\n"
	for _, tc := range []struct {
		component  string
		wantEvents int
	}{
		{"provider.example", 0},
		{"runner", 3},
	} {
		t.Run(tc.component, func(t *testing.T) {
			l := loadResponseLog(t, "2026-09-14T00:00:11.000Z [DEBUG] "+tc.component+": output:\n"+output)
			if len(l.Events) != tc.wantEvents {
				t.Fatalf("events=%d, want %d", len(l.Events), tc.wantEvents)
			}
			if tc.wantEvents == 0 {
				if len(l.Outcomes.PlannedChanges) != 0 || len(l.Outcomes.Summaries) != 0 {
					t.Fatal("provider payload admitted as plan evidence")
				}
				return
			}
			if len(l.Outcomes.PlannedChanges) != 1 || len(l.Outcomes.Summaries) != 2 || l.Outcomes.PlannedChanges[0].Location.StartLine != 2 {
				t.Fatalf("runner outcomes lost: %+v", l.Outcomes)
			}
			if s := l.Outcomes.Summaries[0]; s.Location.StartLine != 3 || s.Summary.Add == nil || *s.Summary.Add != 12 {
				t.Fatalf("runner summary lost counts or source: %+v", s)
			}
		})
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

func TestCLIHistorySurvivesUnrelatedTimestampFooter(t *testing.T) {
	l := loadResponseLog(t, "aws_instance.a: Creating...\naws_instance.a: Still creating... [10s elapsed]\n2026-09-14T00:00:11.000Z [INFO] runner: stopping\n")
	if len(l.Events) != 2 || len(l.Incomplete) != 1 || l.Incomplete[0].LastProgress == nil {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
	provider := loadResponseLog(t, "2026-09-14T00:00:11.000Z [DEBUG] provider.example: response body:\naws_instance.a: Creating...\naws_instance.a: Creation complete after 1s\n")
	if len(provider.Events) != 0 {
		t.Fatalf("provider body became lifecycle: %+v", provider.Events)
	}
}

func TestCLIExtendedPlanSummaryPreservesOptionalCounts(t *testing.T) {
	for _, tc := range []struct {
		line                 string
		imports, invocations bool
	}{
		{"Plan: 1 to import, 0 to add, 0 to change, 0 to destroy.", true, false},
		{"Plan: 0 to add, 0 to change, 0 to destroy. Actions: 1 to invoke.", false, true},
		{"Plan: 1 to import, 0 to add, 0 to change, 0 to destroy. Actions: 1 to invoke.", true, true},
	} {
		l := loadResponseLog(t, tc.line+"\n")
		if len(l.Outcomes.Summaries) != 1 {
			t.Fatalf("summary missing for %q", tc.line)
		}
		s := l.Outcomes.Summaries[0].Summary
		if s.Add == nil || *s.Add != 0 || (s.Import != nil) != tc.imports || (s.ActionInvocation != nil) != tc.invocations {
			t.Fatalf("summary=%+v", s)
		}
		if s.Import != nil && *s.Import != 1 {
			t.Fatalf("import=%d", *s.Import)
		}
		if s.ActionInvocation != nil && *s.ActionInvocation != 1 {
			t.Fatalf("invocations=%d", *s.ActionInvocation)
		}
	}
	l := loadResponseLog(t, "Plan: 18446744073709551616 to import, 0 to add, 0 to change, 0 to destroy.\n")
	if len(l.Outcomes.Summaries) != 0 {
		t.Fatalf("overflow admitted: %+v", l.Outcomes)
	}
}

func TestCLIDeposedOperationsKeepDistinctIdentity(t *testing.T) {
	input := "aws_instance.a (deposed object abc12345): Destroying... [id=old]\naws_instance.a (deposed object def67890): Destroying... [id=older]\naws_instance.a: Destroying... [id=current]\naws_instance.a: Still destroying... [10s elapsed]\naws_instance.a (deposed object abc12345): Destruction complete after 11s\n"
	l := loadResponseLog(t, input)
	if len(l.Events) != 5 || len(l.Incomplete) != 2 {
		t.Fatalf("events=%+v incomplete=%+v", l.Events, l.Incomplete)
	}
	if l.Incomplete[0].Start.DeposedKey != "def67890" || l.Incomplete[1].Start.DeposedKey != "" {
		t.Fatalf("incorrect deposed pairing: %+v", l.Incomplete)
	}
	for _, op := range l.Incomplete {
		if op.Ambiguous || op.LastProgress != nil || op.Start.Address != "aws_instance.a" {
			t.Fatalf("guessed progress ownership: %+v", op)
		}
	}
}
