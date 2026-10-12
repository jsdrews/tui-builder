package screen

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/remote"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// The anchored example's "Jump to second" field: committing a second
// re-anchors the log there.
func TestCommittedInputJumpsTheLog(t *testing.T) {
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "anchored.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// No scroll pause or new-item highlight: the pump would wait out
	// each timer.
	c.Data.Sources["app_log"].Window.ViewportDelay = "-"
	c.TUI.Components["log"].NewFor = "-"
	m := newTestModel(t, c)
	settle(t, m, drainCmd(m.Init())...)
	settle(t, m, drainCmd(m.OnEnter(nil))...)
	holds := func(id string) bool {
		_, items := m.tree.Components["log"].Eventlog.Items()
		for _, it := range items {
			if _, key := build.SplitAnchoredKey(it.Key); key == id {
				return true
			}
		}
		return false
	}
	if !holds("req-0999") || holds("req-0200") {
		t.Fatal("the log should open at the newest line")
	}
	settle(t, m, tea.KeyMsg{Type: tea.KeyTab})
	settle(t, m, keys("100")...)
	settle(t, m, enter)
	if !holds("req-0199") {
		t.Error("after jumping to second 100, the lines just before it should be held")
	}
	if holds("req-0999") {
		t.Error("the jump should replace the newest lines, not add to them")
	}
}

// A screen can't see itself popped, so the screen under it cancels it
// when it's on top again.
func TestReturningFromAPushCancelsThePushedScreen(t *testing.T) {
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "inputs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	parent := newTestModel(t, c)
	child := newTestModel(t, c)
	parent.pushed = append(parent.pushed, cancelOf(child))
	parent.OnEnter(nil)
	select {
	case <-child.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("the pushed screen's context is still live after returning")
	}
	if parent.ctx.Err() != nil {
		t.Error("the screen returned to should keep running")
	}
}

type findStub struct {
	ds.Source
	got ds.FindQuery
	res ds.FindResult
}

func (f *findStub) FindItem(_ context.Context, q ds.FindQuery) (ds.FindResult, error) {
	f.got = q
	return f.res, nil
}

func TestFindAdapters(t *testing.T) {
	declared := &cfg.WindowConfig{Find: &cfg.FindConfig{Params: map[string]any{"q": "x"}}}
	stub := &findStub{res: ds.FindResult{Found: true, Position: 7, Cursor: `"c9"`, Item: map[string]any{"id": "e9"}}}

	if seekableFind(stub, &cfg.WindowConfig{}, nil) != nil {
		t.Error("without find:, search should stay on what's loaded (nil Find)")
	}
	at, found, err := seekableFind(stub, declared, nil)(context.Background(), remote.Find{Term: "web", Newer: true, From: 3})
	if err != nil || !found || at != 7 {
		t.Errorf("seekable find = %d, %v, %v", at, found, err)
	}
	if stub.got.Term != "web" || !stub.got.Newer || stub.got.From != 3 {
		t.Errorf("query = %+v", stub.got)
	}

	log := &build.Component{Kind: build.KEventlog, Cfg: &cfg.Component{Key: cfg.Path{"id"}}}
	key, found, err := anchoredFind(stub, declared, log, nil)(context.Background(), remote.Find{Cursor: build.AnchoredKey(`"c1"`, "e1")})
	if err != nil || !found || key != build.AnchoredKey(`"c9"`, "e9") {
		t.Errorf("anchored find = %q, %v, %v; want the match keyed as the view keys it", key, found, err)
	}
	if stub.got.Cursor != `"c1"` {
		t.Errorf("the current item's cursor should travel without its key, got %q", stub.got.Cursor)
	}
}
