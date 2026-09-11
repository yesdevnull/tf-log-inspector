# Repository Guidelines

## Project Structure & Module Organisation

`tfli` is a Go 1.25+ CLI for inspecting Terraform logs. `cmd/tfli` contains
argument handling and mode dispatch. Under `internal/`, `logfmt` parses logs,
`span` extracts timings, `attrib` correlates resource addresses, and `model`
loads, filters and aggregates data. `diagnose` and `profile` produce reports;
`tui` implements the Bubble Tea/Lip Gloss terminal interface. Keep model logic
independent of rendering and CLI flags.

Tests live beside source files as `*_test.go`. Shared log fixtures are in
`testdata/`; package fixtures and terminal snapshots live under package
`testdata/` directories. Design references are in `docs/superpowers/specs/`;
JSON contracts and release notes are in `docs/`. Completed implementation plans
are retained in Git history.

## Build, Test, and Development Commands

Run from the repository root:

- `go build ./...` — compile all packages.
- `go test ./...` — run the complete test suite.
- `go test ./internal/tui -run TestLayoutDegradesByWidth` — run a focused test.
- `go run ./cmd/tfli testdata/mixed-hcp.log` — open the terminal interface.
- `go run ./cmd/tfli --diagnose testdata/mixed-hcp.log` — inspect log structure.
- `go run ./cmd/tfli --profile testdata/mixed-hcp.log` — print timing rankings.

## Coding Style & Naming Conventions

Use `gofmt` for Go formatting, including tab indentation. Follow existing
package names, exported `MixedCaps` identifiers and unexported `mixedCaps`
names. Choose domain names and explain purpose and constraints in comments.
Use British/Australian spelling in prose. Keep changes small; avoid introducing
new dependencies or tooling without discussion.

## Testing Guidelines

Use Go's standard `testing` package and descriptive `Test...` names. Follow TDD
for features and fixes, exercising real parsing, model and rendering behaviour.
Cover edge cases and error output; no numeric coverage threshold is configured.

TUI goldens are in `internal/tui/testdata/golden/`. Regenerate intentionally with
`go test ./internal/tui -update`, then inspect with
`scripts/read-golden.sh layout-100.txt` and review the raw diff for styling.
Never accept regenerated output solely to silence a failure.

## Commit & Pull Request Guidelines

Use a topic branch and signed commits. Recent history uses imperative subjects
such as “Name…” and “Correct…”, without a mandatory prefix. Describe the concrete
behaviour changed. PRs should explain the problem, resulting behaviour and
validation; link relevant issues and include reviewed terminal output for UI
changes. Run the full suite before submission.

## Log Privacy

Use sanitised fixtures. Profile reports expose resource addresses, and the TUI
shows raw log content. Review even masked diagnose reports before sharing;
never commit private captures or screenshots containing secrets.
