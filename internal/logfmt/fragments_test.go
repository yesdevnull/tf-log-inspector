package logfmt

import (
	"strings"
	"testing"
)

func providerRecord(comp, payload string) string {
	return "2026-09-08T00:00:00.000Z [DEBUG] provider." + comp + ": " + payload
}

func TestProviderJSONInterleaved(t *testing.T) {
	a := `{"body":"` + strings.Repeat("a", 65526) + `\" space é","nested":[true,null]}`
	b := `[{"body":"second"},false]`
	parts := []string{a[:65536], b[:11], a[65536:65539], b[11:], a[65539:]}
	comps := []string{"azure_x5", "azure_x6", "azure_x5", "azure_x6", "azure_x5"}
	var input strings.Builder
	for i, part := range parts {
		input.WriteString(providerRecord(comps[i], part) + "\n")
	}
	got, err := ReconstructProviderJSON(input.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Text != a || got[1].Text != b {
		t.Fatalf("incorrect reconstruction: %d messages", len(got))
	}
	for i, lines := range [][]int{{1, 3, 5}, {2, 4}} {
		var joined strings.Builder
		for j, f := range got[i].Fragments {
			if f.Line != lines[j] {
				t.Fatalf("fragment line = %d, want %d", f.Line, lines[j])
			}
			joined.WriteString(input.String()[f.Start:f.End])
		}
		if joined.String() != got[i].Text {
			t.Fatal("source ranges do not recover payload")
		}
	}
}

func TestProviderJSONBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"single", providerRecord("a", `{"a":1} tf_req_id=scope`), `{"a":1}`},
		{"pretty", providerRecord("a", "{\r\n") + "  \"a\": [true,\r\n false]\r\n}", `{  "a": [true, false]}`},
		{"nested initial", providerRecord("a", `2026/09/08 00:00:00 [DEBUG] {"a":1}`), `{"a":1}`},
		{"nested array", providerRecord("a", `2026/09/08 00:00:00 [1,2]`), `[1,2]`},
		{"nested fragmented array", providerRecord("a", `2026/09/08 00:00:00 [[1],`) + "\n" + providerRecord("a", `2]`), `[[1],2]`},
		{"literal arrays", providerRecord("a", `[true,false,null]`), `[true,false,null]`},
		{"quoted metadata", providerRecord("a", `{"a":1} tf_req_id="scope with \"quotes\"" tf_rpc=ReadResource`), `{"a":1}`},
		{"hclog metadata separator", providerRecord("a", `{"a":1}: timestamp="2026-09-08T00:00:00.000Z"`), `{"a":1}`},
		{"nested continuation", providerRecord("a", `{"a":"`) + "\n" + providerRecord("a", `2026/09/08 00:00:00 [DEBUG] text"}`), `{"a":"2026/09/08 00:00:00 [DEBUG] text"}`},
		{"space", providerRecord("a", `{"a":"hello`) + "\n" + providerRecord("a", `  world"}`), `{"a":"hello  world"}`},
		{"empty", providerRecord("a", `{"a":`) + "\n" + providerRecord("a", "") + "\n" + providerRecord("a", `1}`), `{"a":1}`},
		{"utf8", providerRecord("a", "{\"a\":\"\xc3") + "\n" + providerRecord("a", "\xa9\"}"), `{"a":"é"}`},
		{"tag", providerRecord("a", `[Request] Sending request`), ""},
		{"multiword tag", providerRecord("a", `[HTTP Request] Sending request`), ""},
		{"ordinary", providerRecord("a", `status`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReconstructProviderJSON(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatal("ordinary message treated as JSON")
				}
				return
			}
			if len(got) != 1 || got[0].Text != tc.want {
				t.Fatalf("got %#v, want %q", got, tc.want)
			}
			var joined strings.Builder
			for _, f := range got[0].Fragments {
				joined.WriteString(tc.input[f.Start:f.End])
			}
			if joined.String() != tc.want {
				t.Fatal("incorrect source ranges")
			}
		})
	}
}

func TestProviderJSONRejectsUncertainBodies(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"incomplete", providerRecord("a", `{"secret":`)},
		{"malformed", providerRecord("a", `{"secret":]}`)},
		{"syntax", providerRecord("a", `{"secret":invalid}`)},
		{"extra delimiter", providerRecord("a", `{"secret":1}}`)},
		{"adjoining suffix", providerRecord("a", `{"secret":1}garbage`)},
		{"nonmetadata suffix", providerRecord("a", `{"secret":1} garbage`)},
		{"adjoining metadata", providerRecord("a", `{"secret":1}tf_req_id=scope`)},
		{"colon adjoining metadata", providerRecord("a", `{"secret":1}:tf_req_id=scope`)},
		{"colon garbage", providerRecord("a", `{"secret":1}: garbage`)},
		{"bare colon", providerRecord("a", `{"secret":1}:`)},
		{"colon without metadata", providerRecord("a", `{"secret":1}: `)},
		{"unterminated metadata", providerRecord("a", `{"secret":1} tf_req_id="scope`)},
		{"escaped metadata quote", providerRecord("a", `{"secret":1} tf_req_id="scope\"`)},
		{"unseparated metadata", providerRecord("a", `{"secret":1} tf_req_id="scope"tf_rpc=ReadResource`)},
		{"invalid metadata key", providerRecord("a", `{"secret":1} 1bad=scope`)},
		{"malformed literal array", providerRecord("a", `[true false]`)},
		{"malformed numeric array", providerRecord("a", `[1 two]`)},
		{"malformed quoted array", providerRecord("a", `["one" two]`)},
		{"interrupted", providerRecord("a", `{"secret":`) + "\n" + providerRecord("a", `ordinary status`)},
		{"ambiguous plain", providerRecord("a", `{"secret":`) + "\n" + providerRecord("b", `status`) + "\n1}"},
		{"utf8", providerRecord("a", "{\"secret\":\"\xff\"}")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReconstructProviderJSON(tc.input)
			if err == nil || len(got) != 0 {
				t.Fatal("unsafe input accepted")
			}
			if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "line") {
				t.Fatalf("unsafe diagnostic: %v", err)
			}
		})
	}
}

func TestProviderJSONFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, tail, reason, bytes, offset string
	}{
		{"delimiter", `]`, "delimiter mismatch", "11", "11"},
		{"suffix", `1}garbage`, "suffix grammar", "12", ""},
		{"syntax", `invalid}`, "JSON syntax", "18", "11"},
		{"utf8", "\"\xff\"}", "UTF-8", "14", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "ordinary\n" + providerRecord("a", `{"secret":`) + "\n" + providerRecord("a", tc.tail)
			_, err := ReconstructProviderJSON(input)
			if err == nil {
				t.Fatal("invalid body accepted")
			}
			for _, want := range []string{"invalid body", tc.reason, "line 3", "start line 2", "fragments 2", "joined bytes " + tc.bytes} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("diagnostic %q missing %q", err.Error(), want)
				}
			}
			if !strings.Contains(err.Error(), "first fragment bytes 10") {
				t.Fatalf("missing first fragment byte count: %v", err)
			}
			if tc.name == "delimiter" && !strings.Contains(err.Error(), "last fragment bytes 1") {
				t.Fatalf("missing last fragment byte count: %v", err)
			}
			if tc.offset != "" && !strings.Contains(err.Error(), "syntax offset "+tc.offset) {
				t.Fatalf("missing syntax offset: %v", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "garbage") || strings.Contains(err.Error(), "invalid}") {
				t.Fatalf("source in diagnostic: %v", err)
			}
		})
	}
}

func TestProviderJSONDelimiterDiagnosticFindsEarlierSyntaxError(t *testing.T) {
	input := providerRecord("a", `{"secret":invalid`) + "\n" + providerRecord("a", `]`)
	_, err := ReconstructProviderJSON(input)
	if err == nil {
		t.Fatal("invalid body accepted")
	}
	for _, want := range []string{"delimiter mismatch", "syntax offset 11", "first fragment bytes 17", "last fragment bytes 1", "joined bytes 18"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q missing %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("source in diagnostic: %v", err)
	}
}
