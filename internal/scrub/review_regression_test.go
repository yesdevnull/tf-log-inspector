package scrub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRewrittenChunksRemainReadableHTTP(t *testing.T) {
	for _, ending := range []string{"\r\n"} {
		body := `{"name":"private-person","token":"private-credential"}`
		input := "HTTP/1.1 200 OK" + ending + "Transfer-Encoding: chunked" + ending + ending + fmt.Sprintf("%x;part=one", len(body)) + ending + body + ending + "0" + ending + "X-Request-ID: private-request" + ending + ending
		got, err := Scrub([]byte(input), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(got.Data))), nil)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil || !json.Valid(data) || strings.Contains(string(got.Data), "private-") {
			t.Fatalf("rewritten HTTP body invalid or identifying: %s %v %v", got.Data, readErr, closeErr)
		}
	}
}

func TestChunkExtensionsScrubIdentitiesWithoutChangingPayloadSize(t *testing.T) {
	input := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2;contact=alice@example.com;token=private-credential;other=\"value;inside\"\r\nOK\r\n0;token=trailer-credential\r\n\r\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "alice") || strings.Contains(string(got.Data), "private-credential") || strings.Contains(string(got.Data), "trailer-credential") || !strings.Contains(string(got.Data), `other="value;inside"`) {
		t.Fatalf("chunk extension leaked or changed quoted syntax: %s %v", got.Data, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(got.Data))), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil || closeErr != nil || string(data) != "OK" {
		t.Fatalf("chunk payload size changed: %q %v %v", data, err, closeErr)
	}
}

func TestPrettyPrintedChunkedLogDoesNotRequireWireLengths(t *testing.T) {
	compact := `{"name":"private-person"}`
	input := fmt.Sprintf("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n{\n \"name\": \"private-person\"\n}\r\n0\r\n\r\n", len(compact))
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "private-person") || !strings.Contains(string(got.Data), `"name": "name_`) {
		t.Fatalf("pretty-printed HTTP log rejected or leaked: %s %v", got.Data, err)
	}
}

func TestChunkLengthConstraintEndsWithBody(t *testing.T) {
	input := "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nOK\r\n0\r\n\r\nname=private-person\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "private-person") || !strings.Contains(string(got.Data), "2\r\nOK\r\n0\r\n\r\n") {
		t.Fatalf("chunk constraint escaped body: %s %v", got.Data, err)
	}
}

func TestPlanUpdateOperandsShareFieldIdentity(t *testing.T) {
	for _, key := range []string{"name", "password", "resource_id"} {
		t.Run(key, func(t *testing.T) {
			input := "Earlier old-customer new-customer\n  ~ " + key + " = \"old-customer\" -> \"new-customer\"\n"
			got, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			values := strings.Fields(lines[0])
			if strings.Contains(string(got.Data), "customer") || lines[1] != "  ~ "+key+" = \""+values[1]+"\" -> \""+values[2]+"\"" || values[1] == values[2] {
				t.Fatalf("plan update leaked or lost linkage: %s", got.Data)
			}
		})
	}
	got, err := Scrub([]byte("  ~ resource_id = 123 -> 456\ncount = 456\n"), nil)
	if err != nil || strings.Contains(string(got.Data), "123 ->") || strings.Contains(string(got.Data), "-> 456") || !strings.Contains(string(got.Data), "count = 456") {
		t.Fatalf("numeric update changed unrelated counts or leaked: %s %v", got.Data, err)
	}
}

func TestPlanUpdatePreservesNullOperands(t *testing.T) {
	got, err := Scrub([]byte("  ~ name = null -> \"new-customer\"\n  ~ password = \"old-password\" -> null\n"), nil)
	if err != nil || strings.Contains(string(got.Data), "customer") || strings.Contains(string(got.Data), "old-password") || !strings.Contains(string(got.Data), "name = null ->") || !strings.Contains(string(got.Data), "-> null\n") {
		t.Fatalf("null update syntax changed: %s %v", got.Data, err)
	}
}

func TestNullLogCredentialIsNotPlanSyntax(t *testing.T) {
	for _, input := range []string{"password=null\n", "2026-09-08T00:00:00.000Z [DEBUG] provider.aws: password=null\n"} {
		got, err := Scrub([]byte(input), nil)
		if err != nil || strings.Contains(string(got.Data), "password=null") || got.Replacements["secret"] != 1 {
			t.Fatalf("literal log credential exempted: %s %v", got.Data, err)
		}
	}
}

func TestDecodedHTTPDumpStillDiscoversBodyFields(t *testing.T) {
	dump := "HTTP/1.1 200 OK\nAuthorization: Bearer credential\n\nname=private-person\n"
	encoded, _ := json.Marshal(dump)
	out := scrubObject(t, `{"message":`+string(encoded)+`}`)
	if strings.Contains(out["message"], "credential") || strings.Contains(out["message"], "private-person") {
		t.Fatalf("HTTP header suppressed body discovery: %#v", out)
	}
}

func TestDecodedHTTPDumpScrubsEachCredential(t *testing.T) {
	dump := "HTTP/1.1 200 OK\r\nAuthorization: Bearer topsecretvalue\r\nSet-Cookie: session=cookiesecret\r\nContent-Length: 0\r\n\r\n"
	encoded, _ := json.Marshal(dump)
	for _, input := range []string{`{"message":` + string(encoded) + `}`, "2026-09-08T00:00:00.000Z [DEBUG] provider.aws: response=" + string(encoded) + " tf_req_id=scope\n"} {
		got, err := Scrub([]byte(input), nil)
		if err != nil || strings.Contains(string(got.Data), "topsecretvalue") || strings.Contains(string(got.Data), "cookiesecret") || !strings.Contains(string(got.Data), `\r\nContent-Length: 0\r\n\r\n`) || got.Replacements["secret"] != 2 {
			t.Fatalf("HTTP dump leaked or lost framing: %s %v", got.Data, err)
		}
	}
}

func TestDottedDirectoriesShareWholeNameAliases(t *testing.T) {
	for _, path := range []string{"/Users/jane.doe/private.client/terraform.tfstate", `C:\Users\jane.doe\private.client\terraform.tfstate`} {
		input, _ := json.Marshal(map[string]string{"path": path, "name": "jane.doe", "client_name": "private.client"})
		out := scrubObject(t, string(input))
		if strings.Contains(out["path"], ".doe") || strings.Contains(out["path"], ".client") || !strings.Contains(out["path"], out["name"]) || !strings.Contains(out["path"], out["client_name"]) || !strings.HasSuffix(out["path"], ".tfstate") {
			t.Fatalf("directory fragments retained or linkage lost: %#v", out)
		}
	}
}

func TestHTTPRequestBodyCannotInheritMetadataExemption(t *testing.T) {
	input := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: HTTP Request: tf_req_id=scope\r\nPOST / HTTP/1.1\r\nHost: example.invalid\r\nTransfer-Encoding: chunked\r\n\r\n2e\r\ntf_req_id=12345678-1234-4234-8234-123456789abc\r\n0\r\n\r\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "12345678-1234-4234-8234-123456789abc") || !strings.Contains(string(got.Data), "tf_req_id=scope") || len(got.Data) != len(input) {
		t.Fatalf("body gained metadata exemption: %s", got.Data)
	}
}

func TestChunkedBodyUpdatesLengthChangingAliases(t *testing.T) {
	input := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: HTTP Request: tf_req_id=scope\r\nPOST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n20\r\ntf_req_id=private-body-reference\r\n0\r\n\r\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "private-body-reference") || !strings.Contains(string(got.Data), "11\r\ntf_req_id=id_0001\r\n0\r\n") {
		t.Fatalf("chunk length not updated: %s %v", got.Data, err)
	}
}

func TestDecodedHTTPJSONBodyRetainsFieldCategories(t *testing.T) {
	dump := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"name\":\"private-person\",\"token\":\"private-credential\"}"
	encoded, _ := json.Marshal(dump)
	for _, input := range []string{`{"message":` + string(encoded) + `}`, "2026-09-08T00:00:00.000Z [DEBUG] provider.aws: response=" + string(encoded) + " tf_req_id=scope"} {
		got, err := Scrub([]byte(input), nil)
		if err != nil || strings.Contains(string(got.Data), "private-person") || strings.Contains(string(got.Data), "private-credential") || !strings.Contains(string(got.Data), `\"token\":\"secret_`) {
			t.Fatalf("decoded JSON body lost classification: %s %v", got.Data, err)
		}
	}
}

func TestEscapedUnquotedAddressKeySharesIdentity(t *testing.T) {
	got, err := Scrub([]byte(`addr=aws_instance.web["ali\u0063e"] name=alice`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, name, ok := strings.Cut(string(got.Data), " name=")
	if !ok || name == "alice" || !strings.Contains(string(got.Data), `["`+name+`"]`) || strings.Contains(string(got.Data), `\u0063`) {
		t.Fatalf("escaped resource key lost linkage: %s", got.Data)
	}
}

func TestKnownCredentialCannotRemainInFieldKey(t *testing.T) {
	for _, input := range []string{`{"token":"private-credential","private-credential":"something"}`, `{"token":"private-credential","private-\u0063redential":"something"}`, "token=private-credential private-credential=something"} {
		got, err := Scrub([]byte(input), nil)
		if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), "private-credential") {
			t.Fatalf("credential field key conflict accepted: %s %v", got.Data, err)
		}
	}
	got, err := Scrub([]byte(`{"private-credential":"something"}`), []string{"private-credential"})
	if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), "private-credential") {
		t.Fatalf("explicit field key conflict accepted: %s %v", got.Data, err)
	}
}

func TestResourceCollisionWithoutProgressIsRejected(t *testing.T) {
	s := &session{candidates: make(map[string]*candidate), counts: make(map[string]int)}
	views := s.parseLines(`addr=aws_instance.web["alice"]`)
	// An immutable key cannot be made distinct by reallocating its resource label.
	for _, child := range views[0].children {
		child.view.mandatory = true
		child.view.protect(0, len(child.view.text))
	}
	if err := s.allocate(); err != nil {
		t.Fatal(err)
	}
	_, err := s.ensureDistinctResources(views)
	if err == nil || strings.Contains(err.Error(), "alice") {
		t.Fatalf("unresolvable collision accepted or disclosed identity: %v", err)
	}
}
