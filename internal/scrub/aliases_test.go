package scrub

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

var validGUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func TestGUIDEquivalenceAndCaseSensitiveResourceKeys(t *testing.T) {
	const lower = "aa000000-0000-4000-8000-000000000000"
	const mixed = "aA000000-0000-4000-8000-000000000000"
	for _, names := range []bool{false, true} {
		t.Run(map[bool]string{false: "ids", true: "resource keys"}[names], func(t *testing.T) {
			in := `{"id":["` + lower + `","` + mixed + `"],"message":"{` + strings.ToUpper(lower) + `}"}`
			if names {
				in += "\n" + `aws_instance.web["` + lower + `"] aws_instance.web["` + mixed + `"]`
			}
			got, err := Scrub([]byte(in), nil)
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				ID      []string `json:"id"`
				Message string   `json:"message"`
			}
			if err := json.Unmarshal([]byte(strings.Split(string(got.Data), "\n")[0]), &out); err != nil {
				t.Fatal(err)
			}
			for _, id := range out.ID {
				if !validGUID.MatchString(id) || strings.EqualFold(id, lower) {
					t.Fatalf("not fresh valid GUID: %s", id)
				}
			}
			if strings.EqualFold(out.ID[0], out.ID[1]) == names {
				t.Fatal("incorrect GUID identity grouping")
			}
			if names {
				for _, id := range out.ID {
					if !strings.Contains(string(got.Data), `["`+id+`"]`) {
						t.Fatal("resource key linkage lost")
					}
				}
			} else if strings.Trim(out.Message, "{}") != strings.ToUpper(out.ID[0]) {
				t.Fatal("GUID wrapper or case changed")
			}
		})
	}
}

func TestAddressLabelsIndicesAndDeclarations(t *testing.T) {
	in := `module.customer_prod["alice"].data.aws_instance.web[0] module.customer_prod["alice"].data.aws_instance.web["0"]` + "\n" + `resource "aws_instance" "web" {` + "\n" + `name=alice` + "\n" + `Earlier customer_prod web alice` + "\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got.Data)
	for _, value := range []string{"customer_prod", "web", "alice", `["0"]`} {
		if strings.Contains(out, value) {
			t.Fatalf("address identity remains: %s", out)
		}
	}
	if !strings.Contains(out, ".data.aws_instance.") || !strings.Contains(out, `resource "aws_instance"`) || !strings.Contains(out, "[0]") || !strings.Contains(out, "_prod") {
		t.Fatalf("address grammar changed: %s", out)
	}
}

func TestLifecycleFieldsAndSecretAddressConflict(t *testing.T) {
	in := `{"@level":"info","@module":"terraform.ui","@timestamp":"2026-09-08T00:00:00Z","type":"apply_start","hook":{"resource":{"addr":"module.customer.aws_instance.web[\"alice\"]","module":"module.customer","resource":"aws_instance.web[\"alice\"]","resource_type":"aws_instance","resource_name":"web","resource_key":"alice","implied_provider":"aws"},"action":"create","id_key":"id","id_value":"deadbeef"},"@message":"deadbeef web alice"}`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(got.Data, &out); err != nil {
		t.Fatal(err)
	}
	hook := out["hook"].(map[string]any)
	resource := hook["resource"].(map[string]any)
	if hook["id_key"] != "id" || hook["id_value"] == "deadbeef" || resource["resource_type"] != "aws_instance" || resource["implied_provider"] != "aws" {
		t.Fatalf("lifecycle metadata: %s", got.Data)
	}
	addr := resource["addr"].(string)
	if addr != resource["module"].(string)+".aws_instance."+resource["resource_name"].(string)+"["+string(mustJSON(t, resource["resource_key"]))+"]" || !strings.HasSuffix(addr, resource["resource"].(string)) {
		t.Fatalf("lifecycle linkage: %s", got.Data)
	}
	conflict := in + "\n" + `token="module.customer.aws_instance.web[\"alice\"]"`
	if got, err := Scrub([]byte(conflict), nil); err == nil || got.Data != nil {
		t.Fatal("accepted secret lifecycle address")
	}
}

func TestAliasAllocationReservesSourceNames(t *testing.T) {
	in := `aws_instance.alice["name_0001"] aws_instance.name_0001["alice"] name=alice name=name_0002`
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "name_0001") || strings.Contains(string(got.Data), "name_0002") || strings.Contains(string(got.Data), "alice") {
		t.Fatalf("alias collided with source: %s", got.Data)
	}
	addresses := strings.Fields(string(got.Data))
	if addresses[0] == addresses[1] {
		t.Fatal("distinct rendered addresses collapsed")
	}
}

func TestHeaderValues(t *testing.T) {
	for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-API-Key", "X-Request-ID", "X-Correlation-ID", "X-Amzn-RequestId", "X-Ms-Request-ID"} {
		t.Run(key, func(t *testing.T) {
			got, err := Scrub([]byte(key+": Bearer opaque private value\nEarlier Bearer opaque private value\n"), nil)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(got.Data), "opaque private value") || strings.Contains(string(got.Data), "Bearer") {
				t.Fatalf("header not fully scrubbed: %s", got.Data)
			}
		})
	}
}

func TestAliasesReserveUndiscoveredSourceTokens(t *testing.T) {
	got, err := Scrub([]byte("name=private\nEarlier name_0001 name_0002\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(got.Data), "\n")
	alias := strings.TrimPrefix(lines[0], "name=")
	if strings.Contains(lines[1], alias) {
		t.Fatal("generated alias collides with pre-existing plain token")
	}
}

func TestGUIDAllocationSkipsCollisionsAndSanitisesFailure(t *testing.T) {
	reserved := map[string]bool{"00000000-0000-4000-8000-000000000000": true}
	input := append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...)
	got, err := freshGUID(bytes.NewReader(input), reserved)
	if err != nil || got != "01010101-0101-4101-8101-010101010101" {
		t.Fatalf("GUID collision allocation: %s %v", got, err)
	}
	if _, err := freshGUID(strings.NewReader(""), nil); err == nil || strings.Contains(err.Error(), "EOF") {
		t.Fatal("randomness failure not sanitised")
	}
}

func TestAddressesUseDeclaredTypesAndUnicodeLabels(t *testing.T) {
	in := "thing.private[\"private-key\"] thing.private[0]\n" + `resource "thing" "private" {` + "\n" + `aws_instance.café["private-key"]` + "\n" + `addr="exotic.équipe[\"other-key\"]" note=ordinary.example` + "\nname=thing\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got.Data)
	for _, value := range []string{"private-key", "private", "café", "équipe", "other-key"} {
		if strings.Contains(out, value) {
			t.Fatalf("address identity remains: %s", out)
		}
	}
	if !strings.Contains(out, `resource "thing"`) || strings.Count(out, "thing.") != 2 || !strings.Contains(out, "[0]") || !strings.Contains(out, "exotic.") || !strings.Contains(out, "note=ordinary.example") {
		t.Fatalf("address types or unrelated hostname changed: %s", out)
	}
}

func TestBracedGUIDsPreserveFormatAndIdentity(t *testing.T) {
	const lower = "aa000000-0000-4000-8000-000000000000"
	const mixed = "aA000000-0000-4000-8000-000000000000"
	for _, named := range []bool{false, true} {
		key := "id"
		if named {
			key = "name"
		}
		in := `{"` + key + `":["{` + lower + `}","{` + mixed + `}"],"echo":["` + lower + `","` + mixed + `"]}`
		got, err := Scrub([]byte(in), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Unsupported != 0 {
			t.Fatal("recognised braced GUID was counted as malformed JSON")
		}
		var out map[string][]string
		if json.Unmarshal(got.Data, &out) != nil {
			t.Fatal("invalid JSON")
		}
		for i, braced := range out[key] {
			if !strings.HasPrefix(braced, "{") || !strings.HasSuffix(braced, "}") || !validGUID.MatchString(strings.Trim(braced, "{}")) || strings.Trim(braced, "{}") != out["echo"][i] {
				t.Fatalf("braced GUID format/linkage: %s", got.Data)
			}
		}
		if strings.EqualFold(out["echo"][0], out["echo"][1]) == named {
			t.Fatal("braced name did not constrain case-sensitive identity")
		}
	}
}

func TestRenderedResourceCollisionsReallocateAliases(t *testing.T) {
	for _, collision := range []string{"addresses", "source address", "keys"} {
		t.Run(collision, func(t *testing.T) {
			s := &session{candidates: make(map[string]*candidate), counts: make(map[string]int)}
			views := s.parseLines(`thing.alice["red"] thing.bob["blue"]`)
			views[0].cuts = []sourceCut{{source: 0}, {source: len(views[0].text)}}
			if err := s.allocate(); err != nil {
				t.Fatal(err)
			}
			switch collision {
			case "addresses":
				s.candidates["alice"].alias = "same_name"
				s.candidates["bob"].alias = "same_name"
				s.candidates["red"].alias = "same_key"
				s.candidates["blue"].alias = "same_key"
			case "source address":
				s.candidates["alice"].alias = "bob"
				s.candidates["red"].alias = "blue"
			case "keys":
				s.candidates["red"].alias = "same_key"
				s.candidates["blue"].alias = "same_key"
			}
			rendered, err := s.ensureDistinctResources(views)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.counts) != 0 {
				t.Fatal("collision validation counted unapplied edits")
			}
			if views[0].cuts[0].output != 0 || views[0].cuts[1].output != len(rendered.text[views[0]]) {
				t.Fatal("validated rendering retained stale cuts")
			}
			out, err := s.render(views[0])
			if err != nil {
				t.Fatal(err)
			}
			if rendered.text[views[0]] != out || rendered.counts["name"] != 4 {
				t.Fatalf("validated rendering retained stale text or counts: %+v", rendered)
			}
			parts := strings.Fields(out)
			if parts[0] == parts[1] || parts[0] == `thing.bob["blue"]` || s.candidates["red"].alias == s.candidates["blue"].alias {
				t.Fatalf("resource collision survived: %s", out)
			}
			if s.candidates["red"].alias == "same_key" || s.candidates["red"].alias == "blue" {
				t.Fatal("offending alias was not reallocated")
			}
		})
	}
}

func TestAddressAndWholeNameUseSameResourceStructure(t *testing.T) {
	got, err := Scrub([]byte("addr=\"aws_instance.web\"\nname=\"aws_instance.web\"\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(got.Data), "\n")
	address := strings.TrimPrefix(lines[0], "addr=")
	if !strings.HasPrefix(address, `"aws_instance.`) || address != strings.TrimPrefix(lines[1], "name=") || strings.Contains(string(got.Data), "web") {
		t.Fatalf("whole name removed address structure: %s", got.Data)
	}
}
