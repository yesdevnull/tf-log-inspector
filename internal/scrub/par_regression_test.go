package scrub

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestEmailAddressCannotBecomeResourceSyntax(t *testing.T) {
	const address = "alice_dev.private@example.test"
	got, err := Scrub([]byte("Contact "+address+" for help"), nil)
	if err != nil || strings.Contains(string(got.Data), "alice_dev") || strings.Contains(string(got.Data), "example.test") || got.Replacements["email"] != 1 {
		t.Fatalf("email retained identifying syntax: %s %v", got.Data, err)
	}
	input, _ := json.Marshal(map[string]string{"email": address, "message": "Contact " + address + " for help", "addr": `aws_instance.private_resource["` + address + `"]`})
	out := scrubObject(t, string(input))
	if out["email"] == address || out["message"] != "Contact "+out["email"]+" for help" || !strings.HasPrefix(out["addr"], "aws_instance.") || strings.Contains(out["addr"], "private_resource") || !strings.Contains(out["addr"], `["`+out["email"]+`"]`) {
		t.Fatalf("email or enclosing resource lost linkage: %#v", out)
	}
}

func TestNormalisedResponseBodyKeysAcrossRepresentations(t *testing.T) {
	const body = `{"value":"synthetic-private"}`
	for _, key := range []string{"http.response.body", "httpResponseBody", "HTTP.Response.Body", "http-response-body", "http_response_body", "_httpResponseBody", "__http_response_body"} {
		for _, value := range []string{strconv.Quote(body), body, "{\n  \"value\": \"synthetic-private\"\n}"} {
			input := "2026-09-08T00:00:00.000Z [DEBUG] provider.azure: response " + key + "= " + value + " tf_req_id=abc\n"
			got, err := Scrub([]byte(input), nil)
			if err != nil || strings.Contains(string(got.Data), "synthetic-private") || !strings.Contains(string(got.Data), "secret_") || !strings.Contains(string(got.Data), "tf_req_id=abc") {
				t.Fatalf("response key %s lost discovery: %s %v", key, got.Data, err)
			}
		}
		got, err := Scrub([]byte(key+"=\n"+body+"\n"), nil)
		if err != nil || strings.Contains(string(got.Data), "synthetic-private") || !strings.Contains(string(got.Data), "secret_") {
			t.Fatalf("response key %s lost continuation context: %s %v", key, got.Data, err)
		}
	}
}

func TestUnicodeURLPathUsesOriginalByteSpans(t *testing.T) {
	for _, path := range []string{"/café", "/caf%C3%A9", "/projects/café/locations/europe", "/projects/caf%C3%A9/locations/europe"} {
		input, _ := json.Marshal(map[string]string{"endpoint": "https://example.test" + path + "?token=hidden#café", "name": "café", "token": "hidden"})
		out := scrubObject(t, string(input))
		u, err := url.Parse(out["endpoint"])
		if err != nil || !strings.Contains(u.Path, out["name"]) || u.Query().Get("token") != out["token"] || u.Fragment != out["name"] || strings.Contains(out["endpoint"], "café") {
			t.Fatalf("Unicode path lost identity or URL structure: %#v %v", out, err)
		}
	}
	out := scrubObject(t, `{"raw":"https://example.test/café","encoded":"https://example.test/caf%C3%A9","name":"café"}`)
	if out["raw"] != out["encoded"] || !strings.HasSuffix(out["raw"], "/"+out["name"]) {
		t.Fatalf("equivalent URL spellings lost linkage: %#v", out)
	}
	input := "GET /café?token=hidden HTTP/1.1\nname=café\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || strings.Contains(string(got.Data), "café") || strings.Contains(string(got.Data), "hidden") || !strings.Contains(string(got.Data), " HTTP/1.1") {
		t.Fatalf("Unicode request target failed: %s %v", got.Data, err)
	}
}
