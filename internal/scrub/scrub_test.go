package scrub

import (
	"strings"
	"testing"
)

func TestRepeatedNameAndRequestID(t *testing.T) {
	in := "Earlier customer_prod\n2026-09-08T00:00:00.000Z [TRACE] provider.aws: Sending request downstream: name=customer_prod tf_req_id=12345678-1234-4234-8234-123456789abc\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got.Data)
	if strings.Contains(out, "customer_prod") {
		t.Fatal("name remains")
	}
	fields := strings.Fields(out)
	alias := fields[1]
	if !strings.Contains(out, "name="+alias) {
		t.Fatal("repeated value lost linkage")
	}
	if !strings.Contains(out, "tf_req_id=12345678-1234-4234-8234-123456789abc") {
		t.Fatal("request ID changed")
	}
}
