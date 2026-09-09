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
