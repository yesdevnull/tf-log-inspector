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

func TestInspectProviderJSONQuarantinesOnlyFailedComponent(t *testing.T) {
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
	for _, r := range want.Diagnostics[0].Unavailable {
		if r.Line < 5 || r.Line > 7 {
			t.Fatalf("unavailable range escaped quarantined entry: %#v", r)
		}
	}
	if got := InspectProviderJSON(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("inspection is nondeterministic: %#v", got)
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
