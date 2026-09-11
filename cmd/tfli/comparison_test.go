package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCompareRequiresExactlyTwoInputs(t *testing.T) {
	for _, paths := range [][]string{nil, {"before.log"}, {"a.log", "b.log", "c.log"}} {
		args := append([]string{"--compare"}, paths...)
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr)
		if err == nil || err.Error() != "expected exactly two log file arguments for --compare" {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		if stdout.Len() != 0 || strings.Count(stderr.String(), "Usage: tfli") != 1 {
			t.Fatalf("run(%v): stdout=%q stderr=%q", args, stdout.String(), stderr.String())
		}
	}
}

func TestCompareJSONRejectsExplicitLimitBeforeTouchingOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"--compare", "--format=json", "--limit=0", "-o", output, "missing-before.log", "missing-after.log"}, &stdout, &stderr)
	if err == nil || err.Error() != "--limit is not supported with --format json" {
		t.Fatalf("error: %v", err)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil || string(data) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("validation touched output: data=%q readErr=%v stdout=%q stderr=%q", data, readErr, stdout.String(), stderr.String())
	}
}

func TestComparisonDefaultTextEqualsExplicitText(t *testing.T) {
	before := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	var implicit, explicit, repeated bytes.Buffer
	if err := run([]string{"--compare", before, before}, &implicit, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--compare", "--format=text", before, before}, &explicit, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--compare", "--format=json", "--format=text", "--limit=1", "--limit=20", before, before}, &repeated, io.Discard); err != nil {
		t.Fatal(err)
	}
	if implicit.String() != explicit.String() || implicit.String() != repeated.String() || !strings.Contains(implicit.String(), "tfli comparison report") {
		t.Fatalf("default, explicit and repeated text differ\ndefault:\n%s\nexplicit:\n%s\nrepeated:\n%s", implicit.String(), explicit.String(), repeated.String())
	}
}

func TestCompareValidationPrecedesInputAndOutputAccess(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"diagnose conflict", []string{"--compare", "--diagnose"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"profile conflict", []string{"--compare", "--profile"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"scrub conflict", []string{"--compare", "--scrub"}, "pass only one of --diagnose, --profile, --scrub, or --compare"},
		{"scrub values", []string{"--compare", "--scrub-values=values.txt"}, "--scrub-values applies only to --scrub"},
		{"negative limit", []string{"--compare", "--limit=-1"}, "--limit must be non-negative"},
		{"empty format", []string{"--compare", "--format="}, "--format must be text or json"},
		{"uppercase format", []string{"--compare", "--format=JSON"}, "--format must be text or json"},
		{"control format", []string{"--compare", "--format=json\x1b[31m"}, "--format must be text or json"},
		{"explicit default JSON limit", []string{"--compare", "--format=json", "--limit=20"}, "--limit is not supported with --format json"},
		{"repeated limit ending default", []string{"--compare", "--format=json", "--limit=1", "--limit=20"}, "--limit is not supported with --format json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report")
			if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := append(append([]string{}, tc.args...), "-o", output, "missing-before.log", "missing-after.log")
			var stdout, stderr bytes.Buffer
			err := run(args, &stdout, &stderr)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			got, readErr := os.ReadFile(output)
			if readErr != nil || string(got) != "keep\n" || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("validation touched output: %q, %v, %q, %q", got, readErr, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCompareFalseRetainsOneInputArity(t *testing.T) {
	var stderr bytes.Buffer
	err := run([]string{"--compare=false", "a.log", "b.log"}, io.Discard, &stderr)
	if err == nil || err.Error() != "expected exactly one log file argument" || strings.Count(stderr.String(), "Usage: tfli") != 1 {
		t.Fatalf("error=%v stderr=%q", err, stderr.String())
	}
}

func TestComparisonProtectsBothInputsFromOutputAliases(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, role := range []int{0, 1} {
			for _, alias := range []string{"same path", "hard link", "symbolic link"} {
				t.Run(format+"/role"+string(rune('0'+role))+"/"+alias, func(t *testing.T) {
					dir := t.TempDir()
					inputs := []string{filepath.Join(dir, "before.log"), filepath.Join(dir, "after.log")}
					original := [][]byte{[]byte("2026-09-08T00:00:00.000Z [INFO] before\n"), []byte("2026-09-08T00:00:00.000Z [INFO] after\n")}
					for i := range inputs {
						if err := os.WriteFile(inputs[i], original[i], 0o600); err != nil {
							t.Fatal(err)
						}
					}
					output := inputs[role]
					if alias != "same path" {
						output = filepath.Join(dir, "report")
						link := os.Link
						if alias == "symbolic link" {
							link = os.Symlink
						}
						if err := link(inputs[role], output); err != nil {
							t.Skipf("creating %s: %v", alias, err)
						}
					}
					beforeBytes := make([][]byte, 2)
					for i := range inputs {
						beforeBytes[i], _ = os.ReadFile(inputs[i])
					}
					err := run([]string{"--compare", "--format=" + format, "-o", output, inputs[0], inputs[1]}, io.Discard, io.Discard)
					if err == nil || !strings.Contains(err.Error(), "same file") {
						t.Fatalf("error = %v", err)
					}
					for i := range inputs {
						got, readErr := os.ReadFile(inputs[i])
						if readErr != nil || !bytes.Equal(got, beforeBytes[i]) {
							t.Fatalf("input %d changed: %q, %v", i, got, readErr)
						}
					}
				})
			}
		}
	}
}

func TestComparisonJSONIsOneDocumentAndOutputOnly(t *testing.T) {
	input := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	output := filepath.Join(t.TempDir(), "comparison.json")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--compare", "--format=json", "-o", output, input, input}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("decode after document = %v", err)
	}
	root := document.(map[string]any)
	for _, sectionValue := range root["sections"].([]any) {
		for _, rowValue := range sectionValue.(map[string]any)["rows"].([]any) {
			for metric, value := range rowValue.(map[string]any)["changes"].(map[string]any) {
				if value != nil && value.(float64) != 0 {
					t.Errorf("defined %s change = %v, want zero", metric, value)
				}
			}
		}
	}
	if bytes.Count(data, []byte("\"schema_version\"")) != 1 || !bytes.HasSuffix(data, []byte("\n")) || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("data=%q stdout=%q stderr=%q", data, stdout.String(), stderr.String())
	}
}

func TestComparisonOutputFailuresAreReturned(t *testing.T) {
	input := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	writeFailure := errors.New("comparison output failed")
	for _, tc := range []struct {
		name   string
		args   []string
		stdout io.Writer
		want   error
	}{
		{"text directory", []string{"--compare", "-o", t.TempDir(), input, input}, io.Discard, nil},
		{"JSON directory", []string{"--compare", "--format=json", "-o", t.TempDir(), input, input}, io.Discard, nil},
		{"text short stdout", []string{"--compare", input, input}, profileJSONShortWriter{}, io.ErrShortWrite},
		{"JSON short stdout", []string{"--compare", "--format=json", input, input}, profileJSONShortWriter{}, io.ErrShortWrite},
		{"text stdout error", []string{"--compare", input, input}, comparisonErrorWriter{err: writeFailure}, writeFailure},
		{"JSON stdout error", []string{"--compare", "--format=json", input, input}, comparisonErrorWriter{err: writeFailure}, writeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := run(tc.args, tc.stdout, &stderr)
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if tc.want == nil && (err == nil || !strings.Contains(err.Error(), "creating ")) {
				t.Fatalf("directory error=%v", err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr=%q, want empty", stderr.String())
			}
		})
	}
}

type comparisonErrorWriter struct{ err error }

func (w comparisonErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestCompareLimitParseDiagnosticsAndShortCircuits(t *testing.T) {
	for _, value := range []string{"many", "999999999999999999999999999999999999999999999999999999"} {
		var stderr bytes.Buffer
		err := run([]string{"--compare", "--limit=" + value, "before.log", "after.log"}, io.Discard, &stderr)
		if err == nil || !strings.Contains(err.Error(), "invalid value") || strings.Count(stderr.String(), "invalid value") != 1 {
			t.Fatalf("value=%q error=%v stderr=%q", value, err, stderr.String())
		}
	}
	var help bytes.Buffer
	if err := run([]string{"--help", "--compare"}, io.Discard, &help); err != nil || !strings.Contains(help.String(), "tfli --compare") {
		t.Fatalf("help error=%v output=%q", err, help.String())
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version", "--compare"}, &stdout, &stderr); err != nil || stdout.String() != "tfli "+version+"\n" || stderr.Len() != 0 {
		t.Fatalf("version error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestComparisonMissingInputNamesRoleAndPreservesOutput(t *testing.T) {
	existing := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	for _, tc := range []struct{ name, before, after, want string }{
		{"before", "missing-before.log", existing, "loading before:"},
		{"after", existing, "missing-after.log", "loading after:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "report")
			if err := os.WriteFile(output, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := run([]string{"--compare", "-o", output, tc.before, tc.after}, io.Discard, io.Discard)
			got, readErr := os.ReadFile(output)
			if err == nil || !strings.Contains(err.Error(), tc.want) || readErr != nil || string(got) != "keep\n" {
				t.Fatalf("error=%v output=%q readErr=%v", err, got, readErr)
			}
		})
	}
}

func TestComparisonWorkflowsUseCompleteParsedEvidence(t *testing.T) {
	tests := []struct {
		name        string
		before      string
		after       string
		section     string
		keyField    string
		keyValue    string
		state       string
		beforeCount string
		beforeTotal string
		afterCount  string
		afterTotal  string
		totalChange any
		countChange any
	}{
		{"identical RPC", "provider-rpc.log", "provider-rpc.log", "rpc_providers", "provider", "registry.terraform.io/hashicorp/aws", "matched", "2", "6", "2", "6", "0", "0"},
		{"added RPC group", "provider-rpc.log", "two-tier.log", "rpc_resource_types", "resource_type", "aws_instance", "added", "0", "0", "2", "370", "370", "2"},
		{"changed UI group", "structured-ui.log", "two-tier.log", "ui_resource_types", "resource_type", "aws_instance", "matched", "1", "2500", "2", "5000", "2500", "1"},
		{"disjoint RPC tier", "structured-ui.log", "provider-rpc.log", "rpc_providers", "provider", "registry.terraform.io/hashicorp/aws", "unavailable", "", "", "2", "6", nil, nil},
		{"unavailable RPC tier", "structured-ui.log", "core-only.log", "rpc_resource_types", "resource_type", "", "", "", "", "", "", nil, nil},
		{"lower-bound UI", "resources-long-lower-bound.log", "resources-long-lower-bound.log", "ui_operations", "address", "module.with_a_very_long_instance_key[\"display-safe\"].aws_instance.resource_with_a_long_name", "matched", "1", "4294967295", "1", "4294967295", nil, "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := filepath.Join("..", "..", "testdata", tc.before)
			after := filepath.Join("..", "..", "testdata", tc.after)
			root, raw := runComparisonJSONDocument(t, before, after)
			section := comparisonCLISection(t, root, tc.section)
			if tc.state == "" {
				if section["before_available"] != false || section["after_available"] != false || len(section["rows"].([]any)) != 0 {
					t.Fatalf("unexpected unavailable section: %#v", section)
				}
				return
			}
			row := comparisonCLIRow(t, section, tc.keyField, tc.keyValue)
			if row["state"] != tc.state {
				t.Fatalf("state=%v, want %q", row["state"], tc.state)
			}
			assertCLIComparisonSummary(t, row["before"], tc.beforeCount, tc.beforeTotal)
			assertCLIComparisonSummary(t, row["after"], tc.afterCount, tc.afterTotal)
			changes := row["changes"].(map[string]any)
			if changes["total_ms"] != tc.totalChange || changes["count"] != tc.countChange {
				t.Fatalf("changes=%#v, want total=%v count=%v", changes, tc.totalChange, tc.countChange)
			}
			var text bytes.Buffer
			if err := run([]string{"--compare", "--limit=0", before, after}, &text, io.Discard); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text.String(), tc.keyValue) || len(raw) == 0 {
				t.Fatalf("text omitted parsed key %q", tc.keyValue)
			}
			if tc.name == "lower-bound UI" && !strings.Contains(text.String(), "timing deltas unavailable: lower bound") {
				t.Fatal("text omitted lower-bound delta qualification")
			}
		})
	}
}

func TestComparisonSyntheticParsingCoversCountVolumeAndIdentifiers(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.log")
	after := filepath.Join(dir, "after.log")
	beforeLines := strings.Join([]string{
		rpcCompletion("p", 10),
		rpcCompletion("p", 30),
		rpcCompletion("removed", 5),
		rpcCompletion("zero", 0),
		uiCompletion("old.address", "create", 1),
		uiCompletion("old.address", "create", 2),
		uiCompletion("module.東京[\\u001b]", "créate\\n", 1),
	}, "\n") + "\n"
	afterLines := strings.Join([]string{
		rpcCompletion("p", 40),
		rpcCompletion("added", 7),
		rpcCompletion("zero", 5),
		uiCompletion("new.address", "create", 3),
		uiCompletion("module.東京[\\u001b]", "créate\\n", 2),
	}, "\n") + "\n"
	if err := os.WriteFile(before, []byte(beforeLines), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(after, []byte(afterLines), 0o600); err != nil {
		t.Fatal(err)
	}
	root, _ := runComparisonJSONDocument(t, before, after)
	rpc := comparisonCLISection(t, root, "rpc_providers")
	matched := comparisonCLIRow(t, rpc, "provider", "p")
	assertCLIComparisonSummary(t, matched["before"], "2", "40")
	assertCLIComparisonSummary(t, matched["after"], "1", "40")
	changes := matched["changes"].(map[string]any)
	if changes["count"] != "-1" || changes["total_ms"] != "0" || changes["mean_ms"] != "20" {
		t.Fatalf("equal-volume count change=%#v", changes)
	}
	zero := comparisonCLIRow(t, rpc, "provider", "zero")["changes"].(map[string]any)
	if zero["total_ms"] != "5" || zero["total_percent"] != nil {
		t.Fatalf("zero baseline changes=%#v", zero)
	}
	if comparisonCLIRow(t, rpc, "provider", "added")["state"] != "added" || comparisonCLIRow(t, rpc, "provider", "removed")["state"] != "removed" {
		t.Fatal("added or removed RPC evidence lost")
	}
	ui := comparisonCLISection(t, root, "ui_operations")
	old := comparisonCLIRow(t, ui, "address", "old.address")
	assertCLIComparisonSummary(t, old["before"], "2", "3000")
	if old["state"] != "removed" || comparisonCLIRow(t, ui, "address", "new.address")["state"] != "added" {
		t.Fatal("renamed UI addresses were matched")
	}
	controlled := comparisonCLIRow(t, ui, "address", "module.東京[\x1b]")
	if controlled["key"].(map[string]any)["action"] != "créate\n" {
		t.Fatalf("control/Unicode key changed: %#v", controlled["key"])
	}
	qualifications := root["qualifications"].([]any)
	if !containsCLIString(qualifications, "independent_scrub_aliases_may_differ") {
		t.Fatalf("scrub qualification missing: %#v", qualifications)
	}
	var text bytes.Buffer
	if err := run([]string{"--compare", "--limit=0", before, after}, &text, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), `module.東京[\x1b]`) || !strings.Contains(text.String(), `créate\n`) {
		t.Fatalf("text did not visibly escape identifiers:\n%s", text.String())
	}
}

func TestComparisonJSONDependsOnlyOnBytesAndBasenames(t *testing.T) {
	source := filepath.Join("..", "..", "testdata", "provider-rpc.log")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	leftDir, rightDir := filepath.Join(dir, "left"), filepath.Join(dir, "right")
	if err := os.Mkdir(leftDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rightDir, 0o700); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(leftDir, "capture.log")
	right := filepath.Join(rightDir, "capture.log")
	if err := os.WriteFile(left, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(right, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, first := runComparisonJSONDocument(t, left, right)
	_, second := runComparisonJSONDocument(t, right, left)
	if !bytes.Equal(first, second) || bytes.Contains(first, []byte(dir)) {
		t.Fatalf("directory affected output or leaked: equal=%v", bytes.Equal(first, second))
	}
	renamedBefore := filepath.Join(leftDir, "renamed-before.log")
	renamedAfter := filepath.Join(rightDir, "renamed-after.log")
	if err := os.WriteFile(renamedBefore, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(renamedAfter, data, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRoot, _ := runComparisonJSONDocument(t, renamedBefore, right)
	afterRoot, _ := runComparisonJSONDocument(t, left, renamedAfter)
	if comparisonCLIInput(beforeRoot, "before") != "renamed-before.log" || comparisonCLIInput(beforeRoot, "after") != "capture.log" {
		t.Fatalf("before rename metadata=%#v", beforeRoot)
	}
	if comparisonCLIInput(afterRoot, "before") != "capture.log" || comparisonCLIInput(afterRoot, "after") != "renamed-after.log" {
		t.Fatalf("after rename metadata=%#v", afterRoot)
	}
}

func TestComparisonEmptyAndAdmittedUnpositionedCaptures(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.log")
	unpositioned := filepath.Join(dir, "unpositioned.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	line := `{"@level":"info","@module":"terraform.ui","@timestamp":"invalid","type":"apply_complete","hook":{"resource":{"addr":"thing.example","resource_type":"thing"},"action":"read","elapsed_seconds":9}}` + "\n"
	if err := os.WriteFile(unpositioned, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	root, _ := runComparisonJSONDocument(t, empty, unpositioned)
	section := comparisonCLISection(t, root, "ui_operations")
	if section["before_available"] != false || section["after_available"] != true {
		t.Fatalf("availability=%#v", section)
	}
	row := comparisonCLIRow(t, section, "address", "thing.example")
	if row["state"] != "unavailable" || row["before"] != nil {
		t.Fatalf("unpositioned row=%#v", row)
	}
	assertCLIComparisonSummary(t, row["after"], "1", "9000")
}

func rpcCompletion(provider string, duration int) string {
	return `2026-09-11T00:00:00.000Z [TRACE] provider.test: Received downstream response: tf_provider_addr=` + provider + ` tf_resource_type=thing tf_rpc=Read tf_req_duration_ms=` + strconv.Itoa(duration)
}

func uiCompletion(address, action string, elapsed int) string {
	return `{"@level":"info","@module":"terraform.ui","@timestamp":"2026-09-11T00:00:00Z","type":"apply_complete","hook":{"resource":{"addr":"` + address + `","resource_type":"thing"},"action":"` + action + `","elapsed_seconds":` + strconv.Itoa(elapsed) + `}}`
}

func runComparisonJSONDocument(t *testing.T, before, after string) (map[string]any, []byte) {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"--compare", "--format=json", before, after}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("extra JSON: %v", err)
	}
	normaliseJSONNumbers(root)
	return root, out.Bytes()
}

func normaliseJSONNumbers(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			if number, ok := item.(json.Number); ok {
				value[key] = number.String()
			} else {
				normaliseJSONNumbers(item)
			}
		}
	case []any:
		for _, item := range value {
			normaliseJSONNumbers(item)
		}
	}
}

func comparisonCLISection(t *testing.T, root map[string]any, kind string) map[string]any {
	t.Helper()
	for _, value := range root["sections"].([]any) {
		section := value.(map[string]any)
		if section["kind"] == kind {
			return section
		}
	}
	t.Fatalf("section %q not found", kind)
	return nil
}

func comparisonCLIRow(t *testing.T, section map[string]any, field, value string) map[string]any {
	t.Helper()
	for _, item := range section["rows"].([]any) {
		row := item.(map[string]any)
		if row["key"].(map[string]any)[field] == value {
			return row
		}
	}
	t.Fatalf("row %s=%q not found", field, value)
	return nil
}

func assertCLIComparisonSummary(t *testing.T, value any, count, total string) {
	t.Helper()
	if count == "" {
		if value != nil {
			t.Fatalf("summary=%#v, want null", value)
		}
		return
	}
	summary := value.(map[string]any)
	if summary["count"] != count || summary["total_ms"] != total {
		t.Fatalf("summary=%#v, want count=%s total=%s", summary, count, total)
	}
}

func containsCLIString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func comparisonCLIInput(root map[string]any, side string) string {
	return root[side].(map[string]any)["input"].(map[string]any)["basename"].(string)
}
