#!/bin/sh
# read-golden.sh -- print a TUI golden file as plain text.
#
# The goldens under internal/tui/testdata/golden hold what the interface
# actually renders, escape sequences included, so that a change to the
# theme shows up as a golden change rather than passing unnoticed. That
# makes them hard to read directly: `cat` renders the styling into your
# terminal and hides the frame's structure, and `cat -v` spells every
# escape out in the middle of the line you are trying to read.
#
# This strips the styling and leaves the layout, which is what you want
# when reviewing a golden by eye or working out why one moved. It says
# nothing about the styling itself -- diff the file for that.
#
# Usage:
#   scripts/read-golden.sh layout-100.txt
#   scripts/read-golden.sh internal/tui/testdata/golden/help-60.txt
#   scripts/read-golden.sh            # lists what there is to read
set -eu
# pipefail so a failure inside the listings below is reported rather than
# masked by the exit status of the last command in the pipeline.
set -o pipefail 2>/dev/null || true

root=$(git rev-parse --show-toplevel 2>/dev/null) || {
	echo "$0: not inside the tf-log-inspector repository -- run it from a checkout" >&2
	exit 1
}
dir=$root/internal/tui/testdata/golden

if [ $# -ne 1 ]; then
	echo "usage: $0 <golden>" >&2
	echo "goldens in $dir:" >&2
	ls "$dir" | sed 's/^/  /' >&2
	exit 2
fi

file=$1
[ -f "$file" ] || file=$dir/$1
if [ ! -f "$file" ]; then
	echo "$0: no such golden: $1" >&2
	echo "try one of:" >&2
	ls "$dir" | sed 's/^/  /' >&2
	exit 1
fi

# ESC [ ... final-byte, which covers every SGR sequence the theme emits.
sed 's/'"$(printf '\033')"'\[[0-9;]*[A-Za-z]//g' "$file"
