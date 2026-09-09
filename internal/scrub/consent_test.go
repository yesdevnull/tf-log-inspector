package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConsentDescriptionDiscoversIsolatedPrincipal(t *testing.T) {
	const prefix = "Allow the application to access "
	const suffix = " on behalf of the signed in user"
	input, _ := json.Marshal(map[string]string{"adminConsentDescription": prefix + "Isolated SP_prod" + suffix, "userConsentDescription": prefix + "Isolated SP_prod" + suffix + ".", "echo": "Previously Isolated SP_prod"})
	out := scrubObject(t, string(input))
	alias := strings.TrimPrefix(out["echo"], "Previously ")
	if alias == "Isolated SP_prod" || out["adminConsentDescription"] != prefix+alias+suffix || out["userConsentDescription"] != prefix+alias+suffix+"." {
		t.Fatalf("isolated principal not linked: %#v", out)
	}
}

func TestConsentDescriptionFallbackIsOpaque(t *testing.T) {
	for _, value := range []string{"Custom approval for Isolated SP", "https://private.example.org", "private_sp.label", "Allow the application to access  on behalf of the signed in user", "Allow the application to access Isolated SP on behalf of the signed in user; contact Private Person"} {
		input, _ := json.Marshal(map[string]string{"adminConsentDescription": value, "userConsentDescription": value})
		out := scrubObject(t, string(input))
		if !strings.HasPrefix(out["adminConsentDescription"], "secret") || out["userConsentDescription"] != out["adminConsentDescription"] {
			t.Fatalf("unrecognised consent retained text: %#v", out)
		}
	}
}

func TestConsentDescriptionsInEscapedBodiesAndPlanChanges(t *testing.T) {
	const value = "Allow the application to access IsolatedSP on behalf of the signed in user"
	body, _ := json.Marshal(map[string]string{"adminConsentDescription": value, "userConsentDescription": "Custom approval for OtherSP"})
	input, _ := json.Marshal(map[string]string{"body": string(body)})
	got, err := Scrub(append(input, []byte("\nadmin_consent_description = \""+value+"\" -> \"Custom approval for OtherSP\"\n")...), nil)
	if err != nil || strings.Contains(string(got.Data), "IsolatedSP") || strings.Contains(string(got.Data), "OtherSP") || !strings.Contains(string(got.Data), "Allow the application to access name_") || !strings.Contains(string(got.Data), " -> \"secret_") {
		t.Fatalf("nested or plan consent leaked: %s %v", got.Data, err)
	}
}

func TestConsentDescriptionsHonourSecretPrecedenceAndEmptyValues(t *testing.T) {
	const description = "Allow the application to access PrivateSP on behalf of the signed in user"
	input, _ := json.Marshal(map[string]string{"adminConsentDescription": description, "token": description})
	out := scrubObject(t, string(input))
	if !strings.HasPrefix(out["token"], "secret_") || out["adminConsentDescription"] != out["token"] {
		t.Fatalf("whole credential reconstructed as consent: %#v", out)
	}
	const empty = `{"adminConsentDescription":null,"userConsentDescription":""}`
	got, err := Scrub([]byte(empty), nil)
	if err != nil || string(got.Data) != empty {
		t.Fatalf("empty consent changed: %s %v", got.Data, err)
	}
}

func TestUnquotedConsentFallbackIsOpaque(t *testing.T) {
	got, err := Scrub([]byte("user_consent_description=private_sp.label\n"), nil)
	if err != nil || !strings.HasPrefix(string(got.Data), "user_consent_description=secret_") || strings.Contains(string(got.Data), "private_sp") {
		t.Fatalf("unquoted consent fallback failed: %s %v", got.Data, err)
	}
}
