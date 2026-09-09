package scrub

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func scrubObject(t *testing.T, input string) map[string]string {
	t.Helper()
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(got.Data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestEmailAliases(t *testing.T) {
	out := scrubObject(t, `{"email":"alice@private.test","other":"bob@private.test","echo":"alice@private.test","name":"alice@private.test"}`)
	for _, key := range []string{"email", "other"} {
		parsed, err := mail.ParseAddress(out[key])
		if err != nil || !strings.HasSuffix(parsed.Address, "@example.invalid") {
			t.Fatalf("invalid alias: %q", out[key])
		}
	}
	if out["email"] == out["other"] || out["email"] != out["echo"] || out["email"] != out["name"] {
		t.Fatal("email identity lost")
	}
}

func TestIPAliases(t *testing.T) {
	out := scrubObject(t, `{"ip":"192.0.2.45","echo":"192.0.2.45","other":"10.0.0.1","ipv6":"2001:db8::a","v6echo":"2001:db8::a","v6other":"fd00::1","invalid":"999.0.0.1","duration":"42"}`)
	for _, keys := range [][]string{{"ip", "other"}, {"ipv6", "v6other"}} {
		for _, key := range keys {
			addr, err := netip.ParseAddr(out[key])
			if err != nil {
				t.Fatal(err)
			}
			prefix := "10.0.0.0/8"
			if addr.Is6() {
				prefix = "fd00::/8"
			}
			if !netip.MustParsePrefix(prefix).Contains(addr) || out[key] == "10.0.0.1" || out[key] == "fd00::1" {
				t.Fatal("invalid/reserved fake address")
			}
		}
		if out[keys[0]] == out[keys[1]] {
			t.Fatal("addresses collapsed")
		}
	}
	if out["ip"] != out["echo"] || out["ipv6"] != out["v6echo"] || out["invalid"] != "999.0.0.1" || out["duration"] != "42" {
		t.Fatal("address matching changed unrelated values")
	}
}

func TestURLAndHostnameShareComponents(t *testing.T) {
	out := scrubObject(t, `{"endpoint":"https://alice:private-password@alice.internal:8443/users/alice?username=alice&count=5","echo":"https://alice:private-password@alice.internal:8443/users/alice?username=alice&count=5","hostname":"alice.internal","name":"alice","password":"private-password","host":"private-host"}`)
	u, err := url.Parse(out["endpoint"])
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.Scheme != "https" || u.Port() != "8443" || u.Hostname() != out["hostname"] || !strings.HasSuffix(u.Hostname(), ".example.invalid") || !strings.HasPrefix(u.Hostname(), out["name"]+".") || u.User.Username() != out["name"] || pw != out["password"] || u.Query().Get("username") != out["name"] || u.Query().Get("count") != "5" || !strings.HasSuffix(u.Path, "/"+out["name"]) || out["echo"] != out["endpoint"] {
		t.Fatalf("URL linkage or structure lost: %#v", out)
	}
	if !strings.HasSuffix(out["host"], ".example.invalid") || strings.Contains(out["name"], "_") {
		t.Fatal("hostname alias syntax")
	}
}

func TestWholeSecretComposite(t *testing.T) {
	for _, value := range []string{"https://alice.internal/users/alice", "arn:aws:iam::123456789012:user/alice"} {
		input, _ := json.Marshal(map[string]string{"token": value, "endpoint": value, "resource_arn": value})
		out := scrubObject(t, string(input))
		if out["token"] != out["endpoint"] || out["token"] != out["resource_arn"] || strings.Contains(out["token"], "alice") || strings.Contains(out["token"], ":") {
			t.Fatal("inconsistent secret alias")
		}
	}
}

func TestAWSIdentifiers(t *testing.T) {
	out := scrubObject(t, `{"resource_arn":"arn:aws:iam::123456789012:user/alice","echo":"arn:aws:iam::123456789012:user/alice","bucket":"arn:aws:s3:::private-bucket/dir/object.txt","role":"arn:aws:lambda:us-east-1:123456789012:function:alice","name":"alice","account_id":"123456789012"}`)
	if out["resource_arn"] != "arn:aws:iam::"+out["account_id"]+":user/"+out["name"] || out["resource_arn"] != out["echo"] || out["role"] != "arn:aws:lambda:us-east-1:"+out["account_id"]+":function:"+out["name"] {
		t.Fatalf("ARN component linkage: %#v", out)
	}
	if len(out["account_id"]) != 12 || strings.Trim(out["account_id"], "0123456789") != "" || out["account_id"] == "123456789012" {
		t.Fatal("account syntax")
	}
	if !strings.HasPrefix(out["bucket"], "arn:aws:s3:::") || strings.Contains(out["bucket"], "private-bucket") {
		t.Fatal("bucket ARN not scrubbed")
	}
}

func TestCloudResourcePaths(t *testing.T) {
	for _, value := range []string{"/subscriptions/12345678-1234-4234-8234-123456789abc/resourceGroups/private-solo/providers/Microsoft.Compute/virtualMachines/private-vm", "projects/private-solo/locations/private-region/services/private-service"} {
		input, _ := json.Marshal(map[string]string{"unknown": value})
		out := scrubObject(t, string(input))
		if strings.Contains(out["unknown"], "private-") {
			t.Fatal("cloud identifying segment remains")
		}
	}
	out := scrubObject(t, `{"azure":"/subscriptions/12345678-1234-4234-8234-123456789abc/resourceGroups/private-group/providers/Microsoft.Network/virtualNetworks/private-net/subnets/private-subnet","subscription_id":"12345678-1234-4234-8234-123456789abc","name":"private-group","network_name":"private-net","subnet_name":"private-subnet","gcp":"//compute.googleapis.com/projects/private-project/zones/private-zone/instances/private-instance","project":"private-project","zone_name":"private-zone","instance_name":"private-instance"}`)
	wantAzure := "/subscriptions/" + out["subscription_id"] + "/resourceGroups/" + out["name"] + "/providers/Microsoft.Network/virtualNetworks/" + out["network_name"] + "/subnets/" + out["subnet_name"]
	wantGCP := "//compute.googleapis.com/projects/" + out["project"] + "/zones/" + out["zone_name"] + "/instances/" + out["instance_name"]
	if out["azure"] != wantAzure || out["gcp"] != wantGCP || strings.Contains(out["azure"], "private-") || strings.Contains(out["gcp"], "private-") {
		t.Fatalf("cloud path structure/linkage: %#v", out)
	}
}

func TestLocalPaths(t *testing.T) {
	out := scrubObject(t, `{"posix":"/home/alice/private-state.tfstate","windows":"C:\\Users\\alice\\private-state.tfstate","name":"alice","echo":"/home/alice/private-state.tfstate","spaces":"/private directory/another person/state.tf","relative":"relative/file.txt"}`)
	if !strings.HasPrefix(out["posix"], "/") || !strings.HasPrefix(out["windows"], `C:\`) || !strings.HasSuffix(out["posix"], ".tfstate") || !strings.HasSuffix(out["windows"], ".tfstate") || !strings.Contains(out["posix"], "/"+out["name"]+"/") || !strings.Contains(out["windows"], `\`+out["name"]+`\`) || out["echo"] != out["posix"] || strings.Contains(out["posix"], "private-state") || strings.Contains(out["spaces"], "person") || out["relative"] != "relative/file.txt" {
		t.Fatalf("path structure/linkage: %#v", out)
	}
}

func TestPEMPrivateKeyPayload(t *testing.T) {
	input := "before\r\n-----BEGIN RSA PRIVATE KEY-----\r\nU1lOVEhFVElDX1BSSVZBVEU=\r\nUEFZTE9BRF9PTkxZ\r\n-----END RSA PRIVATE KEY-----\r\nafter"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got.Data)
	if strings.Contains(out, "U1lOVEhFVElDX1BSSVZBVEU=") || strings.Contains(out, "UEFZTE9BRF9PTkxZ") || !strings.Contains(out, "-----BEGIN RSA PRIVATE KEY-----\r\n") || !strings.Contains(out, "-----END RSA PRIVATE KEY-----\r\n") || strings.Count(out, "\r\n") != strings.Count(input, "\r\n") || !strings.HasSuffix(out, "after") || got.Replacements["secret"] != 1 {
		t.Fatalf("PEM payload/framing: %q", out)
	}
	public := "-----BEGIN PUBLIC KEY-----\nU1lOVEhFVElD\n-----END PUBLIC KEY-----\n"
	got, err = Scrub([]byte(public), nil)
	if err != nil || string(got.Data) != public {
		t.Fatal("public-key framing changed")
	}
}

func TestCompositeSyntaxAndEscaping(t *testing.T) {
	for _, value := range []string{"https://[2001:db8::a]", "https://[2001:db8::b]:443/path", "https://private.internal/projects/private-project/locations/private-region/services/private-service", "https://alice%20smith:pass%40word@private.internal/?username=alice%20smith"} {
		t.Run(value, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"endpoint": value, "echo": value})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil || u.Hostname() == "" || strings.Contains(out["endpoint"], "private") || strings.Contains(out["endpoint"], "2001:db8") || out["endpoint"] != out["echo"] {
				t.Fatalf("invalid URL result: %#v", out)
			}
			if strings.Contains(value, "/projects/") && (!strings.Contains(u.Path, "/projects/") || !strings.Contains(u.Path, "/locations/") || !strings.Contains(u.Path, "/services/")) {
				t.Fatal("cloud collections changed in URL")
			}
		})
	}
	got, err := Scrub([]byte(`{"account_id":123456789012,"arn":"arn:aws:iam::123456789012:user/alice","count":123456789012}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.Data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields["account_id"]) != 12 || string(fields["account_id"]) == "123456789012" || string(fields["count"]) != "123456789012" {
		t.Fatal("account numeric context changed")
	}
}

func TestIPCanonicalReservationAndExhaustion(t *testing.T) {
	out := scrubObject(t, `{"one":"2001:db8::1","reserved":"fd00:0000:0000:0000:0000:0000:0000:0000"}`)
	if out["one"] == "fd00::" {
		t.Fatal("allocated original address in another spelling")
	}
	for _, cidr := range []string{"10.0.0.0/32", "fd00::/128"} {
		prefix := netip.MustParsePrefix(cidr)
		cursor := prefix.Addr()
		if _, err := freshIP(prefix, &cursor, map[string]bool{cursor.String(): true}); err == nil {
			t.Fatal("exhaustion reused an address")
		}
	}
}

func TestHostnameSharedWithResourceLabel(t *testing.T) {
	out := scrubObject(t, `{"host":"alice","addr":"aws_instance.alice","hostname":"private-host","token":"private-secret","endpoint":"https://private-secret.internal"}`)
	if out["addr"] != "aws_instance."+out["host"] || strings.ContainsAny(out["host"], "._") || !strings.HasSuffix(out["hostname"], ".example.invalid") {
		t.Fatalf("host/resource syntax conflict: %#v", out)
	}
	u, err := url.Parse(out["endpoint"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Hostname(), out["token"]+".") || strings.Contains(out["token"], "_") || strings.Contains(out["token"], "private") {
		t.Fatal("secret hostname label linkage/syntax")
	}
}

func TestSecretIPCannotDamageURLSyntax(t *testing.T) {
	got, err := Scrub([]byte(`{"token":"2001:db8::a","endpoint":"https://[2001:db8::a]:8443/path"}`), nil)
	if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), "2001") {
		t.Fatal("invalid URL should fail without output/content")
	}
}

func TestStandaloneAccountAndMalformedPayloads(t *testing.T) {
	out := scrubObject(t, `{"account_id":"123456789012","account":"234567890123","count":"123456789012","encoded":"cHJpdmF0ZS1uYW1l","empty":""}`)
	if len(out["account_id"]) != 12 || strings.Trim(out["account_id"], "0123456789") != "" || out["account_id"] == "123456789012" || out["account"] == out["account_id"] || out["encoded"] != "cHJpdmF0ZS1uYW1l" || out["empty"] != "" {
		t.Fatal("standalone account detection/syntax")
	}
	got, err := Scrub([]byte("Malformed {\"email\":\"alice@private.test\"\n"), nil)
	if err != nil || strings.Contains(string(got.Data), "alice@private.test") {
		t.Fatal("malformed input lost pattern discovery")
	}
}

func TestCompositePartsAreNotTerraformResourceTypes(t *testing.T) {
	input := "Earlier /home/alice/private_state.tfstate\npath=/home/alice/private_state.tfstate\narn=arn:aws:s3:::private_bucket/private_object.txt\n"
	got, err := Scrub([]byte(input), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"alice", "private_state", "private_bucket", "private_object"} {
		if strings.Contains(string(got.Data), target) {
			t.Fatalf("path/ARN segment protected as resource type: %s", got.Data)
		}
	}
	if strings.Count(string(got.Data), ".tfstate") != 2 || !strings.Contains(string(got.Data), "arn:aws:s3:::") {
		t.Fatal("composite structure lost")
	}
}

func TestURLUserinfoAndNetworkPunctuation(t *testing.T) {
	got, err := Scrub([]byte("Connect 192.0.2.6:443 and 192.0.2.7."), nil)
	if err != nil || strings.Contains(string(got.Data), "192.0.2") {
		t.Fatal("punctuated network address not discovered")
	}
	out := scrubObject(t, `{"endpoint":"https://alice@example.test:private-password@private.internal/path","email":"alice@example.test","password":"private-password","ip":"192.0.2.5","message":"Connected to 192.0.2.5:443 and 192.0.2.5."}`)
	u, err := url.Parse(out["endpoint"])
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.User.Username() != out["email"] || pw != out["password"] || strings.Contains(out["endpoint"], "private") || out["message"] != "Connected to "+out["ip"]+":443 and "+out["ip"]+"." {
		t.Fatalf("network punctuation/userinfo lost: %#v", out)
	}
}

func TestURLQueryPropagatesLaterDiscoveries(t *testing.T) {
	out := scrubObject(t, `{"endpoint":"https://private.internal/?hint=hello%20alice&opaque=private-value&count=12","name":"alice","token":"private-value"}`)
	u, err := url.Parse(out["endpoint"])
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("hint") != "hello "+out["name"] || u.Query().Get("opaque") != out["token"] || u.Query().Get("count") != "12" {
		t.Fatalf("URL suppressed discovered values: %#v", out)
	}
}

func TestURLFragmentsPropagateNamesAndSecrets(t *testing.T) {
	for _, fragment := range []string{"alice/private-token", "%61lice/private%2Dtoken", "prefix%20value/alice/private-token"} {
		t.Run(fragment, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"endpoint": "https://private.internal/#" + fragment, "name": "alice", "token": "private-token"})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil {
				t.Fatal(err)
			}
			want := out["name"] + "/" + out["token"]
			if strings.HasPrefix(fragment, "prefix") {
				want = "prefix value/" + want
			}
			if u.Fragment != want || strings.Contains(out["endpoint"], "private-token") || strings.Contains(out["endpoint"], "alice") {
				t.Fatalf("fragment identity lost: %#v", out)
			}
		})
	}
}

func TestCompositeStructureRejectsConflictingSecrets(t *testing.T) {
	for _, tc := range []struct{ value, secret string }{
		{"https://private.internal/path", "https"},
		{"https://private.internal:8443/path", "8443"},
		{"https://private.internal/?hint=value", "hint"},
		{"https://private.internal/?%68int=value", "hint"},
		{"https://private.internal/?private+hint=value", "private hint"},
		{"https://private.internal/projects/tenant/serv%69ces/private-service", "services"},
		{"https://private.internal/subscriptions/tenant/providers/Microsoft%2ENetwork/virtualNetworks/private-network", "Microsoft.Network"},
		{"arn:aws:iam::123456789012:user/alice", "user"},
		{"/home/alice/state.tfstate", "tfstate"},
	} {
		t.Run(tc.secret, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"endpoint": tc.value, "token": tc.secret})
			got, err := Scrub(input, nil)
			if err == nil || len(got.Data) != 0 || strings.Contains(err.Error(), tc.secret) {
				t.Fatal("structural secret must fail without output or content")
			}
		})
	}
}

func TestEnclosingSecretSuppressesCompositeConflicts(t *testing.T) {
	for _, tc := range []struct{ value, secret string }{
		{"https://private.internal/projects/tenant/services/private-service", "projects"},
		{"https://private.internal/?%68int=value", "hint"},
	} {
		input, _ := json.Marshal(map[string]string{"endpoint": tc.value, "token": tc.value, "other_token": tc.secret})
		out := scrubObject(t, string(input))
		if out["endpoint"] != out["token"] || strings.Contains(out["endpoint"], ":") || out["other_token"] == tc.secret {
			t.Fatal("enclosing secret did not suppress incidental structure")
		}
	}
}

func TestLiteralPathStructureIsNotURIDecoded(t *testing.T) {
	out := scrubObject(t, `{"path":"/home/alice/state.%68int","token":"hint"}`)
	if !strings.HasSuffix(out["path"], ".%68int") || out["token"] == "hint" {
		t.Fatal("literal path structure gained URI semantics")
	}
}

func TestCloudURLSegmentsUseDecodedIdentity(t *testing.T) {
	for _, key := range []string{"service_name", "token"} {
		t.Run(key, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"endpoint": "https://private.internal/projects/private-project/locations/private-region/services/private%2Dservice", key: "private-service", "plain": "/projects/private-project/locations/private-region/services/private-service"})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(u.Path, "/services/"+out[key]) || !strings.HasSuffix(out["plain"], "/services/"+out[key]) || !strings.Contains(u.Path, "/projects/") || !strings.Contains(u.Path, "/locations/") {
				t.Fatalf("decoded cloud identity lost: %#v", out)
			}
		})
	}
}

func TestCloudURLEquivalenceDoesNotDecodeLocalFilenames(t *testing.T) {
	out := scrubObject(t, `{"endpoint":"https://private.internal/projects/tenant/services/private%2Dservice","service_name":"private-service","filename_name":"private%2Dservice","encoded_path":"/home/private%2Dservice.txt","plain_path":"/home/private-service.txt"}`)
	u, err := url.Parse(out["endpoint"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(u.Path, "/services/"+out["service_name"]) || !strings.HasSuffix(out["encoded_path"], "/"+out["filename_name"]+".txt") || !strings.HasSuffix(out["plain_path"], "/"+out["service_name"]+".txt") || out["encoded_path"] == out["plain_path"] || out["filename_name"] == out["service_name"] {
		t.Fatalf("URL equivalence leaked into literal paths: %#v", out)
	}
}

func TestAbsoluteHostnameSyntaxAndLinkage(t *testing.T) {
	for _, host := range []string{"alice.internal.", "alice."} {
		t.Run(host, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"hostname": host, "endpoint": "https://" + host + ":8443/path"})
			out := scrubObject(t, string(input))
			u, err := url.Parse(out["endpoint"])
			if err != nil {
				t.Fatal(err)
			}
			if u.Hostname() != out["hostname"] || !strings.HasSuffix(out["hostname"], ".example.invalid.") || strings.Contains(out["hostname"], "..") || u.Port() != "8443" {
				t.Fatalf("absolute hostname syntax/linkage: %#v", out)
			}
		})
	}
}

func TestCandidatePrefixContracts(t *testing.T) {
	for _, tc := range []struct {
		input, keep string
		extra       []string
	}{
		{`name=ann name="ann smith" planning ann ann smith`, "planning", nil},
		{`token=ann name="ann smith" ann smith`, " smith", nil},
		{"name=ann élann ann界 planning ann", "élann ann界 planning", nil},
		{"2026-09-08T00:00:00.000Z [TRACE] provider.aws: tf_req_id=ann name=ann\n", "tf_req_id=ann", nil},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := Scrub([]byte(tc.input), tc.extra)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got.Data), tc.keep) || strings.Contains(string(got.Data), "name=ann") {
				t.Fatalf("prefix contract: %s", got.Data)
			}
		})
	}
}

func BenchmarkCandidateMatching(b *testing.B) {
	for _, count := range []int{500, 2000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			var input strings.Builder
			for i := range count {
				fmt.Fprintf(&input, "name=customer_%05d\n", i)
			}
			for input.Len() < 1<<20 {
				input.WriteString("Ordinary diagnostic prose planning a request for customer_00001\n")
			}
			data := []byte(input.String())
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				if _, err := Scrub(data, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
