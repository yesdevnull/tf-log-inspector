package scrub

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestAzureADGUIDSequenceSharesComponents(t *testing.T) {
	ids := []string{"12345678-1234-4234-8234-123456789abc", "23456789-2345-4345-8345-23456789abcd", "34567890-3456-4456-8456-34567890abcd"}
	sequence := strings.Join(ids, "-")
	input, err := json.Marshal(map[string]string{"message": "Earlier " + sequence, "id": sequence, "tenant_id": ids[0], "client_id": ids[1], "object_id": ids[2]})
	if err != nil {
		t.Fatal(err)
	}
	out := scrubObject(t, string(input))
	want := out["tenant_id"] + "-" + out["client_id"] + "-" + out["object_id"]
	if out["id"] != want || out["message"] != "Earlier "+want {
		t.Fatalf("GUID sequence lost linkage: %#v", out)
	}
	for i, key := range []string{"tenant_id", "client_id", "object_id"} {
		if !validGUID.MatchString(out[key]) || out[key] == ids[i] {
			t.Fatalf("invalid GUID replacement: %s", out[key])
		}
	}
	got, err := Scrub([]byte("Earlier "+sequence+"\n"), nil)
	if err != nil || strings.Contains(string(got.Data), ids[0]) || len(got.Data) != len("Earlier "+sequence+"\n") {
		t.Fatalf("standalone sequence was not reconstructed: %s %v", got.Data, err)
	}
}

func TestAzureURLsPreserveServicesAndShareNames(t *testing.T) {
	for _, suffix := range []string{"vault.azure.net", "managedhsm.azure.net", "privatelink.vaultcore.azure.net", "blob.core.windows.net", "dfs.core.windows.net", "file.core.windows.net", "queue.core.windows.net", "table.core.windows.net", "privatelink.blob.core.windows.net"} {
		t.Run(suffix, func(t *testing.T) {
			path := "/privatecontainer/privateobject"
			vault := strings.Contains(suffix, "vault") || strings.Contains(suffix, "managedhsm")
			if vault {
				path = "/secrets/privateobject/0123456789abcdef0123456789abcdef"
			}
			endpoint := "https://privateaccount." + suffix + path + "?api-version=7.4&sig=private%2Bsignature&si=private-policy"
			input, _ := json.Marshal(map[string]string{"endpoint": endpoint, "hostname": "privateaccount." + suffix, "account_name": "privateaccount", "object_name": "privateobject", "container_name": "privatecontainer", "version_id": "0123456789abcdef0123456789abcdef", "password": "private+signature", "policy_id": "private-policy"})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil {
				t.Fatal(err)
			}
			wantPath := "/" + out["container_name"] + "/" + out["object_name"]
			if vault {
				wantPath = "/secrets/" + out["object_name"] + "/" + out["version_id"]
			}
			if u.Hostname() != out["account_name"]+"."+suffix || out["hostname"] != u.Hostname() || u.Path != wantPath || u.Query().Get("api-version") != "7.4" {
				t.Fatalf("Azure structure or linkage changed: %#v", out)
			}
			if !vault && (u.Query().Get("sig") != out["password"] || u.Query().Get("si") != out["policy_id"]) {
				t.Fatalf("SAS linkage changed: %#v", out)
			}
			if strings.ContainsAny(out["account_name"], "_-") {
				t.Fatalf("identifying value or invalid account label: %#v", out)
			}
			for _, original := range []string{"privateaccount", "privateobject", "privatecontainer", "private-policy", "private%2Bsignature"} {
				if strings.Contains(out["endpoint"], original) {
					t.Fatalf("Azure identifier remains: %#v", out)
				}
			}
		})
	}
}

func TestAzureSASSignatureDetectedWithoutOtherFields(t *testing.T) {
	out := scrubObject(t, `{"url":"https://privateaccount.blob.core.windows.net/privatecontainer/privateblob?sv=2024-11-04&sp=r&sig=private%2Bsignature&si=private-policy"}`)
	u, err := url.Parse(out["url"])
	if err != nil || u.Query().Get("sig") == "private+signature" || u.Query().Get("si") == "private-policy" || u.Query().Get("sp") != "r" || u.Query().Get("sv") != "2024-11-04" {
		t.Fatalf("SAS was not scrubbed: %#v %v", out, err)
	}
}

func TestAzureSASIPRestrictionsShareAddressAliases(t *testing.T) {
	for _, restriction := range []string{"192.0.2.10", "192.0.2.10-192.0.2.20"} {
		input, _ := json.Marshal(map[string]string{"endpoint": "https://privateaccount.blob.core.windows.net/container/blob?sip=" + restriction + "&sig=signature", "ip": "192.0.2.10", "other_ip": "192.0.2.20"})
		out := scrubObject(t, string(input))
		u, err := url.Parse(out["endpoint"])
		want := out["ip"]
		if strings.Contains(restriction, "-") {
			want += "-" + out["other_ip"]
		}
		if err != nil || u.Query().Get("sip") != want || strings.Contains(out["endpoint"], "192.0.2.") {
			t.Fatalf("SAS IP restriction leaked or lost linkage: %#v %v", out, err)
		}
	}
}

func TestAzureSASDelegationIdentities(t *testing.T) {
	const id = "12345678-1234-4234-8234-123456789abc"
	for _, key := range []string{"skoid", "sktid", "saoid", "suoid", "sduoid", "skdutid", "scid"} {
		t.Run(key, func(t *testing.T) {
			endpoint := "https://privateaccount.blob.core.windows.net/container/blob?sig=signature&" + key + "=" + strings.ReplaceAll(id, "-", "%2D")
			input, _ := json.Marshal(map[string]string{"endpoint": endpoint})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil || u.Query().Get(key) == id || !validGUID.MatchString(u.Query().Get(key)) {
				t.Fatalf("delegation identity retained or malformed: %#v %v", out, err)
			}
			input, _ = json.Marshal(map[string]string{"endpoint": endpoint, "object_id": id})
			out = scrubObject(t, string(input))
			u, err = url.Parse(out["endpoint"])
			if err != nil || u.Query().Get(key) != out["object_id"] {
				t.Fatalf("delegation identity lost linkage: %#v %v", out, err)
			}
		})
	}
}

func TestAzureTableSASKeysShareAliases(t *testing.T) {
	input := `{"url":"https://privateaccount.table.core.windows.net/privatetable?sig=signature&spk=customer-acme&srk=alice%40example.com&epk=customer-zeta&erk=private-record","start_name":"customer-acme","email":"alice@example.com","end_name":"customer-zeta","record_id":"private-record"}`
	out := scrubObject(t, input)
	u, err := url.Parse(out["url"])
	if err != nil {
		t.Fatal(err)
	}
	for key, field := range map[string]string{"spk": "start_name", "srk": "email", "epk": "end_name", "erk": "record_id"} {
		if u.Query().Get(key) != out[field] {
			t.Fatalf("Table SAS key lost linkage: %#v", out)
		}
	}
	out = scrubObject(t, `{"url":"https://privateaccount.table.core.windows.net/privatetable?sig=signature&spk=customer-acme&srk=alice%40example.com&epk=customer-zeta&erk=private-record"}`)
	for _, value := range []string{"customer-acme", "alice", "customer-zeta", "private-record"} {
		if strings.Contains(out["url"], value) {
			t.Fatalf("Table SAS key retained: %#v", out)
		}
	}
}

func TestAzureBareEndpointsAndVaultCollections(t *testing.T) {
	got, err := Scrub([]byte("Connecting to privateaccount.blob.core.windows.net\n"), nil)
	if err != nil || strings.Contains(string(got.Data), "privateaccount") || !strings.Contains(string(got.Data), ".blob.core.windows.net") {
		t.Fatalf("bare endpoint not scrubbed: %s %v", got.Data, err)
	}
	for _, collection := range []string{"secrets", "keys", "certificates"} {
		out := scrubObject(t, `{"endpoint":"https://privatevault.vault.azure.net/`+collection+`?api-version=7.4"}`)
		u, err := url.Parse(out["endpoint"])
		if err != nil || u.Path != "/"+collection {
			t.Fatalf("vault collection changed: %#v %v", out, err)
		}
	}
}

func TestAzureGUIDSequenceResourceKeysPreserveCaseIdentity(t *testing.T) {
	const first = "aa000000-0000-4000-8000-000000000000"
	const rest = "-bb000000-0000-4000-8000-000000000000-cc000000-0000-4000-8000-000000000000"
	input, _ := json.Marshal(map[string]string{"addr": `azuread_application.example["` + first + rest + `"]`, "other_addr": `azuread_application.example["` + strings.ToUpper(first) + rest + `"]`, "tenant_id": first, "other_id": strings.ToUpper(first)})
	out := scrubObject(t, string(input))
	if strings.EqualFold(out["tenant_id"], out["other_id"]) || out["addr"] == out["other_addr"] || !strings.Contains(out["addr"], out["tenant_id"]+"-") || !strings.Contains(out["other_addr"], out["other_id"]+"-") {
		t.Fatalf("resource GUID spelling identities collapsed: %#v", out)
	}
}

func TestAzureEndpointResourceKeysKeepAddressLabels(t *testing.T) {
	out := scrubObject(t, `{"addr":"azurerm_key_vault_secret.private_resource[\"privatevault.vault.azure.net\"]","vault_name":"privatevault"}`)
	if !strings.HasPrefix(out["addr"], "azurerm_key_vault_secret.") || !strings.HasSuffix(out["addr"], `["`+out["vault_name"]+`.vault.azure.net"]`) || strings.Contains(out["addr"], "private_resource") || out["vault_name"] == "privatevault" {
		t.Fatalf("resource address linkage changed: %#v", out)
	}
}

func TestAzureGUIDSequenceSecretsMetadataAndBoundaries(t *testing.T) {
	const sequence = "12345678-1234-4234-8234-123456789abc-23456789-2345-4345-8345-23456789abcd-34567890-3456-4456-8456-34567890abcd"
	for _, value := range []string{sequence, "https://privatevault.vault.azure.net/secrets/privateobject/version"} {
		input, _ := json.Marshal(map[string]string{"message": "Earlier " + value, "id": value, "token": value})
		out := scrubObject(t, string(input))
		if out["message"] != "Earlier "+out["token"] || out["id"] != out["token"] || !strings.HasPrefix(out["token"], "secret") {
			t.Fatalf("whole secret mapping split: %#v", out)
		}
	}
	input := "2026-09-08T00:00:00.000Z [TRACE] provider.azuread: Sending request downstream: tf_req_id=" + sequence + " resource_id=" + sequence + "\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil || !strings.Contains(string(got.Data), "tf_req_id="+sequence) || strings.Contains(string(got.Data), "resource_id="+sequence) {
		t.Fatalf("metadata exception changed: %s %v", got.Data, err)
	}
	for _, value := range []string{"prefix-" + sequence, sequence + "-suffix", sequence + "-45678901-4567-4567-8567-45678901abcd"} {
		got, err := Scrub([]byte(value), nil)
		if err != nil || string(got.Data) != value {
			t.Fatalf("partial GUID sequence match: %s %v", got.Data, err)
		}
	}
}

func TestAzureEscapingAndStructuralConflicts(t *testing.T) {
	out := scrubObject(t, `{"endpoint":"https://privatevault.vault.azure.net/%73ecrets/private%2Dobject/version?api-version=7.4","object_name":"private-object","host":"privatevault.vault.azure.net."}`)
	u, err := url.Parse(out["endpoint"])
	if err != nil || !strings.HasPrefix(u.Path, "/secrets/"+out["object_name"]+"/") || out["host"] != u.Hostname()+"." {
		t.Fatalf("escaped Azure linkage lost: %#v %v", out, err)
	}
	for _, value := range []string{"vault", "secrets"} {
		input := `{"endpoint":"https://privatevault.vault.azure.net/%73ecrets/object","token":"` + value + `"}`
		got, err := Scrub([]byte(input), nil)
		if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), value) {
			t.Fatalf("structural secret conflict leaked: %s %v", got.Data, err)
		}
	}
}

func TestAzureLookalikeDomainsUseGenericScrubbing(t *testing.T) {
	for _, host := range []string{"privatevault.vault.azure.net.attacker.test", "prefix.privatevault.vault.azure.net", "privateaccount.blob.core.windows.net.attacker.test"} {
		input, _ := json.Marshal(map[string]string{"endpoint": "https://" + host + "/secrets/object"})
		out := scrubObject(t, string(input))
		u, err := url.Parse(out["endpoint"])
		if err != nil || !strings.HasSuffix(u.Hostname(), ".example.invalid") || strings.Contains(u.Hostname(), ".azure.net") || strings.Contains(u.Hostname(), ".core.windows.net") {
			t.Fatalf("lookalike treated as Azure: %#v %v", out, err)
		}
	}
}
