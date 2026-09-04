// Package tui is the terminal interface for tfli. It is the only package in
// this project permitted to import a third-party dependency: internal/model,
// internal/profile, internal/span, internal/logfmt and internal/diagnose
// remain dependency-free and terminal-unaware.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
)

// Model is the bubbletea model for tfli's full-screen interface. It wraps a
// loaded log; nothing here mutates the log, so a Model can be freely copied
// the way bubbletea's value-receiver Update requires.
type Model struct {
	log  *model.Log
	name string

	width, height int
	quitting      bool
}

// New builds the model for l, loaded from path. Only path's base name is
// kept: the header names the file the user is looking at, not where it lives
// on disk.
func New(l *model.Log, path string) Model {
	return Model{log: l, name: filepath.Base(path)}
}

// Quitting reports whether a quit key has been handled. Tests use this
// rather than reaching into Update's returned tea.Cmd, since tea.Quit is a
// sentinel command and not itself inspectable state.
func (m Model) Quitting() bool {
	return m.quitting
}

// Init starts no commands: the model has everything it needs from New, and
// nothing here depends on bubbletea's runtime to load.
func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	}
	return m, nil
}

// View renders the header naming the file and its span counts, followed by
// the observer-effect caveat.
func (m Model) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tfli -- %s\n", m.name)
	fmt.Fprintf(&b, "%d RPC spans, %d UI spans\n\n", len(m.log.RPCSpans), len(m.log.UISpans))
	writeLoggingCaveat(&b)
	fmt.Fprintf(&b, "\nq to quit\n")
	return b.String()
}

// writeLoggingCaveat states that every duration this interface renders was
// measured under logging. Terraform re-logs each line of a provider's stderr
// through its own logger, so a provider that dumps HTTP bodies at DEBUG pays
// that cost per line: four captures of one workspace measured 24.1s with no
// logging enabled against 522.2s with debug plus provider TRACE. A reader
// who mistook these figures for wall-clock truth would be optimising time
// that does not exist without the log, so the caveat travels with every
// rendered duration rather than living only in documentation.
func writeLoggingCaveat(b *strings.Builder) {
	fmt.Fprintf(b, "Durations here are measured under logging, which is not\n")
	fmt.Fprintf(b, "free: one workspace planned in 24.1s unlogged and 522.2s\n")
	fmt.Fprintf(b, "with debug plus provider TRACE. Rankings hold, since every\n")
	fmt.Fprintf(b, "span paid the same cost, but absolute times do not transfer\n")
	fmt.Fprintf(b, "to an unlogged run.\n")
}

// Run opens the full-screen interface for l, loaded from path, and blocks
// until the user quits.
func Run(l *model.Log, path string) error {
	p := tea.NewProgram(New(l, path), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
