package scrub

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestTerraformOutputPreservesIDField(t *testing.T) {
	for _, message := range []string{"Read complete after 2s", "Refreshing state...", "Creation complete after 2s"} {
		t.Run(message, func(t *testing.T) {
			input := "name=id\ndata.aws_instance.example: " + message + " [id=private-instance]\nEarlier private-instance\n"
			got, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			_, value, ok := strings.Cut(lines[1], " [id=")
			if !ok || !strings.HasSuffix(value, "]") || !strings.Contains(lines[1], ": "+message) {
				t.Fatalf("Terraform output structure changed: %s", got.Data)
			}
			value = strings.TrimSuffix(value, "]")
			if value == "private-instance" || lines[2] != "Earlier "+value {
				t.Fatalf("Terraform ID was retained or lost linkage: %s", got.Data)
			}
		})
	}
}

func TestTerraformOutputIDValues(t *testing.T) {
	for _, tc := range []struct {
		name, value, logged string
	}{
		{"numeric", "123", "123"},
		{"quoted", "private-instance", `"private-instance"`},
		{"spaces", "private instance east", "private instance east"},
		{"opening bracket", "private[instance", "private[instance"},
		{"closing bracket", "private]instance", "private]instance"},
		{"IPv6", "https://[2001:db8::1]", "https://[2001:db8::1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "data.aws_instance.example: Read complete after 123s [id=" + tc.logged + "]\n" + string(mustJSON(t, map[string]string{"id": tc.value, "duration": "123"}))
			got, err := Scrub([]byte(input), nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(got.Data), "\n")
			var fields map[string]string
			if err := json.Unmarshal([]byte(lines[1]), &fields); err != nil {
				t.Fatal(err)
			}
			value := fields["id"]
			if value == tc.value || fields["duration"] != "123" {
				t.Fatalf("ID retained or measurement changed: %s", got.Data)
			}
			if tc.name == "IPv6" {
				u, err := url.Parse(value)
				if err != nil {
					t.Fatal(err)
				}
				if ip, err := netip.ParseAddr(u.Hostname()); err != nil || !ip.Is6() {
					t.Fatalf("IPv6 URL lost its authority: %s", got.Data)
				}
			}
			if tc.name == "quoted" {
				value = string(mustJSON(t, value))
			}
			if !strings.HasSuffix(lines[0], ": Read complete after 123s [id="+value+"]") {
				t.Fatalf("bracketed ID lost structure or linkage: %s", got.Data)
			}
		})
	}
}

func TestTerraformOutputDiscoversWholeIDWithSpaces(t *testing.T) {
	input := "data.aws_instance.example: Refreshing state... [id=private instance east]\nEarlier private instance east\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(got.Data), "\n")
	_, value, ok := strings.Cut(lines[0], " [id=")
	if !ok || !strings.HasSuffix(value, "]") {
		t.Fatalf("bracketed ID lost structure: %s", got.Data)
	}
	value = strings.TrimSuffix(value, "]")
	if strings.ContainsAny(value, " \t") || strings.Contains(value, "private") || lines[1] != "Earlier "+value {
		t.Fatalf("bracketed ID was only partly scrubbed: %s", got.Data)
	}
}

func TestTerraformOutputRejectsSecretFieldKey(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		input := "data.aws_instance.example: Read complete after 2s [id=private-instance]\n"
		var extra []string
		if explicit {
			extra = []string{"id"}
		} else {
			input += "token=id\n"
		}
		got, err := Scrub([]byte(input), extra)
		if err == nil || len(got.Data) != 0 || !strings.Contains(err.Error(), "field key conflict") {
			t.Fatalf("protected field key conflict published output: %s %v", got.Data, err)
		}
	}
}

func TestBracketedFieldsDoNotHideCredentials(t *testing.T) {
	for _, key := range []string{"message", "id"} {
		input := "Earlier private-secret\nmessage [" + key + "=thing token=private-secret]\n"
		got, err := Scrub([]byte(input), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got.Data), "private-secret") {
			t.Fatalf("bracketed field hid a credential: %s", got.Data)
		}
	}
}

func TestTerraformOutputScrubsKeyVaultURL(t *testing.T) {
	for _, endpoint := range []string{
		"https://privatevault.vault.azure.net/secrets/private-secret",
		"https://privatevault.vault.azure.net/secrets/private-secret/0123456789abcdef0123456789abcdef",
		"https://KEY_VAULT_NAME.vault.azure.net/secrets/SECRET_NAME/VERSION",
	} {
		for _, quoted := range []bool{false, true} {
			message := "data.azurerm_key_vault_secret.example: Read complete after 2s [id=" + endpoint + "]"
			input := message
			if quoted {
				input = string(mustJSON(t, map[string]string{"@message": message}))
			}
			t.Run(input, func(t *testing.T) {
				got, err := Scrub([]byte(input+"\nEarlier "+endpoint+"\n"), nil)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(string(got.Data), "\n")
				output := lines[0]
				if quoted {
					var envelope map[string]string
					if err := json.Unmarshal([]byte(output), &envelope); err != nil {
						t.Fatal(err)
					}
					output = envelope["@message"]
				}
				_, value, ok := strings.Cut(output, " [id=")
				if !ok || !strings.HasSuffix(value, "]") {
					t.Fatalf("Terraform output structure changed: %s", got.Data)
				}
				value = strings.TrimSuffix(value, "]")
				if !strings.Contains(value, ".vault.azure.net/secrets/") || lines[1] != "Earlier "+value {
					t.Fatalf("Key Vault URL lost structure or linkage: %s", got.Data)
				}
				for _, original := range []string{"privatevault", "private-secret", "0123456789abcdef0123456789abcdef", "KEY_VAULT_NAME", "SECRET_NAME", "VERSION"} {
					if strings.Contains(string(got.Data), original) {
						t.Fatalf("Key Vault identifier retained: %s", got.Data)
					}
				}
			})
		}
	}
}
