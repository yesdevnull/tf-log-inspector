package scrub

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestPublisherDomainSharesTenantName(t *testing.T) {
	for _, domain := range []string{"PrivateOrg.onmicrosoft.com", "PrivateOrg.example.net"} {
		standalone := scrubObject(t, `{"publisherDomain":"`+domain+`"}`)
		if strings.Contains(standalone["publisherDomain"], "PrivateOrg") {
			t.Fatalf("publisher domain retained tenant: %#v", standalone)
		}
		input, _ := json.Marshal(map[string]string{"publisherDomain": domain, "organization": "PrivateOrg", "echo": "Publisher " + domain})
		out := scrubObject(t, string(input))
		if strings.Contains(out["publisherDomain"], "PrivateOrg") || !strings.HasPrefix(out["publisherDomain"], out["organization"]+".") || out["echo"] != "Publisher "+out["publisherDomain"] {
			t.Fatalf("publisher domain leaked or lost linkage: %#v", out)
		}
	}
	got, err := Scrub([]byte("Publisher PrivateOrg.onmicrosoft.com"), nil)
	if err != nil || strings.Contains(string(got.Data), "PrivateOrg") || !strings.HasSuffix(string(got.Data), ".onmicrosoft.com") {
		t.Fatalf("tenant domain not detected: %s %v", got.Data, err)
	}
}

func TestRecognisableTokensAreWholeSecrets(t *testing.T) {
	tokens := []string{"github_pat_" + strings.Repeat("A1", 40), "syntheticID1234.atlasv1." + strings.Repeat("aB9", 30), "ghs_12345_eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.syntheticSignature"}
	for _, prefix := range []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"} {
		tokens = append(tokens, prefix+strings.Repeat("aB9", 12))
	}
	for _, token := range tokens {
		input, _ := json.Marshal(map[string]string{"value": token, "message": "Credential " + token + ".", "name": token})
		out := scrubObject(t, string(input))
		if !strings.HasPrefix(out["value"], "secret_") || out["value"] != out["name"] || out["message"] != "Credential "+out["value"]+"." {
			t.Fatalf("credential not replaced whole: %#v", out)
		}
		got, err := Scrub([]byte("Credential "+token+"."), nil)
		if err != nil || !strings.HasPrefix(string(got.Data), "Credential secret_") || strings.Contains(string(got.Data), token) || !strings.HasSuffix(string(got.Data), ".") {
			t.Fatalf("standalone token not detected: %s %v", got.Data, err)
		}
	}
}

func TestServiceTokensInEscapedBodiesAndResourceKeys(t *testing.T) {
	const token = "ghs_12345_eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ.syntheticSignature"
	body, _ := json.Marshal(map[string]string{"value": token})
	input, _ := json.Marshal(map[string]string{"body": string(body), "value": token, "addr": `terraform_data.private_resource["` + token + `"]`})
	out := scrubObject(t, string(input))
	var decoded map[string]string
	if err := json.Unmarshal([]byte(out["body"]), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out["value"], "secret_") || decoded["value"] != out["value"] || !strings.Contains(out["addr"], `["`+out["value"]+`"]`) || strings.Contains(out["addr"], "private_resource") {
		t.Fatalf("escaped credential lost resource linkage: %#v", out)
	}
	for _, input := range []string{`{"` + token + `":"value"}`, "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: tf_req_id=" + token} {
		got, err := Scrub([]byte(input), nil)
		if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), token) {
			t.Fatalf("protected credential conflict accepted: %s %v", got.Data, err)
		}
	}
}

func TestConsentDescriptionsShareServicePrincipalName(t *testing.T) {
	out := scrubObject(t, `{"userConsentDescription":"Allow Private Service to access your data.","adminConsentDescription":"Allow Private Service to access all data.","displayName":"Private Service"}`)
	if out["displayName"] == "Private Service" || out["userConsentDescription"] != "Allow "+out["displayName"]+" to access your data." || out["adminConsentDescription"] != "Allow "+out["displayName"]+" to access all data." {
		t.Fatalf("consent description lost name linkage: %#v", out)
	}
}

func TestServiceTokensInEncodedURLs(t *testing.T) {
	const token = "ghp_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	encoded := "ghp_" + strings.Repeat("%41", 36)
	for _, suffix := range []string{"/?value=" + encoded, "/#" + encoded, "/" + encoded} {
		out := scrubObject(t, `{"value":"https://example.com`+suffix+`"}`)
		decoded, err := url.PathUnescape(out["value"])
		if err != nil || strings.Contains(decoded, token) || !strings.Contains(decoded, "secret_") {
			t.Fatalf("encoded credential retained or misclassified: %#v %v", out, err)
		}
	}
}
