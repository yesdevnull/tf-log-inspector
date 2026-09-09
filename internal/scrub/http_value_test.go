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
)

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

func TestFragmentedHTTPResponseJSONFailsClosed(t *testing.T) {
	const input = "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n9\r\n{\"value\":\r\n11\r\n\"private-content\"\r\n1\r\n}\r\n0\r\n\r\n"
	got, err := Scrub([]byte(input), nil)
	if err == nil || len(got.Data) != 0 || !strings.Contains(err.Error(), "fragmented JSON") || strings.Contains(err.Error(), "private-content") {
		t.Fatalf("fragmented response published: %s %v", got.Data, err)
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
