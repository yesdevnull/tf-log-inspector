package scrub

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestScrubProviderMetadataWindow(t *testing.T) {
	const header = "2026-09-04T12:56:10.000Z [DEBUG] provider.aws: "
	const suffix = " tf_req_id=scope"
	for _, fragmented := range []bool{false, true} {
		for _, shrink := range []bool{false, true} {
			t.Run(fmt.Sprintf("fragmented=%t/shrink=%t", fragmented, shrink), func(t *testing.T) {
				body := `{"name":"a"}`
				// The request ends exactly at the real scanner's message window.
				padding := 65536 - len(body) - len(suffix)
				if fragmented {
					padding++ // The opening brace belongs to the previous record.
				}
				body = body[:len(body)-1] + strings.Repeat(" ", padding) + "}"
				wantRequest := "scope"
				if shrink {
					body = `{"value":"` + strings.Repeat("z", 70000) + `"}`
					wantRequest = ""
				}
				input := header + body + suffix + "\n"
				if fragmented {
					input = header + body[:1] + "\n" + header + body[1:] + suffix + "\n"
				}
				before, err := scanMetadata(input)
				if err != nil || before[len(before)-1].request != wantRequest {
					t.Fatalf("fixture request visibility: %v, %v", before, err)
				}
				got, err := Scrub([]byte(input), nil)
				if err == nil || got.Data != nil {
					t.Fatal("published provider body changing genuine metadata visibility")
				}
				if !strings.Contains(err.Error(), "changed metadata") || strings.Contains(err.Error(), "zzz") {
					t.Fatalf("expected content-free metadata rejection: %v", err)
				}
			})
		}
	}
}

func TestScrubProviderHTTPBanner(t *testing.T) {
	input := `2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: {"@level":"debug","@message":"2026/09/04 12:56:10 [DEBUG] ==== HTTP Response ====\nHTTP/1.1 200 OK\n\n{\"value\":\"private-credential\",\"displayName\":\"PrivateSP\"}"}` + "\n"
	result, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Data), "private-credential") || strings.Contains(string(result.Data), "PrivateSP") {
		t.Fatal("nested HTTP body leaked")
	}
}

func TestScrubProviderMetadataIgnoresBodyDecoys(t *testing.T) {
	const header = "2026-09-04T12:56:10.000Z [DEBUG] provider.aws: "
	// A continuation starts with a field lookalike inside a JSON string. The
	// genuine suffix still belongs to the second record after body expansion.
	input := header + `{"name":"x","description":"` + "\n" +
		header + `tf_req_id=decoy tf_rpc=DeleteResource"} tf_req_id=scope tf_rpc=ReadResource` + "\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got.Data), " tf_req_id=scope tf_rpc=ReadResource\n") {
		t.Fatal("genuine suffix metadata changed")
	}
	joined, err := logfmt.ReconstructProviderJSON(string(got.Data))
	if err != nil || len(joined) != 1 {
		t.Fatalf("reconstruction: %v", err)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(joined[0].Text), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] == "x" || body["name"] == "" {
		t.Fatal("body name was not scrubbed")
	}
}

func TestScrubProviderFragmentBoundaries(t *testing.T) {
	for _, body := range []string{
		`{"displayName":"PrivateSP","value":"private-credential","tf_req_id":"private-request"}`,
		"{\n  \"description\": \"日本語\",\n  \"displayName\": \"PrivateSP\",\n  \"value\": \"private-credential\"\n}",
	} {
		// Every byte exercises boundaries in syntax, quotes, replacements and UTF-8.
		for cut := 1; cut < len(body); cut++ {
			input := "2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: " + body[:cut] + "\n" +
				"2026-09-04T12:56:10.001Z [DEBUG] provider.azuread_v1: " + body[cut:] + " tf_req_id=abc\n"
			result, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatalf("cut %d: %v", cut, err)
			}
			if !utf8.Valid(result.Data) {
				t.Fatalf("cut %d: invalid UTF-8", cut)
			}
			joined, err := logfmt.ReconstructProviderJSON(string(result.Data))
			if err != nil || len(joined) != 1 {
				t.Fatalf("cut %d: reconstruction: %v", cut, err)
			}
			var fields map[string]string
			if err := json.Unmarshal([]byte(joined[0].Text), &fields); err != nil {
				t.Fatal(err)
			}
			for _, value := range fields {
				if value == "private-credential" || value == "PrivateSP" || value == "private-request" {
					t.Fatalf("cut %d: body identifier leaked", cut)
				}
			}
			if strings.Contains(body, "日本語") && fields["description"] != "日本語" {
				t.Fatal("unchanged Unicode changed")
			}
			if !strings.HasSuffix(string(result.Data), " tf_req_id=abc\n") {
				t.Fatal("trailing metadata changed")
			}
		}
	}
}

func TestScrubRejectsInvalidProviderFragments(t *testing.T) {
	for _, input := range []string{
		"2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: {\"value\":\"private-credential\n",
		"2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: {\"value\":\"private-credential\" invalid}\n",
		"\xff\n2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: {\"value\":\"private-credential\"}\n",
	} {
		result, err := Scrub([]byte(input), nil)
		if err == nil || len(result.Data) != 0 {
			t.Fatal("invalid input produced output")
		}
		if strings.Contains(err.Error(), "private-credential") {
			t.Fatal("error disclosed source")
		}
	}
}

func TestScrubOrdinaryHTTPContinuationWhileProviderJSONPending(t *testing.T) {
	const header = "2026-09-04T12:56:10.000Z [DEBUG] provider.azurerm_v1: "
	input := header + `{"value":"private-` + "\n" +
		"2026-09-04T12:56:10.001Z [DEBUG]  provider.azuread_v1: 2026/09/04 12:56:10 [DEBUG] ============================ Begin AzureAD Request ============================\n" +
		"Request ID: 11111111-2222-4333-8444-555555555555\nPOST /applications HTTP/1.1\nAuthorization: Bearer ordinary-credential\nContent-Type: application/json\n\n{\"password\":\"body-credential\"}\n"
	if got, err := Scrub([]byte(input), nil); err == nil || len(got.Data) != 0 {
		t.Fatal("unfinished provider JSON published")
	}
	input += header + `credential"} tf_req_id=scope` + "\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	output := string(got.Data)
	for _, secret := range []string{"private-credential", "ordinary-credential", "body-credential", "11111111-2222-4333-8444-555555555555"} {
		if strings.Contains(output, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	if !strings.HasSuffix(output, " tf_req_id=scope\n") || !strings.Contains(output, "POST /") || !strings.Contains(output, " HTTP/1.1\n") || !strings.Contains(output, "Authorization: secret_") {
		t.Fatalf("HTTP structure or genuine request metadata changed: %s", output)
	}
	joined, err := logfmt.ReconstructProviderJSON(output)
	if err != nil || len(joined) != 1 {
		t.Fatalf("scrubbed provider reconstruction: %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(joined[0].Text), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || !strings.HasPrefix(fields["value"], "secret_") {
		t.Fatal("provider secret was not scrubbed independently")
	}
}

func TestScrubInterleavedProviderFragments(t *testing.T) {
	prefix := "2026/09/04 12:56:10 [DEBUG] ==== HTTP Response ====\nHTTP/1.1 200 OK\n\n{\"padding\":\""
	suffix := "\",\"value\":\"private-credential\",\"displayName\":\"PrivateSP\"}"
	empty, err := json.Marshal(map[string]string{"@level": "debug", "@message": prefix + suffix})
	if err != nil {
		t.Fatal(err)
	}
	padding := strings.Repeat("x", 65535-strings.Index(string(empty), `\"value`))
	envelope, err := json.Marshal(map[string]string{"@level": "debug", "@message": prefix + padding + suffix})
	if err != nil {
		t.Fatal(err)
	}
	body := string(envelope)
	if body[65535] != '\\' || body[65536] != '"' {
		t.Fatal("fixture must split an escape at 64 KiB")
	}
	for _, cut := range []int{65536, strings.Index(body, "credential") + 4} {
		input := "2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: " + body[:cut] + "\n" +
			"2026-09-04T12:56:10.001Z [DEBUG] provider.azurerm_v1: {\"displayName\":\"PrivateSP\",\"value\":\"other-credential\"}\n" +
			"2026-09-04T12:56:10.002Z [DEBUG] provider.azuread_v1: " + body[cut:] + " tf_req_id=abc\n"
		result, err := Scrub([]byte(input), nil)
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		output := string(result.Data)
		for _, secret := range []string{"private-credential", "other-credential", "PrivateSP"} {
			if strings.Contains(output, secret) {
				t.Errorf("cut %d leaked %s", cut, secret)
			}
		}
		if !utf8.Valid(result.Data) {
			t.Fatal("invalid output UTF-8")
		}
		lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("record count = %d", len(lines))
		}
		for i, stamp := range []string{"10.000Z", "10.001Z", "10.002Z"} {
			if !strings.Contains(lines[i], stamp) {
				t.Fatalf("record %d moved", i)
			}
		}
		if !strings.HasSuffix(lines[2], " tf_req_id=abc") {
			t.Fatal("request metadata changed")
		}
		joined, err := logfmt.ReconstructProviderJSON(output)
		if err != nil || len(joined) != 2 {
			t.Fatalf("reconstruction: %d messages, %v", len(joined), err)
		}
		var outer map[string]string
		if err := json.Unmarshal([]byte(joined[0].Text), &outer); err != nil {
			t.Fatal(err)
		}
		var response, other map[string]string
		_, nested, ok := strings.Cut(outer["@message"], "\n\n")
		if !ok {
			t.Fatal("HTTP body missing")
		}
		if err := json.Unmarshal([]byte(nested), &response); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(joined[1].Text), &other); err != nil {
			t.Fatal(err)
		}
		if response["displayName"] == "" || response["displayName"] != other["displayName"] {
			t.Fatal("name linkage lost")
		}
		if response["value"] == "private-credential" || other["value"] == "other-credential" {
			t.Fatal("decoded credential leaked")
		}
	}
}

func TestScrubProviderInlineUI(t *testing.T) {
	const header = "2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: "
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"PrivateSP","@timestamp":"2026-09-04T12:56:10Z","type":"apply_complete","hook":{"displayName":"PrivateSP","password":"ui-credential"}}`
	const body = `{"@level":"debug","@message":"HTTP/1.1 200 OK\n\n{\"displayName\":\"PrivateSP\",\"value\":\"private-credential\"}"}`
	for _, tc := range []struct {
		name     string
		cut      int
		separate bool
	}{
		{"inside name", strings.Index(body, "PrivateSP") + 3, false},
		{"inside credential", strings.Index(body, "credential") + 4, false},
		{"atomic boundary", len(body) - 1, false},
		{"before first key", 1, false},
		{"before first key separate UI record", 1, true},
		{"between keys", strings.Index(body, `,"@message"`) + 1, false},
		{"between keys separate UI record", strings.Index(body, `,"@message"`) + 1, true},
		{"separate UI record", strings.Index(body, "PrivateSP") + 3, true},
		{"escaped quote", strings.Index(body, `\"displayName`) + 1, false},
		{"escaped quote separate UI record", strings.Index(body, `\"displayName`) + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cut := tc.cut
			separator := ""
			records := 2
			if tc.separate {
				separator = "\n"
				records = 3
			}
			input := header + body[:cut] + separator + ui + "\n" + header + body[cut:] + " tf_req_id=scope\n"
			result, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			output := string(result.Data)
			for _, value := range []string{"PrivateSP", "private-credential", "ui-credential"} {
				if strings.Contains(output, value) {
					t.Fatalf("leaked %s", value)
				}
			}
			if strings.Count(output, "\n") != records || strings.Count(output, header) != 2 || !strings.HasSuffix(output, " tf_req_id=scope\n") {
				t.Fatal("physical records or metadata changed")
			}
			lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
			if !strings.HasPrefix(lines[0], header) || !strings.HasPrefix(lines[records-1], header) || !strings.Contains(lines[records-2], `"type":"apply_complete"`) {
				t.Fatal("provider and UI record order changed")
			}
			joined, err := logfmt.ReconstructProviderJSON(output)
			if err != nil || len(joined) != 1 {
				t.Fatalf("rewritten reconstruction: %v", err)
			}
			var envelope map[string]string
			if err := json.Unmarshal([]byte(joined[0].Text), &envelope); err != nil {
				t.Fatal(err)
			}
			_, nested, ok := strings.Cut(envelope["@message"], "\n\n")
			if !ok {
				t.Fatal("HTTP response missing")
			}
			var response map[string]string
			if err := json.Unmarshal([]byte(nested), &response); err != nil {
				t.Fatal(err)
			}
			// Remove only provider ranges to inspect the independently scrubbed UI.
			physical := output
			for i := len(joined[0].Fragments) - 1; i >= 0; i-- {
				f := joined[0].Fragments[i]
				physical = physical[:f.Start] + physical[f.End:]
			}
			eventStart := strings.Index(physical, `{"@level"`)
			if eventStart < 0 {
				t.Fatal("physical UI event missing")
			}
			var event struct {
				Hook map[string]string `json:"hook"`
			}
			if err := json.NewDecoder(strings.NewReader(physical[eventStart:])).Decode(&event); err != nil {
				t.Fatal(err)
			}
			if response["displayName"] == "" || response["displayName"] != event.Hook["displayName"] {
				t.Fatal("physical and provider aliases differ")
			}
		})
	}
}

func TestScrubRejectsUncertainInlineUI(t *testing.T) {
	const header = "2026-09-04T12:56:10.000Z [DEBUG] provider.azuread_v1: "
	const prefix = `{"@message":"private-credential`
	const ui = `{"@level":"info","@module":"terraform.ui","@message":"secret-event","@timestamp":"2026-09-04T12:56:10Z","type":"apply_complete"}`
	for _, input := range []string{
		header + prefix + ui,
		header + prefix + ui[:len(ui)-1] + "\n" + header + `"}`,
		header + prefix + strings.Replace(ui, `"terraform.ui"`, `"other"`, 1) + "\n" + header + `"}`,
		header + prefix + `\` + ui[:len(ui)-1] + "\n" + header + `"text"}`,
	} {
		result, err := Scrub([]byte(input), nil)
		if err == nil || len(result.Data) != 0 {
			t.Fatal("uncertain UI produced output")
		}
		if strings.Contains(err.Error(), "private-credential") || strings.Contains(err.Error(), "secret-event") {
			t.Fatal("diagnostic leaked input")
		}
	}
}
