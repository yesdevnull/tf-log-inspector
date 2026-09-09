package scrub

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestScrubLongWhitespace(t *testing.T) {
	input := []byte("prefix " + strings.Repeat(" ", 200000) + `name="private-person"`)
	start := time.Now()
	result, err := Scrub(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Data), "private-person") || result.Replacements["name"] != 1 {
		t.Fatal("whitespace hid a field")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("scrubbing 200 KB of whitespace took %s; expected under 2 seconds", elapsed)
	}
}

func TestDiscoverySkipsProviderMaskPadding(t *testing.T) {
	v := &view{text: "name=plain " + strings.Repeat(" ", 30*1024*1024)}
	s := &session{candidates: make(map[string]*candidate)}
	start := time.Now()
	s.discoverPatterns(v)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("pattern discovery over provider padding took %s; expected under 2 seconds", elapsed)
	}
}

func TestPlainQuotedValuesNeedNoDecodingAllocation(t *testing.T) {
	const input = `"a plain UTF-8 value: café"`
	allocations := testing.AllocsPerRun(100, func() {
		end, value, ok := readString(input, 0)
		if !ok || end != len(input) || value != "a plain UTF-8 value: café" {
			t.Fatal("plain quoted value changed")
		}
	})
	if allocations != 0 {
		t.Fatalf("plain quoted value allocated %.0f times; want no decoding allocation", allocations)
	}
}

func TestNonNumericValuesNeedNoParsingAllocation(t *testing.T) {
	for _, value := range []string{"private-person", "https://private.example/path", "", " 12", "null", "true"} {
		allocations := testing.AllocsPerRun(100, func() {
			if isNumber(value) {
				t.Fatalf("non-numeric value accepted: %q", value)
			}
		})
		if allocations != 0 {
			t.Errorf("numeric check for %q allocated %.0f times", value, allocations)
		}
	}
}

func TestJSONSyntaxProtectionAllocation(t *testing.T) {
	text := "[" + strings.TrimSuffix(strings.Repeat(`"plain",`, 2000), ",") + "]"
	allocations := testing.AllocsPerRun(3, func() {
		v := &view{text: text}
		v.protectJSONSyntax()
		if len(v.protected) != 6001 || v.protected[0] != (region{0, 1}) || v.protected[1] != (region{1, 2}) || v.protected[2] != (region{7, 8}) || v.protected[3] != (region{8, 9}) || v.protected[6000] != (region{len(text) - 1, len(text)}) {
			t.Fatal("JSON delimiter protection changed")
		}
	})
	if allocations > 2 {
		t.Fatalf("JSON delimiter protection allocated %.0f times; expected at most 2", allocations)
	}
}

func TestJSONSyntaxProtectionDoesNotDecodeValues(t *testing.T) {
	text := `{"value":"` + strings.Repeat(`\n\u0041\"`, 1000) + `"}`
	allocations := testing.AllocsPerRun(3, func() {
		v := &view{text: text}
		v.protectJSONSyntax()
		if len(v.protected) != 7 || v.protected[5] != (region{len(text) - 2, len(text) - 1}) || v.protected[6] != (region{len(text) - 1, len(text)}) {
			t.Fatal("escaped string delimiter protection changed")
		}
	})
	if allocations > 2 {
		t.Fatalf("delimiter protection decoded values: %.0f allocations", allocations)
	}
}

func TestRepeatedGUIDDiscoveryAllocation(t *testing.T) {
	const value = "12345678-1234-1234-1234-123456789abc"
	s := &session{candidates: make(map[string]*candidate)}
	s.discoverPatterns(&view{text: value})
	cold := testing.AllocsPerRun(100, func() {
		s.patterns = nil
		s.discoverPatterns(&view{text: value})
	})
	allocations := testing.AllocsPerRun(100, func() {
		s.discoverPatterns(&view{text: value})
	})
	if len(s.ordered) != 1 || s.candidates[value] == nil || s.candidates[value].category != "guid" {
		t.Fatal("repeated GUID discovery changed identity")
	}
	if allocations*2 >= cold {
		t.Fatalf("repeated GUID discovery allocated %.0f times versus %.0f cold; expected less than half", allocations, cold)
	}
}

func TestResponseBodySearchSkipsUnrelatedFields(t *testing.T) {
	text := strings.Repeat(`tf_rpc=ReadResource tf_resource_type=example_instance `, 10)
	allocations := testing.AllocsPerRun(100, func() {
		if start, _ := httpBodyStart(text); start != -1 {
			t.Fatal("unrelated metadata was treated as a response body")
		}
	})
	if allocations != 0 {
		t.Fatalf("searching unrelated metadata allocated %.0f times", allocations)
	}
}

func TestRenderLargeLiteralAllocation(t *testing.T) {
	v := &view{text: strings.Repeat(" ", 64*1024)}
	s := &session{}
	allocations := testing.AllocsPerRun(3, func() {
		output, err := s.render(v)
		if err != nil || output != v.text {
			t.Fatalf("literal rendering changed: %v", err)
		}
	})
	if allocations > 5 {
		t.Fatalf("rendering a 64 KiB literal allocated %.0f times; expected at most 5", allocations)
	}
}

func TestResourceTrackingAllocationWithoutAddresses(t *testing.T) {
	s := &session{candidates: make(map[string]*candidate), counts: make(map[string]int), used: make(map[*candidate]bool)}
	v := s.parseLines("[" + strings.TrimSuffix(strings.Repeat(`"ordinary",`, 100), ",") + "]")[0]
	render := func() {
		output, err := s.render(v)
		if err != nil || output != v.text {
			t.Fatalf("non-resource rendering changed: %v", err)
		}
	}
	plain := testing.AllocsPerRun(3, render)
	s.collectResources = true
	tracking := testing.AllocsPerRun(3, render)
	if tracking > plain+5 {
		t.Fatalf("resource tracking added %.0f allocations for fields without addresses", tracking-plain)
	}
}

func TestProtectedRegionLookup(t *testing.T) {
	index := indexRegions([]region{{8, 12}, {2, 10}, {4, 5}, {2, 3}})
	for _, tc := range []struct {
		start, end         int
		contains, overlaps bool
	}{
		{0, 2, false, false},
		{2, 10, true, true},
		{3, 9, true, true},
		{9, 11, true, true},
		{3, 11, false, true},
		{12, 13, false, false},
	} {
		if index.contains(tc.start, tc.end) != tc.contains || index.overlaps(tc.start, tc.end) != tc.overlaps {
			t.Errorf("incorrect protection for [%d,%d)", tc.start, tc.end)
		}
	}
	if indexRegions(nil).contains(0, 1) || indexRegions(nil).overlaps(0, 1) {
		t.Fatal("empty regions protect input")
	}
}

// BenchmarkProviderResponse measures large reconstructed collections without
// using private captures. Every object contains a name and a credential.
func BenchmarkProviderResponse(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			body := `{"value":[` + strings.TrimSuffix(strings.Repeat(`{"displayName":"example-person","token":"example-credential","id":"12345678-1234-1234-1234-123456789abc","homepage":"https://example-person.example.com","enabled":true,"optional":null,"count":12345},`, count), ",") + `]}`
			var input strings.Builder
			for len(body) > 0 {
				n := min(len(body), 65536)
				input.WriteString("2026-09-04T12:56:10.514+1000 [DEBUG] provider.terraform-provider-azurerm_v4.81.0_x5: ")
				input.WriteString(body[:n])
				input.WriteByte('\n')
				body = body[n:]
			}
			data := []byte(input.String())
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				result, err := Scrub(data, nil)
				if err != nil {
					b.Fatal(err)
				}
				if strings.Contains(string(result.Data), "example-credential") || result.Replacements["secret"] != count {
					b.Fatal("credential scrubbing failed")
				}
			}
		})
	}
}
