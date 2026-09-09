package scrub

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

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
