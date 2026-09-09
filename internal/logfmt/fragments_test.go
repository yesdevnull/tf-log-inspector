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

func TestProviderJSONLeavesOrdinaryContinuationsWithTheirEntry(t *testing.T) {
	for _, owner := range []string{"provider.azuread_v1", "terraform"} {
		t.Run(owner, func(t *testing.T) {
			first := providerRecord("a", `{"value":"private-`) + "\n"
			ordinary := "2026-09-08T00:00:00.000Z [DEBUG]  " + owner + ": 2026/09/08 00:00:00 [DEBUG] ============================ Begin AzureAD Request ============================\n" +
				"Request ID: 11111111-2222-4333-8444-555555555555\nPOST /applications HTTP/1.1\nAuthorization: Bearer ordinary-credential\nContent-Type: application/json\n\n{\"password\":\"body-credential\"}\n"
			input := first + ordinary + providerRecord("a", `credential"} tf_req_id=scope`) + "\n"
			got, err := ReconstructProviderJSON(input)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Text != `{"value":"private-credential"}` {
				t.Fatalf("ordinary continuation entered provider JSON: %#v", got)
			}
			var joined strings.Builder
			for _, fragment := range got[0].Fragments {
				joined.WriteString(input[fragment.Start:fragment.End])
			}
			if joined.String() != got[0].Text {
				t.Fatal("source ranges include ordinary continuation bytes")
			}
			var c collector
			var comps, reqIDs Interner
			if _, err := Scan(strings.NewReader(input), &comps, &reqIDs, &c); err != nil {
				t.Fatal(err)
			}
			if len(c.entries) != 3 {
				t.Fatalf("got %d physical entries, want 3", len(c.entries))
			}
			e := c.entries[1]
			if comps.Lookup(e.Comp) != owner || input[e.Off:e.Off+uint64(e.Len)] != ordinary {
				t.Fatal("ordinary entry lost its raw continuations")
			}
			if got, err := ReconstructProviderJSON(first + ordinary); err == nil || len(got) != 0 {
				t.Fatal("unfinished provider JSON accepted")
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

func TestProviderJSONSyntaxDiagnosticLocatesSourceLine(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"event","@timestamp":"2026-09-08T00:00:00Z","type":"apply_complete"}`
	const first = `{"secret":"a` + ui + `","count":`
	for _, tc := range []struct{ name, first, continuation, sourceLine string }{
		{"after inline event", first + `invalid`, "", "1"},
		{"after interleaved entry", first, `invalid`, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := providerRecord("a", tc.first) + "\n" + providerRecord("b", "status") + "\n" +
				providerRecord("a", tc.continuation) + "\n" + providerRecord("b", "status") + "\n" + providerRecord("a", "}")
			got, err := ReconstructProviderJSON(input)
			if err == nil || len(got) != 0 {
				t.Fatal("invalid body accepted")
			}
			for _, want := range []string{"provider JSON at line 5:", "JSON syntax", "start line 1;", "syntax offset 23", "syntax source line " + tc.sourceLine} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("diagnostic %q missing %q", err.Error(), want)
				}
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "event") || strings.Contains(err.Error(), "invalid}") {
				t.Fatalf("source disclosed in diagnostic: %v", err)
			}
		})
	}
}

func TestProviderJSONInlineUI(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"PrivateSP","@timestamp":"2026-09-08T00:00:00Z","type":"apply_complete","hook":{"displayName":"PrivateSP"}}`
	const body = `{"@message":"HTTP/1.1 200 OK\n\n{\"displayName\":\"PrivateSP\"}"}`
	cut := strings.Index(body, "PrivateSP") + 3
	escapeCut := strings.Index(body, `\"displayName`) + 1
	for _, tc := range []struct{ name, input, want string }{
		{"inside string", providerRecord("a", body[:cut]+ui) + "\n" + providerRecord("a", body[cut:]), body},
		{"same line continuation", providerRecord("a", body[:cut]+ui+body[cut:]), body},
		{"separate event", providerRecord("a", body[:cut]) + "\n" + ui + "\n" + providerRecord("a", body[cut:]), body},
		{"escaped quote same line", providerRecord("a", body[:escapeCut]+ui+body[escapeCut:]), body},
		{"escaped quote separate event", providerRecord("a", body[:escapeCut]) + "\n" + ui + "\n" + providerRecord("a", body[escapeCut:]), body},
		{"after atomic string", providerRecord("a", body[:len(body)-1]+ui) + "\n" + providerRecord("a", "}"), body},
		{"after root", providerRecord("a", body+ui), body},
		{"before first key same line", providerRecord("a", `{`+ui+`"nested":{"value":1}}`), `{"nested":{"value":1}}`},
		{"before first key standalone", providerRecord("a", `{"nested":{ `) + "\n" + ui + "\n" + providerRecord("a", `"value":1}}`), `{"nested":{ "value":1}}`},
		{"between keys same line", providerRecord("a", `{"values":["text"], `+ui+`"value":1}`), `{"values":["text"], "value":1}`},
		{"between keys standalone", providerRecord("a", `{"nested":{"values":["text"], `) + "\n" + ui + "\n" + providerRecord("a", `"value":1}}`), `{"nested":{"values":["text"], "value":1}}`},
		{"nested data", providerRecord("a", `{"nested":`+ui+`}`), `{"nested":` + ui + `}`},
		{"first array value", providerRecord("a", `{"nested":[ `) + "\n" + ui + "\n" + providerRecord("a", `]}`), `{"nested":[ ` + ui + `]}`},
		{"later array value", providerRecord("a", `{"nested":[{}, `) + "\n" + ui + "\n" + providerRecord("a", `]}`), `{"nested":[{}, ` + ui + `]}`},
		{"escaped text", providerRecord("a", `{"text":"{\"@module\":\"terraform.ui\"}"}`), `{"text":"{\"@module\":\"terraform.ui\"}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReconstructProviderJSON(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Text != tc.want {
				t.Fatalf("incorrect reconstructed provider: %#v", got)
			}
			var joined strings.Builder
			for _, f := range got[0].Fragments {
				joined.WriteString(tc.input[f.Start:f.End])
			}
			if joined.String() != tc.want {
				t.Fatal("source ranges include UI or omit provider bytes")
			}
		})
	}
}

func TestProviderJSONRejectsUncertainObjectKeyUI(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"private-event","@timestamp":"2026-09-08T00:00:00Z","type":"apply_complete"}`
	for _, candidate := range []string{
		ui[:len(ui)-1],
		strings.Replace(ui, `"info"`, `false`, 1),
		strings.Replace(ui, `"terraform.ui"`, `"other"`, 1),
		`{"message":"private-event"}`,
	} {
		for _, prefix := range []string{`{`, `{"values":["text"],`} {
			input := providerRecord("a", prefix) + "\n" + candidate + "\n" + providerRecord("a", `"value":1}`)
			got, err := ReconstructProviderJSON(input)
			if err == nil || len(got) != 0 {
				t.Fatal("uncertain object-key event accepted")
			}
			if strings.Contains(err.Error(), "private-event") {
				t.Fatal("diagnostic disclosed event")
			}
		}
	}
}

func TestProviderJSONInlineUIFailureDiagnostics(t *testing.T) {
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"event","@timestamp":"2026-09-08T00:00:00Z","type":"apply_complete"}`
	_, err := ReconstructProviderJSON(providerRecord("a", `{"a":"x`+ui+`"}]`))
	if err == nil {
		t.Fatal("invalid suffix accepted")
	}
	if !strings.Contains(err.Error(), "joined bytes 9;") {
		t.Fatalf("provider byte count includes duplicated prefix or UI: %v", err)
	}
}
