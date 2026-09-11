package model

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestProviderResponseAtSelectsPhysicalLineWithinEntry(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider.a: "
	source := head + `{"first":1}` + "\n" + head + `{"second":2}` + "\n"
	l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 2}}}
	got := l.ProviderResponseAt(0, 1)
	if got.State != "complete" || got.SourceLine != 2 || got.Response.Text != `{"second":2}` {
		t.Fatalf("selected wrong physical response: %+v", got)
	}
	if string(l.Data) != source {
		t.Fatal("source changed")
	}
}

func TestProviderResponseAtRejectsInvalidPhysicalPositionWithoutInspection(t *testing.T) {
	const source = "2026-09-11T00:00:00.000Z [DEBUG] provider.a: {\"ok\":1}\n"
	tests := []struct {
		name       string
		entry      uint32
		lineOffset int
	}{
		{name: "entry ordinal", entry: 1},
		{name: "negative line", lineOffset: -1},
		{name: "excessive line", lineOffset: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 1}}}
			got := l.ProviderResponseAt(tc.entry, tc.lineOffset)
			if got.State != "none" || got.SourceLine != 0 || got.Response.Text != "" || got.Diagnostic != nil || got.HasDiagnostics {
				t.Fatalf("selection = %+v", got)
			}
			if got := l.ReconstructionQuality(); got != (ReconstructionQuality{State: "not_checked"}) {
				t.Fatalf("quality = %+v", got)
			}
		})
	}
}

func TestExplicitInspectionWithoutSourceSelection(t *testing.T) {
	l := loadResponseLog(t, "ordinary log line\n")
	if got := l.ReconstructionDiagnostics(); len(got) != 0 {
		t.Fatalf("unchecked diagnostics = %#v", got)
	}
	if l.ReconstructionQuality().State != "not_checked" {
		t.Fatal("diagnostic access triggered inspection")
	}
	l.InspectProviderResponses()
	q := l.ReconstructionQuality()
	if q.State != "complete" || q.Responses != 0 || q.Diagnostics != 0 {
		t.Fatalf("zero-response inspection = %+v", q)
	}
}

func TestReconstructionDiagnosticsReturnsDetachedMetadata(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	tests := []struct {
		name        string
		source      string
		wantQuality ReconstructionQuality
		wantCodes   []string
		wantStarts  []int
	}{
		{
			name:        "complete",
			source:      head + "a: {\"ok\":1}\n",
			wantQuality: ReconstructionQuality{State: "complete", Responses: 1},
		},
		{
			name:        "partial recovered",
			source:      head + "a: {\"ok\":1}\n" + head + "b: {\"broken\":]}\n",
			wantQuality: ReconstructionQuality{State: "partial", Responses: 1, Diagnostics: 1, Code: "reconstruction_partial"},
			wantCodes:   []string{"delimiter_mismatch"},
			wantStarts:  []int{2},
		},
		{
			name:        "failed ambiguous",
			source:      head + "a: {\"pending\":\n" + head + ": {\"unknown\":1}\nordinary tail\n",
			wantQuality: ReconstructionQuality{State: "failed", Diagnostics: 2, Code: "reconstruction_failed"},
			wantCodes:   []string{"ambiguous_ownership", "ambiguous_ownership"},
			wantStarts:  []int{1, 2},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := loadResponseLog(t, tc.source)
			l.InspectProviderResponses()
			if got := l.ReconstructionQuality(); got != tc.wantQuality {
				t.Fatalf("quality = %+v, want %+v", got, tc.wantQuality)
			}
			diagnostics := l.ReconstructionDiagnostics()
			if len(diagnostics) != len(tc.wantCodes) {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
			for i, diagnostic := range diagnostics {
				if diagnostic.Code != tc.wantCodes[i] || diagnostic.StartLine != tc.wantStarts[i] || diagnostic.Ranges != nil || diagnostic.Unavailable != nil {
					t.Errorf("diagnostic %d = %#v", i, diagnostic)
				}
			}
			if len(diagnostics) == 0 {
				return
			}

			diagnostics[0].Code = "changed"
			diagnostics[0].Line = 999
			diagnostics[0].StartLine = 999
			diagnostics[0].Ranges = append(diagnostics[0].Ranges, logfmt.JSONFragment{Start: 1, End: 2})
			diagnostics[0].Unavailable = append(diagnostics[0].Unavailable, logfmt.JSONFragment{Start: 2, End: 3})
			diagnostics = append(diagnostics, logfmt.ProviderJSONDiagnostic{Code: "appended"})

			repeated := l.ReconstructionDiagnostics()
			if len(repeated) != len(tc.wantCodes) || repeated[0].Code != tc.wantCodes[0] || repeated[0].StartLine != tc.wantStarts[0] || repeated[0].Ranges != nil || repeated[0].Unavailable != nil {
				t.Fatalf("stored diagnostics mutated: %#v", repeated)
			}
		})
	}
}

func TestProviderResponseAtSelectsRecoveredOutcomeByPhysicalLine(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"event","@timestamp":"2026-09-11T00:00:00Z","type":"apply_complete"}`
	type responseLineExpectation struct {
		line  uint64
		state string
		body  string
		code  string
	}
	tests := []struct {
		name   string
		source string
		want   []responseLineExpectation
	}{
		{
			name:   "good A bad B good A",
			source: head + "a: {\"a\":1}\n" + head + "b: {\"b\":]}\n" + head + "a: {\"a\":2}\n",
			want:   []responseLineExpectation{{1, "complete", `{"a":1}`, ""}, {2, "invalid", "", "delimiter_mismatch"}, {3, "complete", `{"a":2}`, ""}},
		},
		{
			name:   "pending A bad B finish A",
			source: head + "a: {\"a\":" + "\n" + head + "b: {\"b\":]}\n" + head + "a: 1}\n",
			want:   []responseLineExpectation{{1, "complete", `{"a":1}`, ""}, {2, "invalid", "", "delimiter_mismatch"}, {3, "complete", `{"a":1}`, ""}},
		},
		{
			name:   "complete A incomplete A at EOF",
			source: head + "a: {\"a\":1}\n" + head + "a: {\"a\":" + "\n",
			want:   []responseLineExpectation{{1, "complete", `{"a":1}`, ""}, {2, "invalid", "", "incomplete"}},
		},
		{
			name:   "bad A apparent A good B",
			source: head + "a: {\"a\":]}\n" + head + "a: {\"apparent\":1}\n" + head + "b: {\"b\":1}\n",
			want:   []responseLineExpectation{{1, "invalid", "", "delimiter_mismatch"}, {2, "unavailable", "", "delimiter_mismatch"}, {3, "complete", `{"b":1}`, ""}},
		},
		{
			name:   "invalid UTF-8",
			source: head + "a: {\"a\":\"\xff\"}\n" + head + "b: {\"b\":1}\n",
			want:   []responseLineExpectation{{1, "invalid", "", "invalid_utf8"}, {2, "complete", `{"b":1}`, ""}},
		},
		{
			name:   "inline UI-only line between A fragments",
			source: head + "a: {\"a\":\"x\n" + ui + "\n" + head + "a: y\"}\n",
			want:   []responseLineExpectation{{1, "complete", `{"a":"xy"}`, ""}, {2, "none", "", ""}, {3, "complete", `{"a":"xy"}`, ""}},
		},
		{
			name:   "global ownership trigger",
			source: head + "done: {\"done\":1}\n" + head + "a: {\"a\":" + "\n" + "2026-09-11T00:00:00.000Z [DEBUG] provider.: {}\n" + head + "b: {\"later\":1}\n",
			want:   []responseLineExpectation{{1, "complete", `{"done":1}`, ""}, {2, "invalid", "", "ambiguous_ownership"}, {3, "unavailable", "", "ambiguous_ownership"}, {4, "unavailable", "", "ambiguous_ownership"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := loadResponseLog(t, tc.source)
			outcome := logfmt.InspectProviderJSON(tc.source)
			data := append([]byte(nil), l.Data...)
			entries := append([]logfmt.Entry(nil), l.Entries...)
			for _, want := range tc.want {
				got := providerResponseAtSourceLine(t, l, want.line)
				if got.State != want.state || got.SourceLine != want.line || got.Response.Text != want.body || diagnosticCode(got.Diagnostic) != want.code || got.HasDiagnostics != (len(outcome.Diagnostics) != 0) {
					t.Errorf("line %d: selection = %+v, diagnostic = %+v", want.line, got, got.Diagnostic)
				}
				if want.state == "invalid" || want.state == "unavailable" {
					wantDiagnostic, ok := projectedDiagnosticAtSourceLine(outcome.Diagnostics, want.line, want.state == "unavailable")
					if !ok || got.Diagnostic == nil || !reflect.DeepEqual(*got.Diagnostic, wantDiagnostic) || got.Diagnostic.Error() != wantDiagnostic.Error() {
						t.Errorf("line %d: projected diagnostic = %#v, want %#v", want.line, got.Diagnostic, wantDiagnostic)
					}
				}
			}
			if !reflect.DeepEqual(l.Data, data) || !reflect.DeepEqual(l.Entries, entries) || !reflect.DeepEqual(l.responseDiagnostics, outcome.Diagnostics) {
				t.Fatal("source or entries changed")
			}
		})
	}
}

func TestProviderResponseAtDoesNotClaimOrdinaryOrEmptyPhysicalLines(t *testing.T) {
	const providerHead = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	const coreHead = "2026-09-11T00:00:00.000Z [DEBUG] terraform: "
	source := providerHead + "a: {\"bad\":]}\r\n" +
		providerHead + "a: {\"apparent\":1}\r\n" +
		coreHead + "ordinary\r\n" +
		"ordinary continuation\r\n" +
		"\r\n"
	l := loadResponseLog(t, source)
	for _, line := range []uint64{3, 4, 5} {
		got := providerResponseAtSourceLine(t, l, line)
		if got.State != "none" || got.SourceLine != line || got.Diagnostic != nil {
			t.Errorf("line %d: selection = %+v", line, got)
		}
	}
}

func TestProviderResponseAtHandlesCRLFAndFinalCR(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider.a: "
	source := head + `{"one":1}` + "\r\n\r\n" + head + `{"two":2}` + "\r"
	l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 3}}}
	wants := []struct {
		state, body string
	}{{"complete", `{"one":1}`}, {"none", ""}, {"complete", `{"two":2}`}}
	for offset, want := range wants {
		got := l.ProviderResponseAt(0, offset)
		if got.State != want.state || got.SourceLine != uint64(offset+1) || got.Response.Text != want.body {
			t.Errorf("offset %d: selection = %+v", offset, got)
		}
	}
}

func TestProviderResponseAtValidBlankLineTriggersInspection(t *testing.T) {
	l := &Log{Data: []byte("\r\n"), Entries: []logfmt.Entry{{Len: 2, Lines: 1}}}
	got := l.ProviderResponseAt(0, 0)
	if got.State != "none" || got.SourceLine != 1 || got.Response.Text != "" || got.Diagnostic != nil || got.HasDiagnostics {
		t.Fatalf("selection = %+v", got)
	}
	if quality := l.ReconstructionQuality(); quality != (ReconstructionQuality{State: "complete"}) {
		t.Fatalf("quality = %+v", quality)
	}
}

func TestProviderResponseAtUsesUnsaturatedPhysicalLineIndex(t *testing.T) {
	var source strings.Builder
	source.WriteString("2026-09-11T00:00:00.000Z [INFO] terraform: ordinary\n")
	for range 65_536 {
		source.WriteString("continuation\n")
	}
	l := loadResponseLog(t, source.String())
	if len(l.Entries) != 1 || l.Entries[0].Lines != ^uint16(0) {
		t.Fatalf("entry index = %#v", l.Entries)
	}
	got := l.ProviderResponseAt(0, 65_536)
	if got.State != "none" || got.SourceLine != 65_537 {
		t.Fatalf("selection = %+v", got)
	}
}

func TestProviderResponseAtReturnsDetachedCachedValues(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	source := head + "good: {\"ok\":1}\n" + head + "bad: {\"broken\":]}\n" + head + "bad: ordinary\n" + head + "other: {\"ok\":2}\n"
	l := loadResponseLog(t, source)
	complete := providerResponseAtSourceLine(t, l, 1)
	complete.Response.Fragments[0] = logfmt.JSONFragment{Start: 99, End: 100, Line: 99}
	if repeated := providerResponseAtSourceLine(t, l, 1); repeated.Response.Text != `{"ok":1}` || repeated.Response.Fragments[0] == complete.Response.Fragments[0] {
		t.Fatalf("cached response mutated: %+v", repeated)
	}

	outcome := logfmt.InspectProviderJSON(source)
	if len(outcome.Diagnostics) != 1 {
		t.Fatalf("fixture diagnostics = %#v", outcome.Diagnostics)
	}
	wantDiagnostic := outcome.Diagnostics[0]
	wantProjected := wantDiagnostic
	wantProjected.Ranges, wantProjected.Unavailable = nil, nil
	for _, line := range []uint64{2, 3} {
		got := providerResponseAtSourceLine(t, l, line)
		if got.Diagnostic == nil || !reflect.DeepEqual(*got.Diagnostic, wantProjected) || got.Diagnostic.Error() != wantDiagnostic.Error() {
			t.Fatalf("line %d: diagnostic = %#v, want %#v", line, got.Diagnostic, wantProjected)
		}
		got.Diagnostic.Code = "changed"
		got.Diagnostic.Line = 999
		got.Diagnostic.FragmentCount = 999
		got.Diagnostic.Ranges = []logfmt.JSONFragment{{Start: 1, End: 2}}
		got.Diagnostic.Unavailable = []logfmt.JSONFragment{{Start: 2, End: 3}}
		repeated := providerResponseAtSourceLine(t, l, line)
		if repeated.Diagnostic == nil || !reflect.DeepEqual(*repeated.Diagnostic, wantProjected) {
			t.Fatalf("line %d: cached diagnostic mutated: %#v", line, repeated.Diagnostic)
		}
	}
	if !reflect.DeepEqual(l.responseDiagnostics, outcome.Diagnostics) {
		t.Fatalf("cached diagnostics changed: %#v", l.responseDiagnostics)
	}
	if got := l.ReconstructionQuality(); got != (ReconstructionQuality{State: "partial", Responses: 2, Diagnostics: 1, Code: "reconstruction_partial"}) {
		t.Fatalf("selection mutation changed quality counts: %+v", got)
	}
}

func TestProviderResponseAtDetachesGlobalDiagnosticPositions(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	source := head + "a: {\"pending\":" + "\n" + head + ": {\"unknown\":1}\nordinary tail\n"
	l := loadResponseLog(t, source)
	outcome := logfmt.InspectProviderJSON(source)
	if len(outcome.Diagnostics) != 2 {
		t.Fatalf("fixture diagnostics = %#v", outcome.Diagnostics)
	}
	for _, tc := range []struct {
		line            uint64
		state           string
		diagnosticIndex int
	}{{1, "invalid", 0}, {2, "unavailable", 1}, {3, "unavailable", 1}} {
		got := providerResponseAtSourceLine(t, l, tc.line)
		want := outcome.Diagnostics[tc.diagnosticIndex]
		want.Ranges, want.Unavailable = nil, nil
		if got.State != tc.state || got.Diagnostic == nil || !reflect.DeepEqual(*got.Diagnostic, want) {
			t.Errorf("line %d: selection = %+v, diagnostic = %#v, want %#v", tc.line, got, got.Diagnostic, want)
			continue
		}
		got.Diagnostic.Code = "changed"
		got.Diagnostic.Line = 999
		got.Diagnostic.JoinedBytes = 999
		got.Diagnostic.Ranges = []logfmt.JSONFragment{{Start: 1, End: 2}}
		got.Diagnostic.Unavailable = []logfmt.JSONFragment{{Start: 2, End: 3}}
		repeated := providerResponseAtSourceLine(t, l, tc.line)
		if repeated.Diagnostic == nil || !reflect.DeepEqual(*repeated.Diagnostic, want) {
			t.Errorf("line %d: cached global diagnostic mutated: %#v", tc.line, repeated.Diagnostic)
		}
	}
	if !reflect.DeepEqual(l.responseDiagnostics, outcome.Diagnostics) {
		t.Fatalf("cached global diagnostics changed: %#v", l.responseDiagnostics)
	}
	if got := l.ReconstructionQuality(); got != (ReconstructionQuality{State: "failed", Diagnostics: 2, Code: "reconstruction_failed"}) {
		t.Fatalf("quality did not count diagnostic records: %+v", got)
	}
}

func TestProviderResponseAtPublishesOutcomeQuality(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	for _, tc := range []struct {
		name, source, state string
		wantQuality         ReconstructionQuality
	}{
		{"mixed", head + "a: {\"ok\":1}\n" + head + "b: {\"broken\":]}\n", "complete", ReconstructionQuality{State: "partial", Responses: 1, Diagnostics: 1, Code: "reconstruction_partial"}},
		{"malformed only", head + "b: {\"broken\":]}\n", "invalid", ReconstructionQuality{State: "failed", Diagnostics: 1, Code: "reconstruction_failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &Log{Data: []byte(tc.source), Entries: []logfmt.Entry{{Len: uint32(len(tc.source)), Lines: uint16(strings.Count(tc.source, "\n"))}}}
			if got := l.ProviderResponseAt(0, 0); got.State != tc.state || !got.HasDiagnostics {
				t.Fatalf("selection = %+v", got)
			}
			if got := l.ReconstructionQuality(); got != tc.wantQuality {
				t.Fatalf("quality = %+v", got)
			}
		})
	}
}

func TestProviderResponseAtPublishesCacheAtomically(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider."
	for _, tc := range []struct {
		source string
		want   ReconstructionQuality
	}{
		{head + "a: {\"ok\":1}\n" + head + "b: {\"broken\":]}\n", ReconstructionQuality{State: "partial", Responses: 1, Diagnostics: 1, Code: "reconstruction_partial"}},
		{head + "b: {\"broken\":]}\n", ReconstructionQuality{State: "failed", Diagnostics: 1, Code: "reconstruction_failed"}},
	} {
		l := &Log{Data: []byte(tc.source), Entries: []logfmt.Entry{{Len: uint32(len(tc.source)), Lines: uint16(strings.Count(tc.source, "\n"))}}}
		if got := l.ReconstructionQuality(); got != (ReconstructionQuality{State: "not_checked"}) {
			t.Fatalf("initial quality = %+v", got)
		}
		if got := l.ReconstructionDiagnostics(); got != nil {
			t.Fatalf("initial diagnostics = %#v", got)
		}
		start := make(chan struct{})
		errs := make(chan error, 96)
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				<-start
				l.InspectProviderResponses()
			})
			wg.Go(func() {
				<-start
				for range 100 {
					got := l.ReconstructionQuality()
					if got != (ReconstructionQuality{State: "not_checked"}) && got != tc.want {
						errs <- fmt.Errorf("quality = %+v", got)
						return
					}
				}
			})
			wg.Go(func() {
				<-start
				for range 100 {
					diagnostics := l.ReconstructionDiagnostics()
					if len(diagnostics) == 0 {
						continue
					}
					if len(diagnostics) != tc.want.Diagnostics {
						errs <- fmt.Errorf("diagnostics = %#v", diagnostics)
						return
					}
					for _, diagnostic := range diagnostics {
						if diagnostic.Code == "" || diagnostic.Ranges != nil || diagnostic.Unavailable != nil {
							errs <- fmt.Errorf("partially published diagnostic = %#v", diagnostic)
							return
						}
					}
				}
			})
			wg.Go(func() {
				<-start
				for range 100 {
					got := l.ProviderResponseAt(0, 0)
					if got.State != "complete" && got.State != "invalid" {
						errs <- fmt.Errorf("selection = %+v", got)
						return
					}
				}
			})
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if got := l.ReconstructionQuality(); got != tc.want {
			t.Fatalf("final quality = %+v", got)
		}
		if got := l.ReconstructionDiagnostics(); len(got) != tc.want.Diagnostics {
			t.Fatalf("final diagnostics = %#v", got)
		}
	}
}

func TestProviderResponseAtConcurrentFirstSelectionsAreIndependent(t *testing.T) {
	const head = "2026-09-11T00:00:00.000Z [DEBUG] provider.a: "
	source := head + "{\"value\":" + "\n" + head + "1}\n"
	l := &Log{Data: []byte(source), Entries: []logfmt.Entry{{Len: uint32(len(source)), Lines: 2}}}
	start := make(chan struct{})
	results := make(chan ProviderResponseSelection, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			results <- l.ProviderResponseAt(0, 1)
		})
	}
	close(start)
	wg.Wait()
	close(results)

	selections := make([]ProviderResponseSelection, 0, 32)
	for got := range results {
		if got.State != "complete" || got.SourceLine != 2 || got.Response.Text != `{"value":1}` || len(got.Response.Fragments) != 2 {
			t.Fatalf("selection = %+v", got)
		}
		selections = append(selections, got)
	}
	selections[0].Response.Fragments[0] = logfmt.JSONFragment{Start: 99, End: 100, Line: 99}
	for i := 1; i < len(selections); i++ {
		if selections[i].Response.Fragments[0] == selections[0].Response.Fragments[0] {
			t.Fatalf("selection %d shares mutable fragments", i)
		}
	}
	if repeated := l.ProviderResponseAt(0, 1); repeated.Response.Fragments[0] == selections[0].Response.Fragments[0] {
		t.Fatalf("cached response mutated: %+v", repeated)
	}
}

func loadResponseLog(t testing.TB, source string) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "responses.log")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func providerResponseAtSourceLine(t *testing.T, l *Log, line uint64) ProviderResponseSelection {
	t.Helper()
	for entry := range l.Entries {
		location, ok := l.SourceLocation(uint32(entry))
		if ok && line >= location.StartLine && line <= location.EndLine {
			return l.ProviderResponseAt(uint32(entry), int(line-location.StartLine))
		}
	}
	t.Fatalf("source line %d has no entry", line)
	return ProviderResponseSelection{}
}

func diagnosticCode(d *logfmt.ProviderJSONDiagnostic) string {
	if d == nil {
		return ""
	}
	return d.Code
}

func projectedDiagnosticAtSourceLine(diagnostics []logfmt.ProviderJSONDiagnostic, line uint64, unavailable bool) (logfmt.ProviderJSONDiagnostic, bool) {
	for _, diagnostic := range diagnostics {
		fragments := diagnostic.Ranges
		if unavailable {
			fragments = diagnostic.Unavailable
		}
		for _, fragment := range fragments {
			if fragment.Line == int(line) && fragment.Start != fragment.End {
				diagnostic.Ranges = nil
				diagnostic.Unavailable = nil
				return diagnostic, true
			}
		}
	}
	return logfmt.ProviderJSONDiagnostic{}, false
}

func TestProviderResponseAtPreservesSourceEntries(t *testing.T) {
	const head = "2026-01-01T00:00:00.000Z [DEBUG] provider.example: "
	const source = head + `{"value":"fir` + "\n" + "ordinary text\n" + head + `st"}` + "\n"
	path := filepath.Join(t.TempDir(), "synthetic.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := append(l.Entries[:0:0], l.Entries...)
	var wg sync.WaitGroup
	for i, e := range l.Entries {
		wg.Go(func() {
			selection := l.ProviderResponseAt(uint32(i), 0)
			if selection.State != "complete" || selection.Response.Text != `{"value":"firordinary textst"}` || len(selection.Response.Fragments) != 3 {
				t.Errorf("selection = %+v", selection)
			}
			if !strings.Contains(source, string(l.Bytes(e))) {
				t.Error("source bytes changed")
			}
		})
	}
	wg.Wait()
	if string(l.Data) != source || !reflect.DeepEqual(entries, l.Entries) {
		t.Fatal("source changed")
	}
}

func TestProviderResponseAtMalformedDoesNotBlockLoad(t *testing.T) {
	const source = "2026-01-01T00:00:00.000Z [DEBUG] provider.example: {\"secret\":\n"
	path := filepath.Join(t.TempDir(), "synthetic.log")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	selection := l.ProviderResponseAt(0, 0)
	if selection.State != "invalid" || selection.Diagnostic == nil || strings.Contains(selection.Diagnostic.Error(), "secret") || selection.Response.Text != "" {
		t.Fatalf("selection = %+v", selection)
	}
	if string(l.Bytes(l.Entries[0])) != source {
		t.Fatal("raw entry unavailable")
	}
}
