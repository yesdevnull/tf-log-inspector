package scrub

import (
	"strings"
	"testing"
)

func TestShrinkingHeaderRejectsNewSpan(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: password=\"" + strings.Repeat("z", 70000) + "\" tf_req_id=abc tf_req_duration_ms=5\n"
	got, err := Scrub([]byte(in), nil)
	if err == nil || len(got.Data) != 0 {
		t.Fatal("published changed parser metadata")
	}
	if strings.Contains(err.Error(), "zzz") {
		t.Fatal("error disclosed input")
	}
}

func TestExpansionRejectsHiddenField(t *testing.T) {
	prefix := "Received downstream response: name=a padding="
	// The complete request ID is inside the scanner window before a grows.
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: " + prefix + strings.Repeat("x", 65536-len(prefix)-len(" tf_req_id=abc")) + " tf_req_id=abc tf_req_duration_ms=5\n"
	got, err := Scrub([]byte(in), nil)
	if err == nil || got.Data != nil {
		t.Fatal("published hidden request field")
	}
}

func TestLongHeaderPreservesVisibleMetadata(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.aws: Received downstream response: tf_req_id=abc tf_req_duration_ms=5 password=\"" + strings.Repeat("z", 70000) + "\"\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Data), "zzz") {
		t.Fatal("long secret remains")
	}
}

func TestScannerFailureIsContentFree(t *testing.T) {
	in := "2026-01-01T00:00:00.000Z [TRACE] core: begin\n2026-09-08T00:00:00.000Z [TRACE] core: private-content\n"
	got, err := Scrub([]byte(in), nil)
	if err == nil || got.Data != nil {
		t.Fatal("accepted unsupported timestamp span")
	}
	if strings.Contains(err.Error(), "private-content") {
		t.Fatal("error leaked content")
	}
}

func TestProviderIdentityExemptionsArePositional(t *testing.T) {
	in := "2026-09-08T00:00:00.000Z [TRACE] provider.terraform-provider-aws_v5.0.0_x5: Received downstream response: tf_provider_addr=registry.terraform.io/hashicorp/aws tf_req_duration_ms=5\n" +
		"2026-09-08T00:00:00.001Z [TRACE] provider.terraform-provider-custom_v1.2.3_x5: Received downstream response: tf_provider_addr=registry.terraform.io/customer/custom tf_req_duration_ms=5\n" +
		"2026-09-08T00:00:00.002Z [TRACE] provider.terraform-provider-other_v1.2.3_x5: Received downstream response: tf_provider_addr=provider tf_req_duration_ms=5\n" +
		"name=aws name=customer name=custom\n"
	got, err := Scrub([]byte(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(got.Data), "\n")
	if !strings.Contains(lines[0], "provider.terraform-provider-aws_v5.0.0_x5") || !strings.Contains(lines[0], "tf_provider_addr=registry.terraform.io/hashicorp/aws") {
		t.Fatal("public provider changed")
	}
	if strings.Contains(lines[1], "customer") || strings.Contains(lines[1], "custom") || strings.Contains(lines[1], "registry.terraform.io/") || strings.Contains(lines[2], "provider-other") {
		t.Fatalf("private provider remains: %s", got.Data)
	}
	if !strings.Contains(lines[2], "tf_provider_addr=provider") || strings.Contains(lines[3], "name=aws") {
		t.Fatal("provider exemption leaked to names")
	}
}
