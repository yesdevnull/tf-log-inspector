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
