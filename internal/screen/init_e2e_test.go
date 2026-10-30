package screen

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/palette"
	tscreen "github.com/jsdrews/tuilib/pkg/screen"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/fixture/things"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

// initHarness runs `tui-builder init <kind>`'s config headless.
func initHarness(t *testing.T, kind string) *windowHarness {
	t.Helper()
	ex, err := scaffold.Kind(kind, "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tui-builder.yaml")
	if err := os.WriteFile(path, ex.Config, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.SubstituteEnv()
	th := theme.New(palette.Nord())
	var root tscreen.Screen
	if len(c.TUI.Screens) > 0 {
		root, err = BuildMulti(c.TUI.Initial, &Multi{
			Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources, Actions: c.Actions,
		}, build.Selection{}, nil, th)
	} else {
		root, err = Build(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, th)
	}
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Palettes: []palette.Palette{palette.Nord()}, SkipConfig: true,
		ActionsKey: key.NewBinding(key.WithKeys("a")),
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	h := &windowHarness{t: t, model: m}
	h.drain(h.model.Init())
	return h
}

// init table renders its rows.
func TestInitTableRuns(t *testing.T) {
	view := initHarness(t, "table").view()
	for _, want := range []string{"Services", "Replicas", "worker", "us-east"} {
		if !strings.Contains(view, want) {
			t.Fatalf("init table didn't render %q:\n%s", want, view)
		}
	}
}

// init menu opens the screen its first item names, and esc comes back.
func TestInitMenuRuns(t *testing.T) {
	h := initHarness(t, "menu")
	if view := h.view(); !strings.Contains(view, "Sections") || !strings.Contains(view, "About") {
		t.Fatalf("init menu didn't render the menu:\n%s", view)
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnter})
	if view := h.view(); !strings.Contains(view, "pending") {
		t.Fatalf("enter didn't open Services:\n%s", view)
	}
	h.send(tea.KeyMsg{Type: tea.KeyEsc})
	if view := h.view(); !strings.Contains(view, "About") {
		t.Fatalf("esc didn't come back to the menu:\n%s", view)
	}
}

// init http reads the things fixture: the first page of things renders.
func TestInitHTTPRuns(t *testing.T) {
	srv := httptest.NewServer(things.New())
	t.Cleanup(srv.Close)
	t.Setenv("THINGS_API_URL", srv.URL)
	t.Setenv("THINGS_API_TOKEN", things.Token)
	view := initHarness(t, "http").view()
	for _, want := range []string{"Things", "thing-001", "active"} {
		if !strings.Contains(view, want) {
			t.Fatalf("init http didn't render %q:\n%s", want, view)
		}
	}
}

// init websocket reads the fixture's event stream: the first frame
// becomes a row.
func TestInitWebsocketRuns(t *testing.T) {
	every := things.EventEvery
	things.EventEvery = time.Hour
	t.Cleanup(func() { things.EventEvery = every })
	srv := httptest.NewServer(things.New())
	t.Cleanup(srv.Close)
	t.Setenv("THINGS_EVENTS_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/events")
	t.Setenv("THINGS_API_TOKEN", things.Token)
	view := initHarness(t, "websocket").view()
	if !strings.Contains(view, "Thing statuses") || !strings.Contains(view, "thing-001") {
		t.Fatalf("init websocket didn't render the first event:\n%s", view)
	}
}
