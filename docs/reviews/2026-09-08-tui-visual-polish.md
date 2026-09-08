# TUI visual polish

Dan approved a focused visual update using the existing Bubble Tea v1 and
Lip Gloss stack with Bubbles v0.21.0 components.

## Changes

- Keep the active view visible in a highlighted navigation tab.
- Mark keyboard focus with an arrow and highlighted pane title.
- Count excluded facet values in the filter title.
- Use Bubbles text input for cursor movement, deletion and scrolling search
  queries, with terminal controls escaped before display.
- Render contextual action help through Bubbles, fitting whole hints while
  reserving space for quit on narrow terminals.

## Verification

New behaviour was tested red before implementation. The full race suite,
`go build ./...`, `go vet ./...` and the TUI snapshot suite passed.
All eight changed terminal snapshots were reviewed, including the 60-column
timeline. A real PTY session exercised view switching, search entry, cursor
movement, submission and clean exit.

Independent code review found no actionable issues. Additional overlay probes
covered cursor positions in CJK, combining-accent and emoji queries at widths
3–20. Independent test cleanup retained all 16 touched tests; TUI coverage
was 96.9%. Two assertions were strengthened to associate the highlight and
focus marker with their intended targets.

## Modern workbench redesign

Dan selected a modern workbench direction for a broader structural update.
Navigation sits above independent rounded panels, with inset content,
sentence-case titles and an accented border on the focused panel. File
identity and span counts have separate visual weight. A compact status bar
qualifies timings or reports the Raw Log position.

Bubbles viewport makes the help guide scrollable, including the full timing
explanation. Persistent scroll hints make this discoverable on short screens.
Raw Log retains horizontal scrolling and omits the Detail panel.

All ten terminal goldens were regenerated and reviewed. A colour-rendered
preview and live PTY checks covered the panel layout and help paging.
The full race suite, build and vet passed. Independent review found a header
separator bug for filenames containing ` · `; a red-green regression and
final-separator split resolved it. Independent test cleanup retained the
meaningful coverage and corrected one content-width assertion. TUI coverage
is 97.0%, with no outstanding review findings.

## PR #2 review fixes

Verified both Copilot findings with failing regression tests. Four-column
panels now keep their rounded top border and focus marker. Empty Raw Log
status retains its count and scope without claiming a line or column.
The TUI suite, full race suite, build and vet pass. Independent review found
no remaining issues in these fixes.
Independent test cleanup retained all 53 added or modified test functions;
TUI coverage remains 97.0%.
