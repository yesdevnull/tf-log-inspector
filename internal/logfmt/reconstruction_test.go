package logfmt

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestInspectProviderJSONRecovery(t *testing.T) {
	join := func(lines ...string) string { return strings.Join(lines, "\n") }
	cases := []struct {
		name, input string
		want        []string
		codes       []string
	}{
		{"independent after failure", join(providerRecord("a", `{"a":1}`), providerRecord("b", `{"b":]}`), providerRecord("a", `{"a":2}`)), []string{`{"a":1}`, `{"a":2}`}, []string{"delimiter_mismatch"}},
		{"pending independent", join(providerRecord("a", `{"a":`), providerRecord("b", `{"b":]}`), providerRecord("a", `1}`)), []string{`{"a":1}`}, []string{"delimiter_mismatch"}},
		{"no restart", join(providerRecord("a", `{"a":]}`), providerRecord("a", `{"false-restart":1}`), providerRecord("b", `{"b":1}`)), []string{`{"b":1}`}, []string{"delimiter_mismatch"}},
		{"complete then EOF", join(providerRecord("a", `{"a":1}`), providerRecord("a", `{"a":`)), []string{`{"a":1}`}, []string{"incomplete"}},
		{"invalid UTF8", join(providerRecord("a", "{\"a\":\"\xff\"}"), providerRecord("b", `{"b":1}`)), []string{`{"b":1}`}, []string{"invalid_utf8"}},
		{"global stop", join(providerRecord("a", `{"a":1}`), providerRecord("", `{"unknown":1}`), providerRecord("b", `{"b":1}`)), []string{`{"a":1}`}, []string{"ambiguous_ownership"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InspectProviderJSON(tc.input)
			bodies, codes := []string{}, []string{}
			for _, m := range got.Messages {
				bodies = append(bodies, m.Text)
				var joined strings.Builder
				for _, r := range m.Fragments {
					if r.Start < 0 || r.End < r.Start || r.End > len(tc.input) {
						t.Fatalf("invalid source range: %#v", r)
					}
					joined.WriteString(tc.input[r.Start:r.End])
				}
				if joined.String() != m.Text {
					t.Fatal("source mapping changed")
				}
			}
			for _, d := range got.Diagnostics {
				codes = append(codes, d.Code)
			}
			if !reflect.DeepEqual(bodies, tc.want) || !reflect.DeepEqual(codes, tc.codes) {
				t.Fatalf("bodies=%q codes=%q", bodies, codes)
			}
			if strict, err := ReconstructProviderJSON(tc.input); strict != nil || err == nil {
				t.Fatal("strict consumer accepted diagnostics")
			}
		})
	}
}

func TestInspectProviderJSONStructuralFailureRanges(t *testing.T) {
	cases := []struct {
		name, payload, code string
		end, consumed       int
	}{
		{"delimiter", `{"a":]tail`, "delimiter_mismatch", 55, 6},
		{"suffix", `{"a":1}tail`, "suffix_grammar", 56, 7},
		{"inline UI", `{"a":"x{"@module":false}tail`, "invalid_inline_ui", 73, 8},
	}
	for _, tc := range cases {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(tc.name+"/"+fmt.Sprintf("%q", ending), func(t *testing.T) {
				input := providerRecord("a", tc.payload) + ending
				result := InspectProviderJSON(input)
				if len(result.Messages) != 0 || len(result.Diagnostics) != 1 {
					t.Fatalf("outcome: %#v", result)
				}
				d := result.Diagnostics[0]
				wantRanges := []JSONFragment{{Start: 45, End: tc.end, Line: 1}}
				if d.Code != tc.code || d.Line != 1 || d.StartLine != 1 || !reflect.DeepEqual(d.Ranges, wantRanges) || len(d.Unavailable) != 0 {
					t.Fatalf("diagnostic boundaries: %#v", d)
				}
				if d.FragmentCount != 1 || d.JoinedBytes != tc.consumed || d.FirstFragmentBytes != tc.consumed || d.LastFragmentBytes != tc.consumed {
					t.Fatalf("consumed counts: %#v", d)
				}
				if tc.end-45 <= d.LastFragmentBytes {
					t.Fatal("fixture must include unconsumed trailing bytes")
				}
				if input[45:tc.end] != tc.payload {
					t.Fatal("literal fixture boundary changed")
				}
			})
		}
	}
}

func TestInspectProviderJSONGlobalDiagnosticShape(t *testing.T) {
	const head = "2026-09-08T00:00:00.000Z [DEBUG] "
	lines := []string{head + `provider.a: {"a":`, head + `provider.b: {"b":`, head + `provider.: {}`, head + `provider.c: {"ok":1}`, head + `terraform: ordinary`}
	cases := []struct {
		name, input string
		want        []ProviderJSONDiagnostic
	}{
		{"lone trigger EOF", lines[2], []ProviderJSONDiagnostic{{Code: "ambiguous_ownership", Line: 1, StartLine: 1, Unavailable: []JSONFragment{{Start: 0, End: 46, Line: 1}}}}},
		{"LF pending streams", strings.Join(lines, "\n") + "\n", []ProviderJSONDiagnostic{
			{Code: "ambiguous_ownership", Line: 3, StartLine: 1, FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5, Ranges: []JSONFragment{{Start: 45, End: 50, Line: 1}}},
			{Code: "ambiguous_ownership", Line: 3, StartLine: 2, FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5, Ranges: []JSONFragment{{Start: 96, End: 101, Line: 2}}},
			{Code: "ambiguous_ownership", Line: 3, StartLine: 3, Unavailable: []JSONFragment{{Start: 102, End: 148, Line: 3}, {Start: 149, End: 202, Line: 4}, {Start: 203, End: 255, Line: 5}}},
		}},
		{"CRLF pending streams", strings.Join(lines, "\r\n") + "\r\n", []ProviderJSONDiagnostic{
			{Code: "ambiguous_ownership", Line: 3, StartLine: 1, FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5, Ranges: []JSONFragment{{Start: 45, End: 50, Line: 1}}},
			{Code: "ambiguous_ownership", Line: 3, StartLine: 2, FragmentCount: 1, JoinedBytes: 5, FirstFragmentBytes: 5, LastFragmentBytes: 5, Ranges: []JSONFragment{{Start: 97, End: 102, Line: 2}}},
			{Code: "ambiguous_ownership", Line: 3, StartLine: 3, Unavailable: []JSONFragment{{Start: 104, End: 150, Line: 3}, {Start: 152, End: 205, Line: 4}, {Start: 207, End: 259, Line: 5}}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := InspectProviderJSON(tc.input)
			if len(result.Messages) != 0 || len(result.Diagnostics) != len(tc.want) {
				t.Fatalf("outcome: %#v", result)
			}
			for i, d := range result.Diagnostics {
				if len(d.Ranges) == 0 {
					d.Ranges = nil
				}
				if len(d.Unavailable) == 0 {
					d.Unavailable = nil
				}
				if !reflect.DeepEqual(d, tc.want[i]) {
					t.Fatalf("diagnostic %d: got %#v, want %#v", i, d, tc.want[i])
				}
			}
		})
	}
}

func TestInspectProviderJSONRangesAndOrdering(t *testing.T) {
	input := providerRecord("a", `{"a":`) + "\n" +
		providerRecord("b", `{"b":1}`) + "\n" +
		providerRecord("c", `{"c":]}`) + "\n" +
		providerRecord("a", `1}`) + "\n" +
		providerRecord("c", "ordinary") + "\n" +
		"c continuation\n" + providerRecord("c", `{"restart":1}`) + "\n" +
		"2026-09-08T00:00:00.000Z [DEBUG] terraform: ordinary\nordinary continuation"
	want := InspectProviderJSON(input)
	if len(want.Messages) != 2 || want.Messages[0].Text != `{"a":1}` || want.Messages[1].Text != `{"b":1}` {
		t.Fatalf("messages: %#v", want.Messages)
	}
	if len(want.Diagnostics) != 1 || want.Diagnostics[0].Code != "delimiter_mismatch" || len(want.Diagnostics[0].Unavailable) != 3 {
		t.Fatalf("diagnostic: %#v", want.Diagnostics)
	}
	if got := []string{input[want.Messages[0].Fragments[0].Start:want.Messages[0].Fragments[0].End], input[want.Messages[0].Fragments[1].Start:want.Messages[0].Fragments[1].End], input[want.Messages[1].Fragments[0].Start:want.Messages[1].Fragments[0].End]}; !reflect.DeepEqual(got, []string{`{"a":`, `1}`, `{"b":1}`}) {
		t.Fatalf("complete payload slices: %q", got)
	}
	wantUnavailable := []string{"ordinary", "c continuation", `{"restart":1}`}
	for i, r := range want.Diagnostics[0].Unavailable {
		if input[r.Start:r.End] != wantUnavailable[i] {
			t.Fatalf("unavailable %d = %q", i, input[r.Start:r.End])
		}
		for _, message := range want.Messages {
			for _, fragment := range message.Fragments {
				if r.Start < fragment.End && fragment.Start < r.End {
					t.Fatalf("unavailable range overlaps complete fragment: %#v and %#v", r, fragment)
				}
			}
		}
	}
	for _, r := range want.Diagnostics[0].Unavailable {
		if r.Line < 5 || r.Line > 7 {
			t.Fatalf("unavailable range escaped quarantined entry: %#v", r)
		}
	}
	if got := InspectProviderJSON(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("inspection is nondeterministic: %#v", got)
	}
}

func TestInspectProviderJSONGlobalStopMalformedOwners(t *testing.T) {
	const head = "2026-09-08T00:00:00.000Z [DEBUG] "
	for _, malformed := range []string{`provider.`, `provider.: {}`, `provider.a missing-colon`, `provider.a bad: {}`, `provider.a{}`, `provider.a[]`, `provider.a"message"`, `provider.a=value`} {
		t.Run(malformed, func(t *testing.T) {
			lines := []string{head + `provider.done: {"done":1}`, head + `provider.a: {"a":`, head + `provider.b: {"b":`, head + malformed, head + `provider.c: {"later":1}`, head + `terraform: ordinary`}
			input := strings.Join(lines, "\n")
			got := InspectProviderJSON(input)
			if len(got.Messages) != 1 || got.Messages[0].Text != `{"done":1}` || len(got.Diagnostics) != 3 {
				t.Fatalf("outcome: %#v", got)
			}
			verified := got.Messages[0].Fragments[0]
			if input[verified.Start:verified.End] != `{"done":1}` {
				t.Fatalf("verified source fragment: %#v", verified)
			}
			if got.Diagnostics[0].StartLine != 2 || got.Diagnostics[1].StartLine != 3 || got.Diagnostics[2].StartLine != 4 {
				t.Fatalf("diagnostic order: %#v", got.Diagnostics)
			}
			for i := 0; i < 2; i++ {
				if got.Diagnostics[i].Code != "ambiguous_ownership" || len(got.Diagnostics[i].Ranges) != 1 || len(got.Diagnostics[i].Unavailable) != 0 {
					t.Fatalf("aborted diagnostic %d: %#v", i, got.Diagnostics[i])
				}
				if input[got.Diagnostics[i].Ranges[0].Start:got.Diagnostics[i].Ranges[0].End] != []string{`{"a":`, `{"b":`}[i] {
					t.Fatalf("aborted range %d: %#v", i, got.Diagnostics[i].Ranges[0])
				}
			}
			trigger := got.Diagnostics[2]
			if trigger.Code != "ambiguous_ownership" || len(trigger.Ranges) != 0 || len(trigger.Unavailable) != 3 {
				t.Fatalf("trigger diagnostic: %#v", trigger)
			}
			for i, r := range trigger.Unavailable {
				if input[r.Start:r.End] != lines[i+3] {
					t.Fatalf("global tail %d = %q", i, input[r.Start:r.End])
				}
			}
		})
	}
}

func TestInspectProviderJSONEmptyProviderRecords(t *testing.T) {
	emptyA := strings.TrimSuffix(providerRecord("a", ""), ": ")
	emptyB := strings.TrimSuffix(providerRecord("b", ""), ": ")
	for _, tc := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{"empty request records", []string{emptyA, emptyA}, nil},
		{"long provider identifier", []string{"2026-09-08T00:00:00.000Z [DEBUG] provider.terraform-provider-" + strings.Repeat("a", 64) + "_v1.2.3_x5"}, nil},
		{"split string", []string{providerRecord("a", `{"value":"first`), emptyA, emptyA, providerRecord("a", `last"}`)}, []string{`{"value":"firstlast"}`}},
		{"interleaved pending owners", []string{providerRecord("a", `{"a":`), providerRecord("b", `{"b":`), emptyB, `2}`, providerRecord("a", `1}`)}, []string{`{"a":1}`, `{"b":2}`}},
		{"unrelated continuation", []string{providerRecord("a", `{"a":`), emptyB, "ordinary continuation", providerRecord("a", `1}`)}, []string{`{"a":1}`}},
	} {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(tc.name+fmt.Sprintf("/%q", ending), func(t *testing.T) {
				input := strings.Join(tc.lines, ending)
				got, err := ReconstructProviderJSON(input)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(tc.want) {
					t.Fatalf("messages = %d, want %d", len(got), len(tc.want))
				}
				for i, message := range got {
					var joined strings.Builder
					for _, fragment := range message.Fragments {
						joined.WriteString(input[fragment.Start:fragment.End])
						lineStart := len(strings.Join(tc.lines[:fragment.Line-1], ending))
						if fragment.Line > 1 {
							lineStart += len(ending)
						}
						if fragment.Start < lineStart || fragment.End > lineStart+len(tc.lines[fragment.Line-1]) {
							t.Fatalf("fragment escaped physical line: %#v", fragment)
						}
						if fragment.Start == fragment.End && fragment.Start != lineStart+len(tc.lines[fragment.Line-1]) {
							t.Fatalf("empty fragment must follow its provider header: %#v", fragment)
						}
					}
					if message.Text != tc.want[i] || joined.String() != tc.want[i] {
						t.Fatalf("message %d = %q, source bytes = %q, want %q", i, message.Text, joined.String(), tc.want[i])
					}
				}
			})
		}
	}
}

func TestInspectProviderJSONEOFPendingOrderingAndRanges(t *testing.T) {
	lines := []string{providerRecord("damaged", `{"bad":]}`), providerRecord("a", `{"a":"`), providerRecord("b", `{"b":`), providerRecord("a", "tail"), providerRecord("b", "")}
	for _, tc := range []struct{ name, separator, suffix string }{{"trailing LF", "\n", "\n"}, {"blank final continuation", "\n", "\n\n"}, {"CRLF", "\r\n", "\r\n"}} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Join(lines, tc.separator) + tc.suffix
			got := InspectProviderJSON(input)
			if len(got.Messages) != 0 || len(got.Diagnostics) != 3 {
				t.Fatalf("outcome: %#v", got)
			}
			if got.Diagnostics[0].Code != "delimiter_mismatch" || got.Diagnostics[0].Line != 1 || got.Diagnostics[1].Code != "incomplete" || got.Diagnostics[1].StartLine != 2 || got.Diagnostics[2].Code != "incomplete" || got.Diagnostics[2].StartLine != 3 {
				t.Fatalf("diagnostic order: %#v", got.Diagnostics)
			}
			wantLines := [][]int{{1}, {2, 4}, {3, 5}}
			lastLine := 5
			if tc.name == "blank final continuation" {
				wantLines[2] = []int{3, 5, 6}
				lastLine = 6
			}
			wantCounts := []struct{ fragments, joined, first, last int }{{1, 8, 8, 8}, {2, 10, 6, 4}, {2, 5, 5, 0}}
			if tc.name == "blank final continuation" {
				wantCounts[2].fragments = 3
			}
			for i, d := range got.Diagnostics {
				if i > 0 && d.Line != lastLine {
					t.Fatalf("diagnostic %d EOF line = %d, want %d", i, d.Line, lastLine)
				}
				counts := wantCounts[i]
				if d.FragmentCount != counts.fragments || d.JoinedBytes != counts.joined || d.FirstFragmentBytes != counts.first || d.LastFragmentBytes != counts.last {
					t.Fatalf("diagnostic %d counts: %#v", i, d)
				}
				if len(d.Ranges) != len(wantLines[i]) {
					t.Fatalf("diagnostic %d ranges: %#v", i, d.Ranges)
				}
				for j, r := range d.Ranges {
					if r.Line != wantLines[i][j] || r.Start < 0 || r.End < r.Start || r.End > len(input) || strings.ContainsAny(input[r.Start:r.End], "\r\n") {
						t.Fatalf("diagnostic %d range %d: %#v", i, j, r)
					}
				}
			}
		})
	}
}

func TestInspectProviderJSONRecoveryPreservesInlineUIAndSplitUTF8(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"event","@timestamp":"2026-09-08T00:00:00Z","type":"apply_complete"}`
	for _, tc := range []struct{ name, first, last, want string }{{"inline UI", `{"a":"x` + ui, `y"}`, `{"a":"xy"}`}, {"split UTF-8", "{\"a\":\"\xc3", "\xa9\"}", `{"a":"é"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			input := providerRecord("a", tc.first) + "\n" + providerRecord("b", `{"bad":]}`) + "\n" + providerRecord("a", tc.last)
			got := InspectProviderJSON(input)
			if len(got.Messages) != 1 || got.Messages[0].Text != tc.want || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "delimiter_mismatch" {
				t.Fatalf("outcome: %#v", got)
			}
			var joined strings.Builder
			for _, r := range got.Messages[0].Fragments {
				joined.WriteString(input[r.Start:r.End])
				if strings.Contains(input[r.Start:r.End], ui) {
					t.Fatal("inline UI retained in source fragments")
				}
			}
			if joined.String() != tc.want {
				t.Fatalf("source fragments = %q", joined.String())
			}
		})
	}
}

func TestInspectProviderJSONInvalidInlineUIQuarantinesItsOwner(t *testing.T) {
	input := providerRecord("bad", `{"x":"a{"@module":false}`) + "\n" + providerRecord("bad", "ordinary") + "\ncontinuation\n" + providerRecord("good", `{"ok":1}`)
	got := InspectProviderJSON(input)
	if len(got.Messages) != 1 || got.Messages[0].Text != `{"ok":1}` || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "invalid_inline_ui" {
		t.Fatalf("outcome: %#v", got)
	}
	d := got.Diagnostics[0]
	if len(d.Ranges) != 1 || input[d.Ranges[0].Start:d.Ranges[0].End] != `{"x":"a{"@module":false}` || len(d.Unavailable) != 2 || input[d.Unavailable[0].Start:d.Unavailable[0].End] != "ordinary" || input[d.Unavailable[1].Start:d.Unavailable[1].End] != "continuation" {
		t.Fatalf("positions: %#v", d)
	}
}

func TestInspectProviderJSONRecoversRepeatedComponentMessages(t *testing.T) {
	input := providerRecord("a", `{"n":1}`) + "\n" + providerRecord("bad", `{"x":]}`) + "\n" + providerRecord("a", `{"n":2}`)
	got := InspectProviderJSON(input)
	if len(got.Messages) != 2 || got.Messages[0].Text != `{"n":1}` || got.Messages[1].Text != `{"n":2}` || len(got.Diagnostics) != 1 {
		t.Fatalf("outcome: %#v", got)
	}
	for _, m := range got.Messages {
		if len(m.Fragments) != 1 || input[m.Fragments[0].Start:m.Fragments[0].End] != m.Text {
			t.Fatalf("message range: %#v", m)
		}
	}
}

func TestInspectProviderJSONRecoversAfterEveryLocalFailure(t *testing.T) {
	cases := []struct{ name, payload, code string }{
		{"delimiter", `{"x":]}`, "delimiter_mismatch"},
		{"suffix", `{"x":1}tail`, "suffix_grammar"},
		{"syntax", `{"x":invalid}`, "json_syntax"},
		{"UTF-8", "{\"x\":\"\xff\"}", "invalid_utf8"},
		{"inline UI", `{"x":"a{"@module":false}`, "invalid_inline_ui"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := providerRecord("damaged", tc.payload) + "\n" + providerRecord("good", `{"ok":1}`)
			got := InspectProviderJSON(input)
			if len(got.Messages) != 1 || got.Messages[0].Text != `{"ok":1}` || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != tc.code {
				t.Fatalf("outcome: %#v", got)
			}
			if len(got.Diagnostics[0].Ranges) != 1 || len(got.Diagnostics[0].Unavailable) != 0 {
				t.Fatalf("failure positions: %#v", got.Diagnostics[0])
			}
		})
	}
}

func TestProviderJSONDiagnosticStartHandlesEmptyRanges(t *testing.T) {
	if got := providerJSONDiagnosticStart(ProviderJSONDiagnostic{}); got != 0 {
		t.Fatalf("empty diagnostic start = %d", got)
	}
	if got := providerJSONDiagnosticStart(ProviderJSONDiagnostic{Unavailable: []JSONFragment{{Start: 17, End: 17, Line: 2}}}); got != 17 {
		t.Fatalf("unavailable diagnostic start = %d", got)
	}
}

func TestInspectProviderJSONRetainsVerifiedPrefix(t *testing.T) {
	input := providerRecord("a", `{"ok":1}`) + "\n" +
		providerRecord("b", `{"private-token":]}`)
	result := InspectProviderJSON(input)
	if len(result.Messages) != 1 || result.Messages[0].Text != `{"ok":1}` {
		t.Fatalf("verified prefix lost: %#v", result.Messages)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "delimiter_mismatch" {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
	if got, err := ReconstructProviderJSON(input); err == nil || got != nil {
		t.Fatalf("strict reconstruction accepted partial result: %#v, %v", got, err)
	}
	if strings.Contains(result.Diagnostics[0].Error(), "private-token") {
		t.Fatal("source text in diagnostic")
	}
}

func TestInspectProviderJSONRetainsInterleavedMessageCompletedBeforeFailure(t *testing.T) {
	input := providerRecord("a", `{"value":`) + "\n" +
		providerRecord("b", `{"ok":1}`) + "\n" +
		providerRecord("a", `invalid}`)
	result := InspectProviderJSON(input)
	if len(result.Messages) != 1 || result.Messages[0].Text != `{"ok":1}` {
		t.Fatalf("verified interleaved message lost: %#v", result.Messages)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "json_syntax" {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
}

func TestInspectProviderJSONMatchesStrictCleanResults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"single", providerRecord("a", `{"ok":1}`)},
		{"interleaved", providerRecord("a", `{"a":`) + "\n" + providerRecord("b", `{"b":2}`) + "\n" + providerRecord("a", `1}`)},
		{"empty", ""},
		{"ordinary", "ordinary log line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			strict, err := ReconstructProviderJSON(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			result := InspectProviderJSON(tc.input)
			if !reflect.DeepEqual(result.Messages, strict) {
				t.Fatalf("messages differ: inspect %#v, strict %#v", result.Messages, strict)
			}
			if len(result.Diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics)
			}
			for _, message := range result.Messages {
				var joined strings.Builder
				for _, fragment := range message.Fragments {
					joined.WriteString(tc.input[fragment.Start:fragment.End])
				}
				if joined.String() != message.Text {
					t.Fatalf("source fragments = %q, want %q", joined.String(), message.Text)
				}
			}
		})
	}
}

func TestInspectProviderJSONProjectsFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, tail, code string
		joined, last     int
		offset           int64
		syntaxLine       int
	}{
		{"delimiter", `]`, "delimiter_mismatch", 11, 1, 11, 3},
		{"suffix", `1}garbage`, "suffix_grammar", 12, 2, 0, 0},
		{"syntax", `invalid}`, "json_syntax", 18, 8, 11, 3},
		{"utf8", "\"\xff\"}", "invalid_utf8", 14, 4, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "ordinary\n" + providerRecord("a", `{"secret":`) + "\n" + providerRecord("a", tc.tail)
			result := InspectProviderJSON(input)
			if len(result.Messages) != 0 || len(result.Diagnostics) != 1 {
				t.Fatalf("outcome: %#v", result)
			}
			d := result.Diagnostics[0]
			if d.Code != tc.code || d.Line != 3 || d.StartLine != 2 || d.FragmentCount != 2 || d.JoinedBytes != tc.joined || d.FirstFragmentBytes != 10 || d.LastFragmentBytes != tc.last || d.SyntaxOffset != tc.offset || d.SyntaxLine != tc.syntaxLine {
				t.Fatalf("diagnostic: %#v", d)
			}
			if len(d.Ranges) != 2 || d.Ranges[0].Line != 2 || d.Ranges[1].Line != 3 {
				t.Fatalf("ranges: %#v", d.Ranges)
			}
			if strings.Contains(d.Error(), "secret") || strings.Contains(d.Error(), "invalid}") || strings.Contains(d.Error(), "garbage") {
				t.Fatalf("source in diagnostic: %v", d)
			}
		})
	}
}

func TestInspectProviderJSONProjectsInlineUIAndIncompleteFailures(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"private-event"}`
	for _, tc := range []struct {
		name, input, code string
		line, fragments   int
	}{
		{"inline UI", providerRecord("a", `{"a":"x`+ui+`"}`), "invalid_inline_ui", 1, 1},
		{"incomplete", providerRecord("a", `{"a":`) + "\n" + providerRecord("a", ""), "incomplete", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := InspectProviderJSON(tc.input)
			if len(result.Messages) != 0 || len(result.Diagnostics) != 1 {
				t.Fatalf("outcome: %#v", result)
			}
			d := result.Diagnostics[0]
			if d.Code != tc.code || d.Line != tc.line || d.StartLine != 1 || d.FragmentCount != tc.fragments {
				t.Fatalf("diagnostic: %#v", d)
			}
			if len(d.Ranges) != tc.fragments {
				t.Fatalf("ranges: %#v", d.Ranges)
			}
			if strings.Contains(d.Error(), "private-event") {
				t.Fatalf("source in diagnostic: %v", d)
			}
		})
	}
}
