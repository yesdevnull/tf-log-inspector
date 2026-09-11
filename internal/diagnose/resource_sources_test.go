package diagnose

import (
	"os"
	"strings"
	"testing"
)

func TestDiagnoseResourceSourceAdmissionMatchesModel(t *testing.T) {
	for _, tc := range []struct {
		file       string
		count      int
		duration   uint64
		positioned uint64
	}{
		{"resource-refresh.log", 2, 3250, 2},
		{"resource-cli.log", 3, 137000, 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(fixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			r := build(t, string(data))
			if r.UISpanCount != tc.count || r.Quality.UI.DurationMs != tc.duration || r.Quality.UI.Positioned != tc.positioned {
				t.Fatalf("diagnose admission = %+v", r.Quality.UI)
			}
			out := render(t, r)
			if strings.Contains(out, "aws_instance.example") || strings.Contains(out, "aws_instance.other") || strings.Contains(out, "module.example") {
				t.Fatalf("unmasked address in report: %s", out)
			}
		})
	}
}
