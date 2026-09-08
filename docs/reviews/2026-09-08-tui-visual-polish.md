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
