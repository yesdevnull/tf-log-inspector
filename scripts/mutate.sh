#!/usr/bin/env bash
#
# mutate.sh -- break one behaviour and report which tests notice.
#
# A test that passes tells you it ran, not that it holds anything. This
# applies each mutation from a table to a clean tree, runs the package's
# tests, and reports the failures. A mutation nothing catches names a claim
# the suite does not actually make.
#
# Each mutation is one line of the table, tab-separated:
#
#   <file>\t<literal text to find>\t<text to replace it with>\t<what it breaks>
#
# The find text must appear EXACTLY ONCE in the file, or the mutation is
# refused rather than applied somewhere unintended. The tree is restored from
# git after every mutation and the restore is VERIFIED before the next one --
# an unchecked revert lets mutations accumulate, and then a "caught" result
# can be an earlier mutation's failure rather than this one's.
#
# Usage:
#   scripts/mutate.sh <table-file> [go-test-package...]
#
# Output is one line per mutation: CAUGHT (with the first failing test) or
# SURVIVED. Full test output for each run is kept under the directory named
# in the closing summary.
set -eu
set -o pipefail

usage() {
	cat >&2 <<'USAGE'
usage: scripts/mutate.sh <table-file> [package...]

  <table-file>  tab-separated: file, find, replace, description
  [package...]  packages to test (default: ./...)
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

if ! git diff --quiet -- . || ! git diff --quiet --cached -- .; then
	echo "mutate.sh: the work tree has uncommitted changes; commit or stash them first," >&2
	echo "           because each mutation is reverted with 'git checkout --'." >&2
	exit 1
fi

logs=$(mktemp -d)
n=0
survived=0

while IFS=$'\t' read -r file find replace what; do
	case "$file" in ''|'#'*) continue ;; esac
	n=$((n + 1))
	hits=$(grep -c -F -- "$find" "$file" || true)
	if [ "$hits" != "1" ]; then
		echo "REFUSED  $what -- the find text appears $hits times in $file, want exactly 1"
		continue
	fi
	python3 - "$file" "$find" "$replace" <<'PY'
import sys
path, find, replace = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(path).read()
assert s.count(find) == 1
open(path, 'w').write(s.replace(find, replace))
PY
	out="$logs/mutation-$n.log"
	if go test "$@" >"$out" 2>&1; then
		echo "SURVIVED $what"
		survived=$((survived + 1))
	else
		first=$(grep -m1 -E '^\s*--- FAIL' "$out" | sed 's/^ *//' || true)
		echo "CAUGHT   $what  <-  ${first:-build failure}"
	fi
	git checkout -- "$file"
	git diff --quiet -- "$file" || {
		echo "mutate.sh: $file did not revert cleanly; stopping before results become untrustworthy" >&2
		exit 1
	}
done <"$table"

echo
echo "$n mutations, $survived survived. Full output: $logs"
[ "$survived" -eq 0 ]
