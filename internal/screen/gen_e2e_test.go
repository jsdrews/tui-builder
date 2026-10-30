package screen

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/fixture/things"
	"github.com/jsdrews/tui-builder/internal/gen"
)

// recorder keeps every request the stub server answers: its query, and
// for writes its method, path and body.
type recorder struct {
	mu     sync.Mutex
	seen   []url.Values
	writes []string // "PATCH /things/1 {...}"
	next   http.Handler
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.seen = append(r.seen, req.URL.Query())
	if req.Method != http.MethodGet {
		body, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		r.writes = append(r.writes, strings.TrimSpace(req.Method+" "+req.URL.Path+" "+string(body)))
	}
	r.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

func (r *recorder) written() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...)
}

func (r *recorder) queries() []url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]url.Values(nil), r.seen...)
}

// genHarness generates a config from the things fixture's spec, pointed
// at its stub server, and runs it.
func genHarness(t *testing.T, token string) *windowHarness {
	h, _ := genHarnessRec(t, token)
	return h
}

func genHarnessRec(t *testing.T, token string) (*windowHarness, *recorder) {
	t.Helper()
	return genHarnessWith(t, token, "")
}

// genHarnessWith is genHarnessRec with an overrides file.
func genHarnessWith(t *testing.T, token, overrides string) (*windowHarness, *recorder) {
	t.Helper()
	rec := &recorder{next: things.New()}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	res, err := gen.Generate(context.Background(), gen.Options{
		Input:     gen.Input{Spec: filepath.Join("..", "fixture", "things", "things.yaml")},
		Doc:       gen.DocOptions{BaseURL: srv.URL},
		Overrides: overrides,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "things.yaml")
	if err := os.WriteFile(path, res.Config, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THINGS_API_TOKEN", token)
	t.Setenv("THINGS_API_URL", "")
	os.Unsetenv("THINGS_API_URL")
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.SubstituteEnv()
	th := theme.New(palette.Nord())
	root, err := BuildMulti(c.TUI.Initial, &Multi{
		Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources, Actions: c.Actions,
	}, build.Selection{}, nil, th)
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
	return h, rec
}

// The generated config runs against the API: the things list renders,
// and enter opens the focused thing's detail.
func TestGeneratedConfigListAndDetail(t *testing.T) {
	h := genHarness(t, things.Token)
	view := h.view()
	for _, want := range []string{"Things", "Owners", "thing-001"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the list didn't render %q:\n%s", want, view)
		}
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnter})
	view = h.view()
	if !strings.Contains(view, "Thing thing-001") || !strings.Contains(view, "owner_id") {
		t.Fatalf("enter didn't open the thing's detail:\n%s", view)
	}
}

// Without the token the API answers 401, and the app says the fetch
// failed rather than hanging.
func TestGeneratedConfigWithoutToken(t *testing.T) {
	h := genHarness(t, "wrong")
	if view := h.view(); !strings.Contains(view, "HTTP 401") {
		t.Fatalf("no failure shown:\n%s", view)
	}
}

// From the owners tab, the sub-list's key in the action menu (a, then
// i) opens the focused owner's things.
func TestGeneratedConfigSubList(t *testing.T) {
	h := genHarness(t, things.Token)
	h.send(tea.KeyMsg{Type: tea.KeyShiftRight})
	if view := h.view(); !strings.Contains(view, "Ada") {
		t.Fatalf("the owners tab didn't render:\n%s", view)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	view := h.view()
	if !strings.Contains(view, "Ada's things") || !strings.Contains(view, "thing-") {
		t.Fatalf("i didn't open the owner's things:\n%s", view)
	}
}

// The things list is a window: it pages past its first 100 rows, sorts on
// the server, and recovers when the server refuses a sort the spec
// lists (status).
func TestGeneratedConfigWindow(t *testing.T) {
	h, rec := genHarnessRec(t, things.Token)
	if q := rec.queries(); len(q) != 1 || q[0].Get("page") != "1" || q[0].Get("per_page") != "100" {
		t.Fatalf("first request: %v", q)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if view := h.view(); !strings.Contains(view, "thing-120") {
		t.Fatalf("G didn't page to the last row:\n%s", view)
	}
	last := func() url.Values { q := rec.queries(); return q[len(q)-1] }
	if last().Get("page") != "2" {
		t.Errorf("paging past 100 asked for %v", last())
	}

	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if got := last().Get("order_by"); got != "name" {
		t.Errorf("s sorted by %q, want name", got)
	}
	// Status is in the spec's ThingOrder enum, but the server refuses it.
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := last().Get("order_by"); got != "status" {
		t.Fatalf("] sorted by %q, want status", got)
	}
	view := h.view()
	if !strings.Contains(view, "can't sort by Status") || !strings.Contains(view, "thing-") {
		t.Errorf("the refused sort didn't revert with a notice:\n%s", view)
	}
	// The next column the server does sort: Created at, then Size.
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := strings.TrimPrefix(last().Get("order_by"), "-"); got != "created_at" {
		t.Errorf("] after the refusal sorted by %q, want created_at", got)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := strings.TrimPrefix(last().Get("order_by"), "-"); got != "size" {
		t.Errorf("] sorted by %q, want size", got)
	}
}

// A filter on Status goes to the server as ?status=.
func TestGeneratedConfigFilter(t *testing.T) {
	h, rec := genHarnessRec(t, things.Token)
	h.typeFilter("status:failed")
	q := rec.queries()
	if got := q[len(q)-1].Get("status"); got != "failed" {
		t.Errorf("status:failed sent %v", q[len(q)-1])
	}
	if view := h.view(); !strings.Contains(view, "failed") || strings.Contains(view, " active ") {
		t.Errorf("the filtered rows:\n%s", view)
	}
}

// The write actions hit their routes: n creates with the form's fields
// (an int input sent as a number), e patches only what was filled in,
// and d deletes the focused row after a confirm.
func TestGeneratedConfigWrites(t *testing.T) {
	h, rec := genHarnessRec(t, things.Token)
	key := func(r rune) { h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}) }

	key('a')
	key('n')
	h.send(form.SubmittedMsg{Values: map[string]any{"name": "new-thing", "owner_id": "2"}})
	if w := rec.written(); len(w) != 1 || w[0] != `POST /things {"name":"new-thing","owner_id":2}` {
		t.Fatalf("create sent %q", w)
	}

	key('a')
	key('e')
	h.send(form.SubmittedMsg{Values: map[string]any{"status": "archived"}})
	if w := rec.written(); len(w) != 2 || w[1] != `PATCH /things/1 {"status":"archived"}` {
		t.Fatalf("edit sent %q", w)
	}

	key('a')
	key('d')
	if view := h.view(); !strings.Contains(view, "Delete thing-001?") {
		t.Fatalf("no confirm before deleting:\n%s", view)
	}
	if w := rec.written(); len(w) != 2 {
		t.Fatalf("deleted before the confirm: %q", w)
	}
	key('y')
	if w := rec.written(); len(w) != 3 || w[2] != "DELETE /things/1" {
		t.Fatalf("delete sent %q", w)
	}
}

// With the overrides golden's file: the hidden __icontains filters reach
// the server, and status, which the server refuses, isn't offered.
func TestGeneratedConfigWithOverrides(t *testing.T) {
	ov := filepath.Join("..", "gen", "testdata", "things_overrides", "overrides.yaml")
	h, rec := genHarnessWith(t, things.Token, ov)
	last := func() url.Values { q := rec.queries(); return q[len(q)-1] }

	h.typeFilter("name:thing-01")
	if got := last().Get("name__icontains"); got != "thing-01" {
		t.Errorf("name:thing-01 sent %v", last())
	}
	if view := h.view(); !strings.Contains(view, "thing-019") || strings.Contains(view, "thing-020") {
		t.Errorf("the filtered rows:\n%s", view)
	}

	h, rec = genHarnessWith(t, things.Token, ov)
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if got := last().Get("order_by"); got != "created_at" {
		t.Errorf("after name, ] sorted by %q; status should be skipped", got)
	}
}
