package logfmt

import (
	"reflect"
	"strings"
	"testing"
)

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
