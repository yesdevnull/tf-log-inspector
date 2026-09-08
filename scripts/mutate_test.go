package scripts_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type mutation struct {
	File    string `json:"file"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
	What    string `json:"what"`
}

// Each run uses a real checkout and real Go tests. No mutation can touch the
// working checkout, and cloning existing history requires no test commits.
func mutationCheckout(t *testing.T) (string, string, []string) {
	t.Helper()
	source, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if wrapper := "/Users/dan/.codex/bin/codex-git"; exists(wrapper) {
		git = wrapper
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	cmd := exec.Command(git, "clone", "--quiet", "--local", "--no-hardlinks", source, repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TMPDIR="+dir)
	return repo, filepath.Join(source, "scripts", "mutate.sh"), env
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mutationCommand(t *testing.T, repo, script string, env []string, mutations []mutation, args ...string) *exec.Cmd {
	t.Helper()
	data, err := json.Marshal(mutations)
	if err != nil {
		t.Fatal(err)
	}
	table := filepath.Join(t.TempDir(), "mutations.json")
	if err := os.WriteFile(table, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script, table}, args...)...)
	cmd.Dir, cmd.Env = repo, env
	return cmd
}

func TestMutationRejectsUnrestorableTargetsBeforeWriting(t *testing.T) {
	repo, script, env := mutationCheckout(t)
	for _, file := range []string{"untracked.txt", "../outside.txt", "linked.txt"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(repo, file)
			if file == "linked.txt" {
				if err := os.Symlink("untracked.txt", path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("valuable original content\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := mutationCommand(t, repo, script, env, []mutation{
				{"internal/model/rollup.go", "const noKey = \"(none)\"", "const noKey = \"(none)\"", "valid first target"},
				{file, "valuable original content", "BROKEN", "unrestorable target"},
			}, "./internal/model")
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "target") {
				t.Errorf("want target rejection, got %v\n%s", err, out)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "valuable original content\n" {
				t.Errorf("target was damaged: %q, %v", got, err)
			}
			if strings.Contains(string(out), "SURVIVED") || strings.Contains(string(out), "CAUGHT") {
				t.Errorf("applied a mutation before validating every target:\n%s", out)
			}
		})
	}
}

func TestMutationRejectsFailingBaseline(t *testing.T) {
	repo, script, env := mutationCheckout(t)
	cmd := mutationCommand(t, repo, script, env, []mutation{
		{"internal/model/rollup.go", "const noKey = \"(none)\"", "const noKey = \"(none)\"", "no behaviour changed"},
	}, "./does-not-exist")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "baseline") || strings.Contains(string(out), "CAUGHT") {
		t.Fatalf("want failing baseline, not a caught mutation; got %v\n%s", err, out)
	}
}

func TestMutationDoesNotCountPersistentToolFailureAsCaught(t *testing.T) {
	repo, script, env := mutationCheckout(t)
	bin := filepath.Join(filepath.Dir(repo), "bin")
	for _, name := range []string{"go", "python3"} {
		tool, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(tool, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Removing this private link simulates losing the real tool during a
	// run without changing the installed tool or substituting a fake one.
	env = append(env, "PATH="+bin+":/usr/bin:/bin")
	path := filepath.Join(repo, "internal/model/rollup.go")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(before), "\"sort\"", "\"sort\"\n\"os\"", 1)
	broken = strings.Replace(broken, "return noKey", fmt.Sprintf("os.Remove(%q); return \"missing\"", filepath.Join(bin, "go")), 1)
	cmd := mutationCommand(t, repo, script, env, []mutation{
		{"internal/model/rollup.go", string(before), broken, "tool unavailable after running mutant"},
	}, "./internal/model")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "baseline") || strings.Contains(string(out), "CAUGHT") {
		t.Errorf("want persistent tool failure, got %v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("tool failure left mutation in source: %v", err)
	}
}

func TestMutationRestoresAfterProcessGroupInterruption(t *testing.T) {
	repo, script, env := mutationCheckout(t)
	path := filepath.Join(repo, "internal/model/rollup.go")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "test-running")
	blocked := strings.Replace(string(before), "\"sort\"", "\"sort\"\n\"os\"", 1)
	blocked = strings.Replace(blocked, "return noKey", fmt.Sprintf("os.WriteFile(%q, nil, 0600); for {}", ready), 1)
	cmd := mutationCommand(t, repo, script, env, []mutation{
		{"internal/model/rollup.go", string(before), blocked, "block the test until interrupted"},
	}, "./internal/model")
	// Start a separate process group and interrupt it only after the real
	// mutated test is running, so neither the baseline nor a timing guess
	// can turn this into a test of cancelling before any write.
	const interrupt = `
import os, pathlib, signal, subprocess, sys, time
p = subprocess.Popen(sys.argv[2:], start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
observed = False
try:
    deadline = time.monotonic() + 30
    while p.poll() is None and time.monotonic() < deadline:
        if pathlib.Path(sys.argv[1]).exists():
            observed = True
            break
        time.sleep(0.005)
    if p.poll() is None:
        os.killpg(p.pid, signal.SIGTERM)
    output, _ = p.communicate(timeout=15)
    print(output.decode(), end="")
    if not observed or p.returncode == 0:
        sys.exit("did not interrupt an active mutation")
finally:
    if p.poll() is None:
        os.killpg(p.pid, signal.SIGKILL)
        p.wait()
`
	probe := exec.Command("python3", append([]string{"-c", interrupt, ready}, cmd.Args...)...)
	probe.Dir, probe.Env = cmd.Dir, cmd.Env
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("interrupt mutation: %v\n%s", err, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("interrupted mutation left source changed: %v", err)
	}
}

func TestMutationReportsOutcomesAndRestoresSource(t *testing.T) {
	repo, script, env := mutationCheckout(t)
	path := filepath.Join(repo, "internal/model/rollup.go")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		find    string
		replace string
		failed  bool
	}{
		{"CAUGHT", "return noKey", "return \"missing\"", false},
		{"SURVIVED", "return noKey", "return noKey", true},
		{"REFUSED", "no such source fragment", "unused", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := mutationCommand(t, repo, script, env, []mutation{
				{"internal/model/rollup.go", tc.find, tc.replace, "check outcome"},
			}, "./internal/model")
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.failed || !strings.Contains(string(out), tc.name+" ") {
				t.Fatalf("want %s (failure=%t), got %v\n%s", tc.name, tc.failed, err, out)
			}
			if tc.name == "REFUSED" && !strings.Contains(string(out), "found 0 times, want exactly 1") {
				t.Errorf("missing refusal explanation:\n%s", out)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("source changed after %s: %v", tc.name, err)
			}
		})
	}
}
