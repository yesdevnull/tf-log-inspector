package scrub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestProviderJSONMessageValuesAreSecrets(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: "
	const body = `{"value":"ORG_SPECIFIC_SUBDOMAIN.name0355.int","id":"https://name5142.vault.azure.net/secrets/name5567/name5570","attributes":{"enabled":true,"created":1773897762,"updated":1773897762,"recoveryLevel":"Recoverable","recoverableDays":90},"tags":{}}`
	for _, message := range []string{body, "\t" + body, "[DEBUG] " + body, "2026/09/04 12:56:10 [DEBUG] " + body, strings.ReplaceAll(body, ",", ",\n")} {
		t.Run(message, func(t *testing.T) {
			input := prefix + message + " tf_req_id=abc\n" + prefix + "Routine status\n{\"value\":\"ordinary-status\"}\n"
			got, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			output := string(got.Data)
			if strings.Contains(output, "ORG_SPECIFIC_SUBDOMAIN") || !strings.Contains(output, `"value":"secret_`) || got.Unsupported != 0 {
				t.Fatalf("provider JSON value leaked: %s", output)
			}
			if !strings.Contains(output, "tf_req_id=abc") || !strings.Contains(output, `"value":"ordinary-status"`) {
				t.Fatalf("provider body changed metadata or next record: %s", output)
			}
			h := logfmt.ParseHeader(output)
			var parsed struct {
				Attributes map[string]any `json:"attributes"`
			}
			if err := json.NewDecoder(strings.NewReader(h.Msg)).Decode(&parsed); err != nil || parsed.Attributes["enabled"] != true || parsed.Attributes["created"] != float64(1773897762) {
				t.Fatalf("provider JSON structure changed: %s %v", output, err)
			}
		})
	}
}

func TestProviderBracketedDiagnosticIsNotJSON(t *testing.T) {
	const input = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: [Request] Sending request to server\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || string(got.Data) != input || got.Unsupported != 0 {
		t.Fatalf("bracketed diagnostic rejected or changed: %s %v", got.Data, err)
	}
}

func TestProviderJSONMessageCollections(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.azure: "
	got, err := Scrub([]byte(prefix+`[{"value":"private-content"},{"value":null},{"value":""}]`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	h := logfmt.ParseHeader(string(got.Data))
	if err := json.Unmarshal([]byte(h.Msg), &entries); err != nil || len(entries) != 3 {
		t.Fatalf("provider collection lost JSON structure: %s %v", got.Data, err)
	}
	if value, ok := entries[0]["value"].(string); !ok || !strings.HasPrefix(value, "secret_") || entries[1]["value"] != nil || entries[2]["value"] != "" {
		t.Fatalf("provider collection values leaked or empty values changed: %s", got.Data)
	}
}

func TestMalformedProviderJSONFailsClosed(t *testing.T) {
	const prefix = "2026-09-04T12:56:10.514+1000 [DEBUG] provider.azure: "
	for _, body := range []string{`{"value":"private-content"`, `[{"value":"private-content"}`} {
		got, err := Scrub([]byte(prefix+body), nil)
		if err == nil || len(got.Data) != 0 || !strings.Contains(err.Error(), "invalid body") || strings.Contains(err.Error(), "private-content") {
			t.Fatalf("malformed provider JSON published: %s %v", got.Data, err)
		}
	}
}

func TestHTTPResponseScalarValuesAreSecrets(t *testing.T) {
	const body = `{"value":"private-content","nested":{"value":"private-content"},"echo":"private-content"}`
	dump := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n" + body
	wrapped, _ := json.Marshal(map[string]string{"response": dump})
	for _, input := range []string{dump, string(wrapped), "http.response.body=" + strconv.Quote(body), "2026-09-08T00:00:00.000Z [DEBUG] provider.azure: HTTP Response:\n" + body} {
		got, err := Scrub([]byte(input), nil)
		if err != nil || strings.Contains(string(got.Data), "private-content") || !strings.Contains(string(got.Data), "secret_") {
			t.Fatalf("HTTP response value leaked: %s %v", got.Data, err)
		}
	}
}

func TestHTTPResponseStatusAfterLoggerPrefix(t *testing.T) {
	const header = "2026-09-08T00:00:00.000Z [DEBUG] provider.aws: "
	for _, prefix := range []string{"", header, header + "[DEBUG] ", header + "2026/09/08 00:00:00 [DEBUG] "} {
		t.Run(prefix, func(t *testing.T) {
			input := prefix + "HTTP/1.1 200 OK\n\n{\"value\":\"private-credential\"}\n" +
				header + "Routine status: tf_req_id=scope\n{\"value\":\"ordinary-status\"}\n"
			got, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			var body map[string]string
			if json.Unmarshal([]byte(lines[2]), &body) != nil || !strings.HasPrefix(body["value"], "secret_") || got.Unsupported != 0 {
				t.Fatalf("prefixed response lost credential classification: %s", got.Data)
			}
			if lines[0] != prefix+"HTTP/1.1 200 OK" || lines[3] != header+"Routine status: tf_req_id=scope" || lines[4] != `{"value":"ordinary-status"}` {
				t.Fatalf("HTTP framing, metadata or following record changed: %s", got.Data)
			}
		})
	}
}

func TestUnquotedHTTPResponseBodyValuesAreSecrets(t *testing.T) {
	for _, input := range []string{
		`http.response.body={"value":"private-content"}`,
		"http.response.body=\n{\"value\":\"private-content\"}\n",
		"http.response.body= {\n  \"value\": \"private-content\"\n}\n",
		"2026-09-08T00:00:00.000Z [DEBUG] provider.azure: response http.response.body={\"value\":\"private-content\"} tf_req_id=abc\n",
		"2026-09-08T00:00:00.000Z [DEBUG] provider.azure: response http.response.body=\n{\"value\":\"private-content\"}\n",
		"2026-09-08T00:00:00.000Z [DEBUG] provider.azure: response http.response.body= {\n\"value\":\"private-content\"\n} tf_req_id=abc\n",
	} {
		got, err := Scrub([]byte(input), nil)
		if err != nil || strings.Contains(string(got.Data), "private-content") || !strings.Contains(string(got.Data), "secret_") {
			t.Fatalf("unquoted response value leaked: %s %v", got.Data, err)
		}
	}
}

func TestHTTPRequestBodyFieldsRetainCredentialClassification(t *testing.T) {
	const header = "2026-09-08T00:00:00.000Z [DEBUG] provider.aws: "
	const body = `{"password":"private-credential","value":"ordinary-request"}`
	for _, key := range []string{"http.request.body", "httpRequestBody", "http_request_body", "http-request-body"} {
		for _, tc := range []struct{ name, prefix, body string }{
			{"compact", "", body},
			{"multiline", header, "{\n\"password\":\"private-credential\",\n\"value\":\"ordinary-request\"\n}"},
			{"quoted", header, strconv.Quote(body)},
			{"response context", header + "HTTP Response: ", body},
			{"response context next line", header + "HTTP Response: ", "\n" + body},
		} {
			t.Run(key+"/"+tc.name, func(t *testing.T) {
				input := tc.prefix + key + "=" + tc.body + " tf_req_id=scope\n"
				got, err := Scrub([]byte(input), nil)
				if err != nil {
					t.Fatal(err)
				}
				_, output, ok := strings.Cut(string(got.Data), key+"=")
				if !ok || strings.Contains(output, "private-credential") || got.Unsupported != 0 {
					t.Fatalf("request body lost credential classification: %s", got.Data)
				}
				var raw json.RawMessage
				if err := json.NewDecoder(strings.NewReader(output)).Decode(&raw); err != nil {
					t.Fatalf("request body lost JSON syntax: %s %v", got.Data, err)
				}
				if raw[0] == '"' {
					var decoded string
					if err := json.Unmarshal(raw, &decoded); err != nil {
						t.Fatal(err)
					}
					raw = []byte(decoded)
				}
				var fields map[string]string
				if json.Unmarshal(raw, &fields) != nil || !strings.HasPrefix(fields["password"], "secret_") || fields["value"] != "ordinary-request" {
					t.Fatalf("request field scope changed: %s", got.Data)
				}
				if tc.prefix != "" && !strings.HasSuffix(string(got.Data), " tf_req_id=scope\n") {
					t.Fatalf("genuine request metadata changed: %s", got.Data)
				}
			})
		}
	}
}

func TestHTTPRequestBodyOverridesEnclosingResponseContext(t *testing.T) {
	const input = `{"http.response.body":{"value":"response-credential","http.request.body":{"password":"request-credential","value":"ordinary-request"}}}`
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "response-credential") || strings.Contains(string(got.Data), "request-credential") || !strings.Contains(string(got.Data), `"value":"ordinary-request"`) || !json.Valid(got.Data) {
		t.Fatalf("request body inherited response scalar classification: %s %v", got.Data, err)
	}
}

func TestFragmentedHTTPResponseJSONFailsClosed(t *testing.T) {
	const input = "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n9\r\n{\"value\":\r\n11\r\n\"private-content\"\r\n1\r\n}\r\n0\r\n\r\n"
	got, err := Scrub([]byte(input), nil)
	if err == nil || len(got.Data) != 0 || !strings.Contains(err.Error(), "fragmented JSON") || strings.Contains(err.Error(), "private-content") {
		t.Fatalf("fragmented response published: %s %v", got.Data, err)
	}
}

func TestFragmentedHTTPRequestJSONFailsClosed(t *testing.T) {
	const body = `{"password":"private-credential"}`
	for cut := 1; cut < len(body); cut++ {
		input := fmt.Sprintf("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n%x\r\n%s\r\n0\r\n\r\n", cut, body[:cut], len(body)-cut, body[cut:])
		got, err := Scrub([]byte(input), nil)
		if err == nil || len(got.Data) != 0 || !strings.Contains(err.Error(), "fragmented JSON") || strings.Contains(err.Error(), "private-credential") {
			t.Fatalf("request split at byte %d published data or disclosed content: %s %v", cut, got.Data, err)
		}
	}
}

func TestChunkedHTTPRequestCredentialsRemainReadable(t *testing.T) {
	const body = `{"password":"private-credential","value":"ordinary-request"}`
	input := fmt.Sprintf("POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(body), body)
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.ReadRequest(bufio.NewReader(strings.NewReader(string(got.Data))))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	var fields map[string]string
	if readErr != nil || closeErr != nil || json.Unmarshal(data, &fields) != nil || !strings.HasPrefix(fields["password"], "secret_") || fields["value"] != "ordinary-request" {
		t.Fatalf("request lost credentials, scalar scope or chunk framing: %s %v %v", data, readErr, closeErr)
	}
}

func TestStructuredHTTPResponseBodyValueScope(t *testing.T) {
	const input = `{"http.response.body":{"value":"private-content","nested":{"value":"nested-content"}},"value":"ordinary-status"}`
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "private-content") || strings.Contains(string(got.Data), "nested-content") || !strings.Contains(string(got.Data), `"value":"ordinary-status"`) {
		t.Fatalf("structured response body value scope incorrect: %s %v", got.Data, err)
	}
}

func TestHTTPResponseCannotGainLifecycleExemptions(t *testing.T) {
	const body = `{"@level":"info","@timestamp":"2026-09-08T00:00:00Z","value":"private-content"}`
	got, err := Scrub([]byte("HTTP/1.1 200 OK\n\n"+body), nil)
	if err != nil || strings.Contains(string(got.Data), "private-content") || !strings.Contains(string(got.Data), `"value":"secret_`) {
		t.Fatalf("response gained lifecycle exemptions: %s %v", got.Data, err)
	}
}

func TestHTTPResponseValueScalarTypes(t *testing.T) {
	const body = `{"value":[123,true,null,""],"count":7}`
	got, err := Scrub([]byte("HTTP/1.1 200 OK\n\n"+body), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, output, _ := strings.Cut(string(got.Data), "\n\n")
	var parsed struct {
		Value []any `json:"value"`
		Count int   `json:"count"`
	}
	if json.Unmarshal([]byte(output), &parsed) != nil || len(parsed.Value) != 4 || parsed.Count != 7 {
		t.Fatalf("response scalar types lost: %s", got.Data)
	}
	if n, ok := parsed.Value[0].(float64); !ok || n == 123 || parsed.Value[1] != false || parsed.Value[2] != nil || parsed.Value[3] != "" {
		t.Fatalf("response scalars leaked or empty values changed: %s", got.Data)
	}
}

func TestHTTPResponseValueCollectionsRetainStructure(t *testing.T) {
	const body = `{"value":[{"displayName":"PrivateSP","value":"private-content"},{"value":{"displayName":"OtherSP","value":"other-content"}},"scalar-entry"],"count":2}`
	got, err := Scrub([]byte("HTTP/1.1 200 OK\n\n"+body), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, output, ok := strings.Cut(string(got.Data), "\n\n")
	var parsed struct {
		Value []json.RawMessage `json:"value"`
		Count int               `json:"count"`
	}
	if !ok || json.Unmarshal([]byte(output), &parsed) != nil || len(parsed.Value) != 3 || parsed.Count != 2 {
		t.Fatalf("value collection lost structure: %s", got.Data)
	}
	for _, value := range []string{"PrivateSP", "OtherSP", "private-content", "other-content", "scalar-entry"} {
		if strings.Contains(output, value) {
			t.Fatalf("nested value leaked: %s", got.Data)
		}
	}
	if !strings.HasPrefix(string(parsed.Value[0]), "{") || !strings.HasPrefix(string(parsed.Value[1]), "{\"value\":{") || !strings.HasPrefix(string(parsed.Value[2]), `"secret_`) {
		t.Fatalf("value entries flattened: %s", got.Data)
	}
}

func TestHTTPResponseValueContextEndsAtNextLogRecord(t *testing.T) {
	input := "HTTP/1.1 200 OK\n\n{\"value\":\"private-content\"}\n2026-09-08T00:00:00.000Z [DEBUG] provider.azure: Routine status\n{\"value\":\"ordinary-status\"}\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "private-content") || !strings.Contains(string(got.Data), `"value":"ordinary-status"`) {
		t.Fatalf("response classification leaked scope: %s %v", got.Data, err)
	}
}

func TestChunkedHTTPResponseValuesRemainReadable(t *testing.T) {
	const body = `{"value":"private-content"}`
	input := fmt.Sprintf("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(body), body)
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(got.Data))), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || !json.Valid(data) || strings.Contains(string(data), "private-content") || !strings.Contains(string(data), "secret_") {
		t.Fatalf("chunked value leaked or framing invalid: %s %v %v", data, readErr, closeErr)
	}
}
