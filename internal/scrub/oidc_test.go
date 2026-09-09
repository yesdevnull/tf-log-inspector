package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTerraformOIDCSubjectSharesNames(t *testing.T) {
	for _, phase := range []string{"plan", "apply"} {
		subject := "organization:private-org:project:Private Project:workspace:private-workspace_prod:run_phase:" + phase
		input, _ := json.Marshal(map[string]string{"sub": subject, "echo": "Earlier " + subject, "organization": "private-org", "project": "Private Project", "workspace": "private-workspace_prod"})
		out := scrubObject(t, string(input))
		want := "organization:" + out["organization"] + ":project:" + out["project"] + ":workspace:" + out["workspace"] + ":run_phase:" + phase
		if out["sub"] != want || out["echo"] != "Earlier "+want || strings.Contains(out["sub"], "private-") || strings.Contains(out["sub"], "Private Project") {
			t.Fatalf("OIDC subject leaked or lost linkage: %#v", out)
		}
		got, err := Scrub([]byte("Earlier "+subject+"\n"), nil)
		if err != nil || strings.Contains(string(got.Data), "private-") || strings.Contains(string(got.Data), "Private Project") || !strings.HasSuffix(string(got.Data), ":run_phase:"+phase+"\n") {
			t.Fatalf("standalone OIDC subject not detected: %s %v", got.Data, err)
		}
	}
}

func TestTerraformOIDCSubjectHonoursSecretsAndStructure(t *testing.T) {
	const subject = "organization:private-org:project:Private Project:workspace:private-workspace:run_phase:apply"
	input, _ := json.Marshal(map[string]string{"sub": subject, "token": subject, "echo": "Earlier " + subject})
	out := scrubObject(t, string(input))
	if out["sub"] != out["token"] || out["echo"] != "Earlier "+out["token"] || !strings.HasPrefix(out["token"], "secret") {
		t.Fatalf("whole OIDC credential split: %#v", out)
	}
	got, err := Scrub([]byte(subject), []string{"organization"})
	if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), "organization") {
		t.Fatalf("OIDC structure conflict accepted: %s %v", got.Data, err)
	}
}

func TestTerraformOIDCSubjectInEscapedResourceKeys(t *testing.T) {
	subject := "organization:private_org-example:project:Private Project:workspace:private-workspace:run_phase:apply"
	body, _ := json.Marshal(map[string]string{"sub": subject})
	input, _ := json.Marshal(map[string]string{"body": string(body), "addr": `azurerm_federated_identity_credential.private_resource["` + subject + `"]`, "organization": "private_org-example", "project": "Private Project", "workspace": "private-workspace"})
	out := scrubObject(t, string(input))
	var decoded map[string]string
	if err := json.Unmarshal([]byte(out["body"]), &decoded); err != nil {
		t.Fatal(err)
	}
	want := "organization:" + out["organization"] + ":project:" + out["project"] + ":workspace:" + out["workspace"] + ":run_phase:apply"
	if decoded["sub"] != want || !strings.Contains(out["addr"], `["`+want+`"]`) || strings.Contains(out["addr"], "private_resource") {
		t.Fatalf("escaped OIDC subject lost resource linkage: %#v", out)
	}
}

func TestTerraformOIDCSubjectPreservesTrustWildcards(t *testing.T) {
	got, err := Scrub([]byte("organization:private-org:project:*:workspace:*:run_phase:*"), nil)
	if err != nil || strings.Contains(string(got.Data), "private-org") || !strings.HasSuffix(string(got.Data), ":project:*:workspace:*:run_phase:*") {
		t.Fatalf("OIDC trust wildcard altered: %s %v", got.Data, err)
	}
}
