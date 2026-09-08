#!/usr/bin/env bash
#
# mutate.sh -- break one behaviour and report which tests notice.
#
# A test that passes tells you it ran, not that it holds anything. This
# applies each mutation from a table to a clean tree, runs the tests, and
# reports what failed. A mutation nothing catches names a claim the suite
# does not actually make.
#
# The table is a JSON array -- not a line-based format, because the text
# worth mutating is often several lines of a function body:
#
#   [{"file": "internal/tui/facets.go",
#     "find": "if !excluded[v.Value] {",
#     "replace": "if excluded[v.Value] {",
#     "what": "the allow-list keeps what was NOT excluded"}]
#
# find must appear EXACTLY ONCE in the file or the mutation is refused
# rather than applied somewhere unintended, and a refusal fails the run: a
# mutation that never ran is not a mutation that was caught. The tree is
# restored from git after each one and the restore is VERIFIED before the
# next -- an unchecked revert lets mutations accumulate, and a "caught"
# result is then an earlier mutation's failure rather than this one's.
# Targets must be tracked regular files within the repository. The original
# tests must pass before any mutation runs; errors and catchable interrupts
# restore the active target before exiting.
#
# Usage:
#   scripts/mutate.sh <table.json> [package...]
# Requires Bash, Python 3.9 or later, Git and the project's Go toolchain.
#
# One line per mutation: CAUGHT (with the first test that failed) or
# SURVIVED. Full output per run is kept under the directory the summary
# names. Exit status is 0 only when every mutation was applied and caught.
set -eu
set -o pipefail

usage() {
	cat >&2 <<'USAGE'
usage: scripts/mutate.sh <table.json> [package...]

  <table.json>  JSON array of {file, find, replace, what}
  [package...]  packages to test (default: ./...)

Requires Bash, Python 3.9 or later, Git and the project's Go toolchain.
USAGE
	exit 2
}

[ $# -ge 1 ] || usage
table=$1
shift
[ -f "$table" ] || { echo "mutate.sh: no such table: $table" >&2; exit 1; }
[ $# -gt 0 ] || set -- ./...

root=$(git rev-parse --show-toplevel 2>/dev/null) || {
	echo "mutate.sh: not inside a git work tree -- the revert depends on one" >&2
	exit 1
}
cd "$root"

if ! git diff --quiet || ! git diff --quiet --cached; then
	echo "mutate.sh: the work tree has uncommitted changes; commit or stash them first," >&2
	echo "           because each mutation is reverted with 'git checkout --'." >&2
	exit 1
fi

# Validate the entire table before changing any file: a clean diff says
# nothing about untracked files, which checkout cannot restore.
count=$(python3 - "$table" <<'PY'
import json, pathlib, subprocess, sys
mutations = json.load(open(sys.argv[1]))
root = pathlib.Path.cwd()
tracked = {}
for entry in subprocess.check_output(["git", "ls-files", "--stage", "-z"]).split(b"\0"):
    if entry:
        metadata, name = entry.split(b"\t", 1)
        mode, _, stage = metadata.split()
        if stage == b"0" and mode in (b"100644", b"100755"):
            tracked[name.decode()] = True
for m in mutations:
    target = root / m["file"]
    resolved = target.resolve()
    if (not resolved.is_relative_to(root) or resolved != target.absolute()
            or not target.is_file() or str(resolved.relative_to(root)) not in tracked):
        sys.exit(f"mutate.sh: target must be a tracked regular file within the repository: {m['file']}")
print(len(mutations))
PY
)
logs=$(mktemp -d)
if ! go test "$@" >"$logs/baseline.log" 2>&1; then
	echo "mutate.sh: baseline tests failed; no mutations applied. Full output: $logs/baseline.log" >&2
	exit 1
fi
survived=0
refused=0
active=""

restore_active() {
	[ -n "$active" ] || return 0
	if ! git --literal-pathspecs checkout -- "$active" || ! git --literal-pathspecs diff --quiet -- "$active"; then
		echo "mutate.sh: $active did not revert cleanly; stopping before results become untrustworthy" >&2
		return 1
	fi
	active=""
}

cleanup() {
	local result=$?
	trap - EXIT
	# A second interrupt must not interrupt restoration itself.
	trap '' HUP INT TERM
	if ! restore_active; then
		result=1
	fi
	exit "$result"
}

trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

i=0
while [ "$i" -lt "$count" ]; do
	file=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[int(sys.argv[2])]["file"])' "$table" "$i")
	what=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[int(sys.argv[2])]["what"])' "$table" "$i")
	active=$file
	if python3 - "$table" "$i" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))[int(sys.argv[2])]
s = open(m["file"]).read()
n = s.count(m["find"])
if n != 1:
    print(f"found {n} times, want exactly 1", file=sys.stderr)
    sys.exit(1)
open(m["file"], "w").write(s.replace(m["find"], m["replace"]))
PY
	then
		out="$logs/mutation-$i.log"
		if go test "$@" >"$out" 2>&1; then
			echo "SURVIVED $what"
			survived=$((survived + 1))
		else
			# A persistent tool or environment failure is not evidence that
			# the mutation was caught. Check the restored source as a control.
			restore_active
			if ! go test "$@" >"$logs/baseline-after-$i.log" 2>&1; then
				echo "mutate.sh: restored baseline failed; mutation result is inconclusive. Full output: $logs/baseline-after-$i.log" >&2
				exit 1
			fi
			first=$(grep -m1 -E '^[[:space:]]*--- FAIL' "$out" | sed 's/^ *//' || true)
			echo "CAUGHT   $what  <-  ${first:-build failure}"
		fi
	else
		echo "REFUSED  $what -- its find text does not appear exactly once in $file"
		refused=$((refused + 1))
	fi
	restore_active
	i=$((i + 1))
done

echo
echo "$count mutations: $((count - survived - refused)) caught, $survived survived, $refused refused."
echo "Full output: $logs"
[ "$survived" -eq 0 ] && [ "$refused" -eq 0 ]
