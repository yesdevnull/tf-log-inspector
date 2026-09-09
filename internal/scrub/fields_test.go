package scrub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
)

func TestFieldClassification(t *testing.T) {
	for category, keys := range map[string][]string{
		"name":   {"name", "user", "username", "user_name", "displayName", "first_name", "last-name", "full.name", "organisation", "organization", "workspace", "project", "tenant", "account", "tfResourceName"},
		"id":     {"id", "uuid", "guid", "requestid", "correlationid", "request_id", "tf_resource_id", "tfResourceID", "other-UUID", "other.guid"},
		"secret": {"password", "passwd", "pwd", "secret", "token", "api_key", "apikey", "access_key", "accesskey", "private_key", "client_secret", "authorization", "proxy_authorization", "cookie", "cookies", "set_cookie", "dbPassword", "http.request.header.x_api_key", "awsAccessKeyID"},
	} {
		for _, key := range keys {
			t.Run(key, func(t *testing.T) {
				in := "Earlier opaque-private-value\n" + key + "=opaque-private-value\n"
				got, err := Scrub([]byte(in), nil)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(got.Data), "opaque-private-value") || got.Replacements[category] != 2 {
					t.Fatalf("classification/count: %s %#v", got.Data, got.Replacements)
				}
			})
		}
	}
}

func TestNestedJSONAndScalarTypes(t *testing.T) {
	in := `{"name":["ann","b\\ob","c\"at"],"body":"{\"name\":\"ann\",\"tf_req_id\":\"private-reference\"}","id":123,"duration":123,"token":456,"echo":"456","nothing":null,"empty":"","message":"planning ann b\\ob c\"at"}`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(got.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out["id"] == float64(123) || out["duration"] != float64(123) || out["token"] == float64(456) {
		t.Fatalf("numeric contexts: %s", got.Data)
	}
	if _, ok := out["token"].(float64); !ok {
		t.Fatal("numeric secret type changed")
	}
	if !strings.Contains(out["message"].(string), "planning") || strings.Contains(out["message"].(string), " ann ") {
		t.Fatalf("token boundaries: %s", got.Data)
	}
	var nested map[string]string
	if err := json.Unmarshal([]byte(out["body"].(string)), &nested); err != nil {
		t.Fatal(err)
	}
	if nested["name"] != out["name"].([]any)[0] || nested["tf_req_id"] == "private-reference" {
		t.Fatalf("nested context: %s", got.Data)
	}
	if out["echo"] != json.Number(strings.TrimSpace(string(mustJSON(t, out["token"])))).String() {
		t.Fatal("numeric secret linkage")
	}
}

func TestNumericIdentifiersPreserveQuotedMeasurements(t *testing.T) {
	for _, in := range []string{
		`id=123 request_id="123" duration="123" count="123" elapsed=123 endpoint="https://private.internal/123"`,
		`id="123" request_id="123" duration="123" count="123" elapsed=123 endpoint="https://private.internal/123"`,
		`{"id":123,"request_id":"123","duration":"123","count":"123","elapsed":123,"endpoint":"https://private.internal/123"}`,
		`{"id":"123","request_id":"123","duration":"123","count":"123","elapsed":123,"endpoint":"https://private.internal/123"}`,
	} {
		t.Run(in, func(t *testing.T) {
			got, err := Scrub([]byte(in), nil)
			if err != nil {
				t.Fatal(err)
			}
			out := make(map[string]any)
			if strings.HasPrefix(in, "{") {
				if err := json.Unmarshal(got.Data, &out); err != nil {
					t.Fatal(err)
				}
				if out["elapsed"] != float64(123) {
					t.Fatalf("numeric measurement changed: %s", got.Data)
				}
				if id, numeric := out["id"].(float64); numeric {
					out["id"] = string(mustJSON(t, id))
				} else if !strings.Contains(in, `"id":"123"`) {
					t.Fatalf("numeric identifier lost its type: %s", got.Data)
				}
			} else {
				for _, f := range logfmt.ParseFields(string(got.Data), nil) {
					out[f.Key] = f.Val
				}
				if out["elapsed"] != "123" {
					t.Fatalf("numeric measurement changed: %s", got.Data)
				}
			}
			if out["duration"] != "123" || out["count"] != "123" {
				t.Fatalf("quoted measurements changed: %s", got.Data)
			}
			if out["id"] == "123" || out["request_id"] != out["id"] || !strings.HasSuffix(out["endpoint"].(string), "/"+out["id"].(string)) {
				t.Fatalf("numeric identifier linkage lost: %s", got.Data)
			}
		})
	}
}

func TestNumericIdentifierStringResourceKeys(t *testing.T) {
	got, err := Scrub([]byte(`id=123 addr='aws_instance.web["123"]' other='aws_instance.web[123]'`), nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := logfmt.ParseFields(string(got.Data), nil)
	id, _ := fields.Get("id")
	if id == "123" || !strings.Contains(string(got.Data), `["`+id+`"]`) || !strings.Contains(string(got.Data), "[123]") {
		t.Fatalf("string key linkage or count index changed: %s", got.Data)
	}
}

func TestNumericSecretsRetainPropagationAndMetadataConflicts(t *testing.T) {
	for _, in := range []string{`token=123 duration="123"`, `{"token":123,"duration":"123"}`} {
		got, err := Scrub([]byte(in), nil)
		if err != nil || strings.Contains(string(got.Data), "123") || got.Replacements["secret"] != 2 {
			t.Fatalf("numeric secret propagation: %s %#v %v", got.Data, got.Replacements, err)
		}
	}
	for _, assignment := range []string{"tf_req_id=123", `tf_req_duration_ms="123"`} {
		in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: " + assignment + "\ntoken=123\n"
		got, err := Scrub([]byte(in), nil)
		if err == nil || got.Data != nil || strings.Contains(err.Error(), "123") {
			t.Fatalf("numeric secret metadata conflict published output or content: %s %v", got.Data, err)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMetadataPositionsAndBodyDecoys(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: tf_req_id=private-reference tf_rpc=ReadResource tf_resource_type=aws_instance\n  tf_req_id=continuation-reference\n  http.response.body=\"{\\\"tf_req_id\\\":\\\"private-reference\\\",\\\"message\\\":\\\"tf_req_id=continuation-reference\\\"}\"\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got.Data)
	if strings.Count(out, "private-reference") != 1 || strings.Count(out, "continuation-reference") != 1 {
		t.Fatalf("positional exemption: %s", out)
	}
	if !strings.Contains(out, "tf_rpc=ReadResource tf_resource_type=aws_instance") {
		t.Fatal("structural metadata changed")
	}
}

func TestRequestMetadataRequiresParserFieldSyntax(t *testing.T) {
	const guid = "12345678-1234-4234-8234-123456789abc"
	const header = "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: "
	for _, prefix := range []string{header, header + "tf_req_id=scope\n  "} {
		for _, assignment := range []string{"tf_req_id = ", "tf_req_id= ", "tf_req_id\t=\t", "tf_req_id="} {
			for _, value := range []string{guid, `"` + guid + `"`} {
				in := prefix + assignment + value + "\n"
				t.Run(in, func(t *testing.T) {
					got, err := Scrub([]byte(in), nil)
					if err != nil {
						t.Fatal(err)
					}
					preserved := assignment == "tf_req_id="
					if strings.Contains(string(got.Data), guid) != preserved {
						t.Fatalf("request-ID exemption outside parser value: %s", got.Data)
					}
					before := logfmt.ParseFields(assignment+value, nil)
					outLine := strings.TrimSpace(strings.Split(strings.TrimSuffix(string(got.Data), "\n"), "\n")[strings.Count(prefix, "\n")])
					if strings.HasPrefix(outLine, header) {
						outLine = strings.TrimPrefix(outLine, header)
					}
					after := logfmt.ParseFields(outLine, nil)
					beforeID, beforeOK := before.Get("tf_req_id")
					afterID, afterOK := after.Get("tf_req_id")
					if beforeID != afterID || beforeOK != afterOK {
						t.Fatalf("parser metadata changed: before %q/%t after %q/%t", beforeID, beforeOK, afterID, afterOK)
					}
				})
			}
		}
	}
}

func TestWholeSecretsAndExplicitConflicts(t *testing.T) {
	for _, in := range []string{
		`token="arn:aws:iam::123456789012:user/alice" resource_arn="arn:aws:iam::123456789012:user/alice"`,
		`password="https://private.example/path" endpoint="https://private.example/path"`,
	} {
		got, err := Scrub([]byte(in), nil)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Fields(string(got.Data))
		a := strings.SplitN(parts[0], "=", 2)[1]
		b := strings.SplitN(parts[1], "=", 2)[1]
		if a != b || strings.Contains(a, "://") || strings.Contains(a, "arn:") || got.Replacements["secret"] != 2 {
			t.Fatalf("whole secret: %s", got.Data)
		}
	}
	metadata := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: tf_req_id=private-reference tf_req_duration_ms=5\n"
	for _, tc := range []struct {
		in    string
		extra []string
	}{
		{metadata + "token=private-reference\n", nil},
		{metadata, []string{"private-reference"}},
		{metadata, []string{"Received downstream response"}},
		{"2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: tf_provider_addr=\"registry.terraform.io/hashicorp/aws\" tf_req_duration_ms=5\ntoken=\"registry.terraform.io/hashicorp/aws\"\n", nil},
	} {
		got, err := Scrub([]byte(tc.in), tc.extra)
		if err == nil || got.Data != nil || got.Replacements != nil || got.Unsupported != 0 {
			t.Fatal("published metadata conflict")
		}
		if strings.Contains(err.Error(), "private-reference") {
			t.Fatal("error disclosed input")
		}
	}
}

func TestInputBoundariesAndMalformedText(t *testing.T) {
	for _, in := range [][]byte{{0xff}, {'a', 0, 'b'}} {
		got, err := Scrub(in, nil)
		if err == nil || got.Data != nil {
			t.Fatal("accepted binary input")
		}
	}
	for _, extra := range []string{"", "a\nb", "a\tb", string([]byte{0xff})} {
		if _, err := Scrub([]byte("text"), []string{extra}); err == nil {
			t.Fatal("accepted invalid explicit value")
		}
	}
	for _, in := range []string{"", "name=private\r\nEarlier private\r\n", "name=private\nEarlier private"} {
		got, err := Scrub([]byte(in), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(got.Data), "\n") != strings.Count(in, "\n") || strings.Count(string(got.Data), "\r") != strings.Count(in, "\r") || strings.HasSuffix(string(got.Data), "\n") != strings.HasSuffix(in, "\n") {
			t.Fatal("line framing changed")
		}
	}
	got, err := Scrub([]byte("{\"name\": broken\nname=\"unterminated\nEarlier multi word and multi wording\n"), []string{"multi word"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Unsupported != 2 || strings.Contains(string(got.Data), "Earlier multi word and") || !strings.Contains(string(got.Data), "multi wording") {
		t.Fatalf("malformed/literal handling: %s %#v", got.Data, got)
	}
}

func TestMultilineJSONBodyKeepsContext(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: HTTP Response: tf_req_id=scope\n{\n  \"name\": \"private-person\",\n  \"tf_req_id\": \"private-body-id\",\n  \"count\": 5\n}\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "private-person") || strings.Contains(string(got.Data), "private-body-id") || got.Unsupported != 0 {
		t.Fatalf("multiline body: %s (unsupported %d)", got.Data, got.Unsupported)
	}
	if !strings.Contains(string(got.Data), "tf_req_id=scope") || !strings.Contains(string(got.Data), `"count": 5`) {
		t.Fatal("body changed header or measurements")
	}
}

func TestProtectedQuotedSyntaxAndNumericContexts(t *testing.T) {
	in := `resource "aws_instance" "web" {` + "\n" + `name=aws_instance` + "\n" + `{"id":5,"count":5,"resource_key":0}`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Data), `resource "aws_instance"`) || !strings.Contains(string(got.Data), `"count":5`) {
		t.Fatalf("structural values changed: %s", got.Data)
	}
	boolean, err := Scrub([]byte(`{"token":true}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]bool
	if json.Unmarshal(boolean.Data, &values) != nil || values["token"] {
		t.Fatal("boolean secret type or value was not scrubbed")
	}
}

func TestHTTPTextBodyHasNoMetadataExemption(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: HTTP Response: tf_req_id=scope\nHTTP/1.1 200 OK\nContent-Type: text/plain\n\ntf_req_id=12345678-1234-4234-8234-123456789abc\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "12345678-1234-4234-8234-123456789abc") || !strings.Contains(string(got.Data), "tf_req_id=scope") {
		t.Fatalf("body metadata decoy: %s", got.Data)
	}
}

func TestWholeSecretOverridesNestedJSONSyntax(t *testing.T) {
	in := `{"token":"{\"name\":\"alice\"}","payload":"{\"name\":\"alice\"}"}`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if json.Unmarshal(got.Data, &out) != nil || out["token"] != out["payload"] || strings.Contains(out["token"], "{") || got.Replacements["secret"] != 2 {
		t.Fatalf("nested whole secret: %s", got.Data)
	}
}

func TestWholeHTTPHeaderCredentialsWithQuotes(t *testing.T) {
	for _, header := range []string{`Authorization: Bearer "opaque-private-value"`, `Cookie: session="opaque-private-value"`} {
		t.Run(strings.SplitN(header, ":", 2)[0], func(t *testing.T) {
			value := strings.SplitN(header, ": ", 2)[1]
			got, err := Scrub([]byte("Earlier "+value+"\n"+header+"\n"), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			alias := strings.TrimPrefix(lines[0], "Earlier ")
			if strings.Contains(string(got.Data), "opaque-private-value") || strings.Contains(alias, `"`) || lines[1] != strings.SplitN(header, ":", 2)[0]+": "+alias || got.Replacements["secret"] != 2 {
				t.Fatalf("complete credential not shared: %s %#v", got.Data, got.Replacements)
			}
		})
	}
}

func TestIdentifyingStringsDoNotAcquireSyntaxExemptions(t *testing.T) {
	for _, value := range []string{"alice=bob", "[1,2]", `{"count":5}`} {
		t.Run(value, func(t *testing.T) {
			encoded := string(mustJSON(t, value))
			in := "Earlier " + value + "\nname=" + encoded + "\n" + `{"name":` + encoded + `,"echo":` + encoded + `}`
			got, err := Scrub([]byte(in), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			alias := strings.TrimPrefix(lines[0], "Earlier ")
			var out map[string]string
			if json.Unmarshal([]byte(lines[2]), &out) != nil || out["name"] == value || out["name"] != alias || out["echo"] != alias || lines[1] != "name="+string(mustJSON(t, alias)) {
				t.Fatalf("syntax-like value left visible or inconsistent: %s", got.Data)
			}
		})
	}
}

func TestStringNullIsIdentifyingButJSONNullIsNot(t *testing.T) {
	for _, extra := range [][]string{nil, {"null"}} {
		in := `{"password":"null","name":"null","id":"null","missing":null}`
		if extra != nil {
			in = `{"name":"null","echo":"null","missing":null}`
		}
		got, err := Scrub([]byte(in), extra)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if json.Unmarshal(got.Data, &out) != nil || out["name"] == "null" || out["missing"] != nil {
			t.Fatalf("string/null distinction: %s", got.Data)
		}
		if extra == nil && (out["password"] != out["name"] || out["id"] != out["name"] || got.Replacements["secret"] != 3) {
			t.Fatalf("string secret linkage: %s", got.Data)
		}
		if extra != nil && (out["echo"] != out["name"] || got.Replacements["explicit"] != 2) {
			t.Fatalf("explicit null string: %s", got.Data)
		}
	}
}

func TestLifecycleEnvelopeUsesParsedKeys(t *testing.T) {
	in := `{"@level" : "info", "@timestamp" : "2026-09-08T00:00:00Z", "type" : "apply_start", "hook" : {"id_key" : "id", "id_value" : "private-resource"}}` + "\nname=apply_start\nEarlier private-resource\n" + `{"hook":{"id_value":"body-private"}}`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(strings.Split(string(got.Data), "\n")[0]), &envelope) != nil {
		t.Fatal("invalid envelope")
	}
	hook := envelope["hook"].(map[string]any)
	if hook["id_value"] == "private-resource" || hook["id_key"] != "id" || envelope["type"] != "apply_start" || strings.Contains(string(got.Data), "private-resource") || !strings.Contains(string(got.Data), "body-private") {
		t.Fatalf("incorrect lifecycle context: %s", got.Data)
	}
}

func TestWholeValuesPreserveStandaloneJSONDocuments(t *testing.T) {
	const document = `{"name":"alice"}`
	for _, key := range []string{"name", "token"} {
		in := document + "\n" + key + "=" + string(mustJSON(t, document)) + "\n"
		got, err := Scrub([]byte(in), nil)
		if key == "token" {
			if err == nil || got.Data != nil || got.Replacements != nil {
				t.Fatal("published whole-secret JSON syntax conflict")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		first := strings.Split(string(got.Data), "\n")[0]
		var out map[string]string
		if json.Unmarshal([]byte(first), &out) != nil || out["name"] == "alice" {
			t.Fatalf("standalone JSON syntax or name lost: %s", got.Data)
		}
	}
}

func TestWholeCredentialsSuppressIncidentalResourceSyntax(t *testing.T) {
	for _, in := range []string{`password="foo_bar.baz"`, `password=foo_bar.baz`, `Authorization: Bearer "foo_bar.baz"`} {
		got, err := Scrub([]byte(in), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got.Data), "foo_bar.baz") || got.Replacements["secret"] != 1 {
			t.Fatalf("whole credential remains: %s", got.Data)
		}
	}
	if got, err := Scrub([]byte("addr=\"foo_bar.baz\"\npassword=\"foo_bar.baz\"\n"), nil); err == nil || got.Data != nil {
		t.Fatal("accepted secret in mandatory address context")
	}
}
