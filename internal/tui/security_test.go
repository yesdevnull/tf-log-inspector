package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yesdevnull/tf-log-inspector/internal/logfmt"
	"github.com/yesdevnull/tf-log-inspector/internal/model"
	"github.com/yesdevnull/tf-log-inspector/internal/span"
)

func TestLoadedStructuredFieldsCannotControlTheTerminal(t *testing.T) {
	const control = "\x1b[2J\r\a\u009b"
	for _, field := range []string{"action", "implied_provider", "resource_type", "addr"} {
		t.Run(field, func(t *testing.T) {
			resource := map[string]any{"implied_provider": "local", "resource_type": "local_file", "addr": "local_file.example"}
			hook := map[string]any{"action": "read", "elapsed_seconds": 1, "resource": resource}
			if field == "action" {
				hook[field] = control
			} else {
				resource[field] = control
			}
			line, err := json.Marshal(map[string]any{
				"@level": "info", "@timestamp": "2026-09-04T09:15:02Z", "type": "apply_complete", "hook": hook,
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "capture.log")
			if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			l, err := model.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			m := New(l, path)
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m.setView(ViewTimeline)
			out := m.View()
			for _, unsafe := range []string{"\x1b[2J", "\r", "\a", "\u009b"} {
				if strings.Contains(out, unsafe) {
					t.Errorf("decoded %s emitted terminal control %q", field, unsafe)
				}
			}
			if !strings.Contains(out, `\x1b[2J`) {
				t.Error("escaped field is not visible in the frame")
			}
			if len(l.UISpans) != 1 || (field == "addr" && l.UISpans[0].Address != control) {
				t.Fatal("display sanitisation changed the loaded span identity")
			}
		})
	}
}

func TestTableAndFacetValuesCannotControlTheTerminal(t *testing.T) {
	const control = "\x1b[2J\a"
	l := &model.Log{RPCSpans: []span.Span{{Provider: control, RPC: control, ResourceType: control}}}
	m := New(l, "x.log")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	for _, view := range []View{ViewProviders, ViewTypes, ViewCalls} {
		m.setView(view)
		out := m.View()
		if strings.Contains(out, "\x1b[2J") || strings.ContainsRune(out, '\a') {
			t.Errorf("view %d emitted a terminal control from table or facet values", view)
		}
	}
}

func TestRawLogDisplaysControlCharactersSafely(t *testing.T) {
	data := []byte("before\r\b\a\u009bafter\n")
	l := &model.Log{Data: data, Entries: []logfmt.Entry{{Len: uint32(len(data))}}}
	m := New(l, "x.log")
	if out := m.renderRawLog(100, 1); out != `before\r\b\a\u009bafter` {
		t.Errorf("raw control characters rendered as %q", out)
	}
}

func TestFilenameAndSearchTextCannotControlTheTerminal(t *testing.T) {
	const control = "\x1b]52;c;U0VDUkVU\a"
	m := New(&model.Log{}, control+".log")
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.setView(ViewRawLog)
	m.raw.searching, m.raw.query = true, control
	out := m.View()
	if strings.Contains(out, control) {
		t.Fatal("filename or search prompt emitted an OSC52 command")
	}
	if strings.Count(out, `\x1b]52;c;U0VDUkVU\a`) != 2 {
		t.Fatal("filename and search prompt must display escaped controls")
	}
	m.raw.searching, m.raw.notFound, m.raw.lastQuery = false, true, control
	if strings.Contains(m.View(), control) {
		t.Fatal("failed-search message emitted an OSC52 command")
	}
}
