package logfmt

import "testing"

func TestParseHeaderProviderLine(t *testing.T) {
	line := `2022-12-15T00:16:20.800Z [TRACE] provider.terraform-provider-aws_v4.46.0_x5: Received downstream response: tf_req_id=abc tf_req_duration_ms=5`
	h := ParseHeader(line)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Level != LevelTrace {
		t.Errorf("Level = %v, want TRACE", h.Level)
	}
	if h.Comp != "provider.terraform-provider-aws_v4.46.0_x5" {
		t.Errorf("Comp = %q", h.Comp)
	}
	if h.Msg != "Received downstream response: tf_req_id=abc tf_req_duration_ms=5" {
		t.Errorf("Msg = %q", h.Msg)
	}
	if h.TS.UnixMilli() != 1671063380800 {
		t.Errorf("TS.UnixMilli() = %d, want 1671063380800", h.TS.UnixMilli())
	}
}

func TestParseHeaderNumericOffset(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] terraform.NewContext: complete`)
	if !h.HasTS {
		t.Fatal("HasTS = false, want true")
	}
	if h.Comp != "terraform.NewContext" {
		t.Errorf("Comp = %q, want terraform.NewContext", h.Comp)
	}
	if h.Msg != "complete" {
		t.Errorf("Msg = %q, want complete", h.Msg)
	}
	if _, off := h.TS.Zone(); off != 2*60*60 {
		t.Errorf("zone offset = %d, want 7200", off)
	}
}

func TestParseHeaderPaddedLevelAndNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.123+0200 [INFO]  Terraform version: 1.16.0`)
	if h.Level != LevelInfo {
		t.Errorf("Level = %v, want INFO", h.Level)
	}
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty (\"Terraform version\" contains a space)", h.Comp)
	}
	if h.Msg != "Terraform version: 1.16.0" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderNoColonMeansNoComponent(t *testing.T) {
	h := ParseHeader(`2026-08-29T10:34:43.151+0200 [TRACE] building graph for terraform dependencies`)
	if h.Comp != "" {
		t.Errorf("Comp = %q, want empty", h.Comp)
	}
	if h.Msg != "building graph for terraform dependencies" {
		t.Errorf("Msg = %q", h.Msg)
	}
}

func TestParseHeaderContinuationLine(t *testing.T) {
	if ParseHeader(`on linux_amd64`).HasTS {
		t.Error("HasTS = true, want false for a continuation line")
	}
}

func TestParseHeaderPlanOutputIsNotAHeader(t *testing.T) {
	if ParseHeader(`  + resource "aws_subnet" "example" {`).HasTS {
		t.Error("HasTS = true, want false for plan output")
	}
}

func TestParseHeaderMultilineValueBodyIsNotAHeader(t *testing.T) {
	// The hclog multi-line value shape, verbatim from testdata/multiline-body.log.
	if ParseHeader(`  | {"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}`).HasTS {
		t.Error("HasTS = true, want false for an hclog multi-line value body")
	}
}
