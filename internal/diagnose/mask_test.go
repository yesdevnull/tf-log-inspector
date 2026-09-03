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
