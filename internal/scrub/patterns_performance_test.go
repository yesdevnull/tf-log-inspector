package scrub

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// The reference regex defines every match the cheap marker check must retain.
func TestPatternMarkersPreserveMatches(t *testing.T) {
	s := &session{}
	for _, tc := range []struct {
		pattern         *regexp.Regexp
		markers, sample string
	}{
		{emailPattern, "@", "person@example.com"},
		{urlPattern, ":", "https://private.example/path"},
		{localPathPattern, "/\\", `C:\Users\private\file`},
		{cloudPathPattern, "/", "/SUBSCRIPTIONS/private/resourceGroups/group"},
		{azureEndpointPattern, ".", "PRIVATE.VAULT.AZURE.NET"},
		{serviceTokenPattern, "_.", "ghp_private"},
		{serviceTokenPattern, "_.", "abc.atlasv1.private"},
		{guidPattern, "-", "12345678-1234-1234-1234-123456789abc"},
	} {
		for _, value := range []string{"ordinary words", strings.Repeat(" ", 4096), tc.sample, "before " + tc.sample + " after", tc.sample + " " + tc.sample, `"` + tc.sample + `"` + tc.sample + `"`} {
			want := tc.pattern.FindAllStringIndex(value, -1)
			if got := patternMatches(tc.pattern, value, tc.markers); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s lost matches for %q: %v != %v", tc.pattern, value, got, want)
			}
			for range 2 {
				if got := s.cachedPatternMatches(tc.pattern, value, tc.markers); !reflect.DeepEqual(got, want) {
					t.Fatalf("%s cached matches changed for %q: %v != %v", tc.pattern, value, got, want)
				}
			}
		}
	}
}

func TestPatternCacheBoundedWithoutLosingMatches(t *testing.T) {
	s := &session{}
	for i := range 10000 {
		value := "https://example.invalid/" + strconv.Itoa(i)
		got := s.cachedPatternMatches(urlPattern, value, ":")
		if len(got) != 1 || got[0][0] != 0 || got[0][1] != len(value) {
			t.Fatal("cache saturation lost URL matches")
		}
	}
	if len(s.patterns) == 0 || len(s.patterns) >= 10000 {
		t.Fatal("pattern cache grew with every distinct value")
	}
	s = &session{}
	value := "https://example.invalid/" + strings.Repeat("path", 1000)
	got := s.cachedPatternMatches(urlPattern, value, ":")
	if len(got) != 1 || got[0][1] != len(value) || len(s.patterns) != 0 {
		t.Fatal("large URL was lost or retained in the short-value cache")
	}
}

func TestPatternCacheDoesNotRetainSourceBacking(t *testing.T) {
	source := strings.Repeat("x", 64*1024) + "a-b"
	value := source[len(source)-3:]
	s := &session{}
	s.cachedPatternMatches(guidPattern, value, "-")
	if len(s.patterns) != 1 {
		t.Fatal("short negative match was not cached")
	}
	for key := range s.patterns {
		if unsafe.StringData(key.text) == unsafe.StringData(value) {
			t.Fatal("short cache key retains its larger source allocation")
		}
	}
}

func TestReserveSourceTokens(t *testing.T) {
	s := &session{sources: make(map[string]bool)}
	s.reserveTokens("name0001 plain-word under_score café cafe\u0301 日本語 Ⅷ ½ 𐐀 \xfftail name0001")
	want := map[string]bool{"name0001": true, "plain-word": true, "under_score": true, "café": true, "cafe": true, "日本語": true, "Ⅷ": true, "½": true, "𐐀": true, "tail": true}
	if !reflect.DeepEqual(s.sources, want) {
		t.Fatalf("reserved tokens=%v; want%v", s.sources, want)
	}
}

func FuzzAddressMatch(f *testing.F) {
	for _, text := range []string{
		`data.aws_instance.web["ali\u0063e"].module.child`,
		`123-aws_instance.web[12] ordinary.example.com`,
		"a\u0301.Ⅷ½-𐐀[\"key.with.dots\"]",
		"\u0301_私.名\xff resource.thing[\"bad\\\nkey\"]",
		`{"displayName":"person","homepage":"https://private.example/path"}`,
		`a.b["unfinished a.c`,
	} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		want := addressPart.FindStringSubmatchIndex(text)
		if got := addressMatch(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("address matches for %q: %v; want %v", text, got, want)
		}
	})
}

func BenchmarkAddressDiscovery(b *testing.B) {
	text := strings.Repeat(`{"displayName":"ordinary-person","enabled":true,"count":12345},`, 1000) + `aws_instance.web["private-key"]`
	for b.Loop() {
		if addressMatch(text) == nil {
			b.Fatal("missing address")
		}
	}
}

func FuzzIPAddressCandidates(f *testing.F) {
	for _, text := range []string{"123 abc false", "192.0.2.1:443 deadbeef ::1 fe80::1%eth0", "abc%face.1 ff%0:1", "invalid 1.2.3.4. trailing"} {
		f.Add(text)
	}
	original := regexp.MustCompile(`[0-9A-Fa-f:.]+(?:%[a-zA-Z0-9_-]+)?`)
	f.Fuzz(func(t *testing.T, text string) {
		var want []region
		for _, match := range original.FindAllStringIndex(text, -1) {
			if strings.ContainsAny(text[match[0]:match[1]], ".:") {
				want = append(want, region{match[0], match[1]})
			}
		}
		if got := ipMatches(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("IP candidates for %q: %v; want %v", text, got, want)
		}
	})
}
