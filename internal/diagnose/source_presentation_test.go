package diagnose

import (
	"os"
	"strings"
	"testing"
)

func TestDiagnoseQualifiesRefreshAndCLIDurations(t *testing.T) {
	for _, name := range []string{"resource-refresh.log", "resource-cli.log"} {
		data, err := os.ReadFile(fixture(t, name))
		if err != nil {
			t.Fatal(err)
		}
		out := render(t, build(t, string(data)))
		section := strings.Split(strings.Split(out, "SLOWEST RESOURCES (addresses masked)\n")[1], "BY RESOURCE TYPE")[0]
		if strings.Contains(section, "Terraform reports these") || (!strings.Contains(section, "[refresh_window]") && !strings.Contains(section, "[cli_elapsed]")) {
			t.Errorf("duration sources misrepresented in %s: %s", name, section)
		}
	}
}

func TestDiagnoseRetainsSaturatedResourceLowerBounds(t *testing.T) {
	const capture = "aws_instance.large: Creation complete after 999999999999999h\n" +
		"aws_instance.small: Creation complete after 1s\n" +
		"local_file.small: Creation complete after 2s\n"
	out := render(t, build(t, capture))
	if !strings.Contains(out, "resource slowest span ≥4294967295 ms") {
		t.Errorf("saturated slowest resource summary was displayed as exact:\n%s", out)
	}
	_, resources, ok := strings.Cut(out, "SLOWEST RESOURCES (addresses masked)\n")
	if !ok {
		t.Fatal("resource observations were not rendered")
	}
	resources, types, ok := strings.Cut(resources, "BY RESOURCE TYPE\n")
	if !ok {
		t.Fatal("resource type totals were not rendered")
	}
	types, _, _ = strings.Cut(types, "ADDRESS ATTRIBUTION\n")
	if !strings.Contains(resources, "≥4294967.295s") {
		t.Errorf("saturated observation was displayed as exact:\n%s", resources)
	}
	if !strings.Contains(types, "≥4294968.3s") {
		t.Errorf("saturated type total was displayed as exact:\n%s", types)
	}
	if strings.Count(resources, "≥") != 1 || strings.Count(types, "≥") != 1 {
		t.Errorf("lower-bound markers affected exact durations:\n%s\n%s", resources, types)
	}
}
