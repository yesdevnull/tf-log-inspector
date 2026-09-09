package scrub

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestValidatorAttributeReferenceIsNotUnsupportedJSON(t *testing.T) {
	const input = `2026-09-04T12:53:23.321+1000 [TRACE] provider.terraform-provider-azurerm_v4.81.0_x5: Calling provider defined validator.List: tf_mux_provider="*proto5server.Server" tf_provider_addr=registry.terraform.io/hashicorp/azurerm tf_req_id=f0358e1b-2abf-93a0-f4a8-3038e2d2b73b tf_rpc=PrepareProviderConfig @caller=github.com/hashicorp/terraform-plugin-framework@v1.19.0/internal/fwserver/block_validation.go:221 @module=sdk.framework description="Ensure that if an attribute is set, these are not set: \"[enhanced_validation]\"" timestamp="2026-09-04T12:53:23.321+1000"`
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported != 0 || len(got.UnsupportedInputs) != 0 {
		t.Fatalf("attribute reference reported as unsupported: %+v", got.UnsupportedInputs)
	}
	if !strings.Contains(string(got.Data), `[enhanced_validation]`) || !strings.Contains(string(got.Data), `tf_req_id=f0358e1b-2abf-93a0-f4a8-3038e2d2b73b`) {
		t.Fatal("attribute reference or request ID changed")
	}
	got, err = Scrub([]byte(input), []string{"enhanced_validation"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "enhanced_validation") {
		t.Fatal("attribute reference bypassed explicit replacement")
	}
}

func TestMalformedArraysStillReportUnsupported(t *testing.T) {
	for _, value := range []string{`[true`, `[null,]`, `["unfinished`, `[123,]`, `[{"token":"private-secret"}] trailing`} {
		encoded, _ := json.Marshal(value)
		got, err := Scrub([]byte("http_response_body="+string(encoded)), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Unsupported == 0 {
			t.Errorf("malformed array %q not reported", value)
		}
	}
	got, err := Scrub([]byte("HTTP/1.1 200 OK\n\n[enhanced_validation]\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported == 0 {
		t.Fatal("invalid HTTP JSON body not reported")
	}
}

func TestDescriptiveStringsAreNotJSONPayloads(t *testing.T) {
	for _, value := range []string{`[features[0].enhanced_validation]`, `Ensure that if an attribute is set, these are not set: "[features[0].enhanced_validation]"`, `{attribute}`, `[one two]`, "{example\ntext}", `[true`} {
		encoded, _ := json.Marshal(value)
		for _, input := range []string{
			"description=" + string(encoded),
			`{"description":` + string(encoded) + `}`,
			"HTTP/1.1 200 OK\n\n" + `{"description":` + string(encoded) + `}`,
		} {
			got, err := Scrub([]byte(input), []string{"attribute"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Unsupported != 0 || len(got.UnsupportedInputs) != 0 {
				t.Errorf("text %q reported as JSON: %+v", input, got.UnsupportedInputs)
			}
			if strings.Contains(string(got.Data), "attribute") {
				t.Fatal("text bypassed explicit replacement")
			}
		}
	}
}

func TestValidJSONInDescriptiveStringsStillScrubs(t *testing.T) {
	encoded, _ := json.Marshal(`{"token":"private-credential","name":"private-person"}`)
	got, err := Scrub([]byte("description="+string(encoded)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported != 0 || strings.Contains(string(got.Data), "private-credential") || strings.Contains(string(got.Data), "private-person") {
		t.Fatal("valid embedded JSON not scrubbed")
	}
}

func TestProviderResponseHeadersInStringStillScrubBody(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: "
	message, _ := json.Marshal("Content-Type: application/json\n\n" + `{"value":"private-credential"}`)
	got, err := Scrub([]byte(prefix+`{"@message":`+string(message)+`}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "private-credential") || got.Replacements["secret"] == 0 {
		t.Fatal("HTTP header/body string leaked credential")
	}
}

func TestResponseStringWithProseStillScrubsJSONRecords(t *testing.T) {
	const input = "HTTP/1.1 200 OK\n\n" + `{"description":"details\n{\"value\":\"private-credential\",\"token\":\"private-token\"}"}`
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "private-credential") || strings.Contains(string(got.Data), "private-token") || got.Replacements["secret"] != 2 {
		t.Fatal("JSON following prose bypassed credential scrubbing")
	}
}

func TestUnsupportedLocations(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []UnsupportedInput
	}{
		{"unicode column", "ok\r\né token=\"unfinished\n", []UnsupportedInput{{2, 9, "invalid quoted string"}}},
		{"invalid JSON", "  {broken}\n", []UnsupportedInput{{1, 3, "invalid JSON"}}},
		{"Unicode whitespace", "\u00a0{broken}\n", []UnsupportedInput{{1, 2, "invalid JSON"}}},
		{"decoded newline", `message="HTTP/1.1 200 OK\n\n{broken}"`, []UnsupportedInput{{1, 29, "invalid JSON"}}},
		{"unicode escapes", `message="\uD83D\uDE00 token=\"unfinished"`, []UnsupportedInput{{1, 29, "invalid quoted string"}}},
		{"Go escapes", `message="\x61 token=\"unfinished"`, []UnsupportedInput{{1, 21, "invalid quoted string"}}},
		{"nested quotes", `message="http_response_body=\"{broken}\""`, []UnsupportedInput{{1, 31, "invalid JSON"}}},
		{"unpaired surrogate", `message="\uD800 token=\"unfinished"`, []UnsupportedInput{{1, 23, "invalid quoted string"}}},
		{"Go Unicode escape", `message="\U0001F600 token=\"unfinished"`, []UnsupportedInput{{1, 27, "invalid quoted string"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Scrub([]byte(tc.input), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.UnsupportedInputs, tc.want) {
				t.Fatalf("locations = %+v; want %+v", got.UnsupportedInputs, tc.want)
			}
		})
	}
}

func TestUnsupportedLocationsAreCappedInSourceOrder(t *testing.T) {
	input := strings.Repeat("{broken}\n", 12)
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported != 12 || len(got.UnsupportedInputs) != 10 {
		t.Fatalf("count=%d locations=%d", got.Unsupported, len(got.UnsupportedInputs))
	}
	for i, location := range got.UnsupportedInputs {
		if location.Line != i+1 || location.Column != 1 {
			t.Fatalf("location %d = %+v", i, location)
		}
	}
}

func TestUnsupportedLocationInProviderFragment(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: "
	input := prefix + `{"http_response_body":"` + "\n" + prefix + `{broken}"}` + "\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []UnsupportedInput{{2, len(prefix) + 1, "invalid JSON"}}
	if !reflect.DeepEqual(got.UnsupportedInputs, want) {
		t.Fatalf("locations=%+v; want %+v", got.UnsupportedInputs, want)
	}
}

func TestUnsupportedLocationsOrderAcrossProviderAndPhysicalViews(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: "
	input := prefix + `{"http_response_body":"{broken}"}` + "\n" + strings.Repeat("{broken}\n", 12)
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported != 13 || len(got.UnsupportedInputs) != 10 {
		t.Fatalf("count=%d locations=%d", got.Unsupported, len(got.UnsupportedInputs))
	}
	for i, location := range got.UnsupportedInputs {
		column := 1
		if i == 0 {
			column = len(prefix) + 24
		}
		if location.Line != i+1 || location.Column != column {
			t.Fatalf("location %d = %+v; want line%d column%d", i, location, i+1, column)
		}
	}
}
