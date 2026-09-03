package diagnose

import (
	"strings"
	"testing"
)

func TestMaskProseHidesAddressesAndPaths(t *testing.T) {
	cases := []struct{ in, mustNotContain string }{
		{`module.vpc["datacenter1"].aws_internet_gateway.this[0] encountered an error`, "datacenter1"},
		{`writeResourceInstanceState to workingState for aws_codebuild_project.codebuild_name`, "codebuild_name"},
		{`Attempting to open CLI config file: /home/lucas/.terraformrc`, "lucas"},
		{`applying the planned Create change for terraform_data.r1`, "terraform_data.r1"},
	}
	for _, c := range cases {
		got := MaskProse(c.in)
		if strings.Contains(got, c.mustNotContain) {
			t.Errorf("MaskProse(%q) = %q, still contains %q", c.in, got, c.mustNotContain)
		}
	}
}

func TestMaskProseKeepsCoreIdentifiers(t *testing.T) {
	// Go identifiers in core messages are safe and are what makes a template
	// legible. CamelCase after the dot distinguishes them from addresses.
	for _, in := range []string{"terraform.NewContext complete", "statemgr.Filesystem unlocking"} {
		got := MaskProse(in)
		if strings.Contains(got, "<addr>") {
			t.Errorf("MaskProse(%q) = %q, masked a core identifier", in, got)
		}
	}
}

func TestMaskProseHidesQuotedStrings(t *testing.T) {
	got := MaskProse(`vertex "aws_codebuild_project.codebuild_name": visit complete`)
	if strings.Contains(got, "codebuild_name") {
		t.Errorf("MaskProse = %q, leaked a quoted address", got)
	}
	if !strings.Contains(got, "vertex") {
		t.Errorf("MaskProse = %q, lost the message shape", got)
	}
}

func TestMaskProseHidesLongIdentifiers(t *testing.T) {
	got := MaskProse("request 2634bc46bb663d22528dd2eaf8165f52 complete")
	if strings.Contains(got, "2634bc46") {
		t.Errorf("MaskProse = %q, leaked a long identifier", got)
	}
}

func TestMaskProseHidesEntireQuotedSpan(t *testing.T) {
	// A per-token quote check alone only replaces the words touching the
	// quote marks; a multi-word quoted phrase leaves everything between them
	// -- the actual content -- untouched. The whole span must go, not just
	// its boundary words.
	cases := []struct{ in, mustNotContain string }{
		// Real ERROR line from the AWS capture: a single-word quoted address.
		{`vertex "aws_codebuild_project.codebuild_name" error: updating CodeBuild project (codebuild-name): InvalidInputException: Caller is an end user and not allowed to mutate system tags.`, "aws_codebuild_project.codebuild_name"},
		// Real JSON error body from testdata/multiline-body.log: a
		// multi-word quoted message. This is the interior-leak case.
		{`{"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}`, "is an end user"},
		{`{"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}`, "not allowed"},
		{`{"__type":"Inva*************tion","message":"Caller is an end user and not allowed to mutate system tags."}`, "mutate system"},
	}
	for _, c := range cases {
		got := MaskProse(c.in)
		if strings.Contains(got, c.mustNotContain) {
			t.Errorf("MaskProse(%q) = %q, still contains %q", c.in, got, c.mustNotContain)
		}
	}
}

func TestMaskProseTwoQuotedSpansKeepInterveningTextIntact(t *testing.T) {
	// Each quoted phrase must become its own placeholder, not one placeholder
	// spanning from the first opening quote to the last closing quote in the
	// message -- that would also swallow "and subtitle" below.
	got := MaskProse(`set title to "hello world" and subtitle "goodbye now" today`)
	want := `set title to <q> and subtitle <q> today`
	if got != want {
		t.Errorf("MaskProse = %q, want %q", got, want)
	}
}

func TestMaskProseUnterminatedQuoteMasksToEndOfString(t *testing.T) {
	// The conservative direction, matching readQuoted in
	// internal/logfmt/fields.go, which already consumes the remainder on an
	// unterminated quote.
	got := MaskProse(`error: opening file "config.tf: no such file`)
	want := `error: opening file <q>`
	if got != want {
		t.Errorf("MaskProse = %q, want %q", got, want)
	}
}

func TestMaskProseContractionsDoNotTriggerSpanMasking(t *testing.T) {
	// The span rule is scoped to double quotes only. A single quote stays a
	// token-level trigger, so a contraction masks just that one word rather
	// than swallowing the rest of the line the way a span rule would.
	got := MaskProse(`File doesn't exist, but doesn't need to. Ignoring.`)
	want := `File <q> exist, but <q> need to. Ignoring.`
	if got != want {
		t.Errorf("MaskProse = %q, want %q", got, want)
	}
}

func TestMaskComponent(t *testing.T) {
	if got := MaskComponent("terraform_data.r1"); got != "<addr>" {
		t.Errorf("MaskComponent(terraform_data.r1) = %q, want <addr>", got)
	}
	if got := MaskComponent("statemgr.Filesystem"); got != "statemgr.Filesystem" {
		t.Errorf("MaskComponent(statemgr.Filesystem) = %q, want it kept", got)
	}
	if got := MaskComponent("provider.terraform-provider-aws_v4.46.0_x5"); got != "provider.terraform-provider-aws_v4.46.0_x5" {
		t.Errorf("MaskComponent kept-case failed: %q", got)
	}
}
