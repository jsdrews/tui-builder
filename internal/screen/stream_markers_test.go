package screen

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// streamLogModel binds a logview to an exec stream that prints two lines
// and exits.
func streamLogModel(t *testing.T) *Model {
	t.Helper()
	comps := map[string]*cfg.Component{"log": {Type: "logview", Source: "tail"}}
	sources := map[string]*cfg.Source{"tail": cfg.NewEntry(&cfg.Source{
		Type: "exec", Follow: true, Command: []string{"sh", "-c", "echo one; echo two"},
	})}
	sc := &cfg.Screen{Layout: cfg.Node{Component: "log"}}
	m, err := New(sc, comps, sources, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func logLines(m *Model) []string {
	var out []string
	for _, l := range m.tree.Components["log"].Logview.Lines() {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

// A stream that ends says so in the log, and `r` reconnects it rather
// than refetching — which, for a stream, only cleared what it had shown.
func TestStreamEndAndReconnectAreMarked(t *testing.T) {
	m := streamLogModel(t)
	runToQuiescence(t, m, drainCmd(m.OnEnter(nil))...)

	got := strings.Join(logLines(m), "|")
	if !strings.Contains(got, "one|two") || !strings.HasSuffix(got, "stream ended —") {
		t.Fatalf("after the stream ends, log = %q; want its lines, then the ended marker", got)
	}

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	got = strings.Join(logLines(m), "|")
	if !strings.HasPrefix(got, "one|two|") {
		t.Errorf("r should keep what the stream showed, log = %q", got)
	}
	if !strings.Contains(got, "— reconnected —|one|two") {
		t.Errorf("r should reconnect with a marker and stream again, log = %q", got)
	}
}

// A column's `distinct: {source:}` is fetched and polled like a bound
// source, though nothing displays it, and its answers go to that column.
func TestDistinctSourceIsFetchedForItsColumn(t *testing.T) {
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"books": cfg.NewEntry(&cfg.Source{Type: "http", URL: "http://127.0.0.1:1/books",
				Window: &cfg.WindowConfig{OffsetParam: "offset", LimitParam: "limit", SearchParam: "q", TotalPath: "total"}}),
			"genres": cfg.NewEntry(&cfg.Source{Type: "static", Data: []any{"fiction", "history"}}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{"books": {
				Type: "table", Source: "books", Filterable: true,
				Columns: []cfg.Column{
					{Title: "Title", Value: cfg.Path{"title"}},
					{Title: "Genre", Value: cfg.Path{"genre"}, Distinct: &cfg.Distinct{Source: "genres"}},
				},
			}},
			Screen: cfg.Screen{Layout: cfg.Node{Component: "books"}},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	m, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.sources["genres"]; !ok {
		t.Fatal("the distinct source should be registered for fetching")
	}
	uses := m.distinctUsers["genres"]
	if len(uses) != 1 || uses[0].component != "books" || uses[0].column != 1 {
		t.Errorf("distinct uses = %+v, want books column 1", uses)
	}
}
