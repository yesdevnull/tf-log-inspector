package logfmt

import (
	"strings"
	"testing"
)

func TestParseFieldsUnorderedLookup(t *testing.T) {
	f := ParseFields(`tf_req_id=abc tf_resource_type=aws_subnet tf_rpc=ApplyResourceChange tf_req_duration_ms=5 @module=sdk.proto`, nil)
	want := map[string]string{
		"tf_req_id":          "abc",
		"tf_resource_type":   "aws_subnet",
		"tf_rpc":             "ApplyResourceChange",
		"tf_req_duration_ms": "5",
		"@module":            "sdk.proto",
	}
	for k, v := range want {
		got, ok := f.Get(k)
		if !ok {
			t.Errorf("Get(%q): not found", k)
			continue
		}
		if got != v {
			t.Errorf("Get(%q) = %q, want %q", k, got, v)
		}
	}
}

func TestParseFieldsQuotedValue(t *testing.T) {
	f := ParseFields(`tf_rpc=ReadResource diagnostic_summary="something went wrong" tf_req_duration_ms=7`, nil)
	if got, _ := f.Get("diagnostic_summary"); got != "something went wrong" {
		t.Errorf("quoted value = %q", got)
	}
	if got, _ := f.Get("tf_req_duration_ms"); got != "7" {
		t.Errorf("field after quoted value = %q, want 7", got)
	}
}

// hclog escapes embedded quotes as \". Terminating the value at the first
// quote re-tokenises the remainder and turns its contents into "keys".
func TestParseFieldsEscapedQuoteInsideValue(t *testing.T) {
	f := ParseFields(`diagnostic_summary="bad header \"Authorization=Bearer s3cr3t\" here" tf_rpc=ReadResource`, nil)
	if got, _ := f.Get("diagnostic_summary"); got != `bad header \"Authorization=Bearer s3cr3t\" here` {
		t.Errorf("escaped-quote value = %q", got)
	}
	if _, ok := f.Get("Authorization"); ok {
		t.Error("value contents were re-tokenised into a key")
	}
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("field after escaped-quote value = %q", got)
	}
}

func TestParseFieldsValueContainingEquals(t *testing.T) {
	f := ParseFields(`url=https://example.com/a?b=c tf_rpc=ReadResource`, nil)
	if got, _ := f.Get("url"); got != "https://example.com/a?b=c" {
		t.Errorf("value with = : %q", got)
	}
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("following field = %q", got)
	}
}

// The disclosure guarantee: a token whose key is not an identifier is not a
// field, and must not be recorded at all.
func TestParseFieldsRejectsNonIdentifierKeys(t *testing.T) {
	cases := []string{
		`{"UserData":"IyEvYmluL2Jhc2gK==" tf_rpc=ReadResource`,
		`"-input=false tf_rpc=ReadResource`,
		`[id=673ed14b tf_rpc=ReadResource`,
		`wJalrXUtnFEMI/K7MDENG=secret tf_rpc=ReadResource`,
	}
	for _, in := range cases {
		f := ParseFields(in, nil)
		for _, fl := range f {
			if !ValidKey(fl.Key) {
				t.Errorf("ParseFields(%q) recorded invalid key %q", in, fl.Key)
			}
		}
		if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
			t.Errorf("ParseFields(%q): real field lost, tf_rpc = %q", in, got)
		}
	}
}

func TestParseFieldsNewlineIsADelimiter(t *testing.T) {
	f := ParseFields("tf_req_duration_ms=12\nTerraform used the selected providers", nil)
	if got, _ := f.Get("tf_req_duration_ms"); got != "12" {
		t.Errorf("tf_req_duration_ms = %q, want 12 (newline must delimit)", got)
	}
}

func TestParseFieldsEmptyValue(t *testing.T) {
	f := ParseFields(`diagnostic_detail= tf_rpc=ReadResource`, nil)
	if got, ok := f.Get("diagnostic_detail"); !ok || got != "" {
		t.Errorf("Get(diagnostic_detail) = %q, %v; want \"\", true", got, ok)
	}
}

func TestParseFieldsIgnoresLeadingProse(t *testing.T) {
	f := ParseFields(`Received downstream response: tf_rpc=ReadResource tf_req_duration_ms=3`, nil)
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("tf_rpc = %q", got)
	}
	if _, ok := f.Get("Received"); ok {
		t.Error("prose word parsed as a field key")
	}
}

func TestValidKey(t *testing.T) {
	valid := []string{"tf_rpc", "@module", "aws.operation", "http.response.header.x_amzn_requestid", "a-b"}
	invalid := []string{"", "@", "1abc", `{"UserData"`, `"-input`, "[id", "a/b", "a b", "a=b"}
	for _, s := range valid {
		if !ValidKey(s) {
			t.Errorf("ValidKey(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidKey(s) {
			t.Errorf("ValidKey(%q) = true, want false", s)
		}
	}
}

// The longest key any real log this project was built against produces is
// "http.response.header.x_amzn_requestid" at 37 bytes. It must still be
// accepted after the length cap is added.
func TestValidKeyLongestRealKeyStillAccepted(t *testing.T) {
	key := "http.response.header.x_amzn_requestid"
	if len(key) != 37 {
		t.Fatalf("test fixture key is %d bytes, want 37", len(key))
	}
	if !ValidKey(key) {
		t.Errorf("ValidKey(%q) = false, want true", key)
	}
}

func TestValidKeyLengthBoundary(t *testing.T) {
	at64 := strings.Repeat("a", 64)
	if !ValidKey(at64) {
		t.Errorf("ValidKey(64-byte key) = false, want true")
	}
	at65 := strings.Repeat("a", 65)
	if ValidKey(at65) {
		t.Errorf("ValidKey(65-byte key) = true, want false")
	}
}

// A long base64url-shaped token that happens to satisfy the key charset
// (letters only, here) must still be discarded when it exceeds MaxKeyLen --
// this is asserted against ParseFields, not just ValidKey, because
// ParseFields is what the diagnose report actually consumes.
func TestParseFieldsRejectsOverlongKeyShapedToken(t *testing.T) {
	blob := strings.Repeat("A", 80)
	f := ParseFields(blob+`=c3VwZXJzZWNyZXQ tf_rpc=ReadResource`, nil)
	if _, ok := f.Get(blob); ok {
		t.Errorf("ParseFields recorded an overlong token as a key")
	}
	for _, fl := range f {
		if len(fl.Key) > MaxKeyLen {
			t.Errorf("ParseFields recorded a key longer than MaxKeyLen: %q (%d bytes)", fl.Key, len(fl.Key))
		}
	}
	if got, _ := f.Get("tf_rpc"); got != "ReadResource" {
		t.Errorf("ParseFields(%q): real field lost, tf_rpc = %q", blob, got)
	}
}

func TestParseFieldsReusesBuffer(t *testing.T) {
	buf := ParseFields(`a=1 b=2`, nil)
	buf = ParseFields(`c=3`, buf[:0])
	if len(buf) != 1 {
		t.Fatalf("len = %d, want 1", len(buf))
	}
	if got, _ := buf.Get("c"); got != "3" {
		t.Errorf("Get(c) = %q, want 3", got)
	}
}
