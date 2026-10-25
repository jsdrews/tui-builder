package screen

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/poll"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func polledModel(t *testing.T) *Model {
	t.Helper()
	sources := map[string]*cfg.Source{
		"a": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"echo", `["x"]`}, Refresh: "1h"}),
		"b": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"echo", `["y"]`}, Refresh: "1h"}),
		"c": cfg.NewEntry(&cfg.Source{Type: "exec", Command: []string{"echo", `["z"]`}}),
	}
	comps := map[string]*cfg.Component{
		"la": {Type: "list", Source: "a", Item: ""},
		"lb": {Type: "list", Source: "b", Item: ""},
		"lc": {Type: "list", Source: "c", Item: ""},
	}
	sc := &cfg.Screen{Layout: cfg.Node{VStack: []cfg.Item{
		{Node: cfg.Node{Component: "la"}}, {Node: cfg.Node{Component: "lb"}}, {Node: cfg.Node{Component: "lc"}},
	}}}
	m, err := New(sc, comps, sources, nil, theme.New(palette.Nord()))
	if err != nil {
		t.Fatal(err)
	}
	m.OnEnter(nil)
	return m
}

// Each source that refreshes gets its own poller; a one-shot source
// doesn't, and arming again (another OnEnter) doesn't add a second.
func TestPollersPerRefreshingSource(t *testing.T) {
	m := polledModel(t)
	if m.sources["a"].poll == nil || m.sources["b"].poll == nil {
		t.Fatal("both refreshing sources should be polled")
	}
	if m.sources["c"].poll != nil {
		t.Error("a one-shot source shouldn't be polled")
	}
	first := m.sources["a"].poll
	m.startPolls()
	if m.sources["a"].poll != first {
		t.Error("arming again replaced the poller, which would start a second tick chain")
	}
}

// A refresh fetches its own source, once: a second refresh while that
// fetch is out is dropped instead of stacking behind it.
func TestPollRefreshFetchesOnce(t *testing.T) {
	m := polledModel(t)
	m.sources["a"].inflight = false
	if cmd := m.handlePoll(pollMsg{source: "a", msg: poll.RefreshMsg{}}); cmd == nil {
		t.Fatal("a refresh should start a fetch")
	}
	if !m.sources["a"].inflight {
		t.Error("the fetch should be marked in flight")
	}
	if cmd := m.handlePoll(pollMsg{source: "a", msg: poll.RefreshMsg{}}); cmd != nil {
		t.Error("a refresh during a fetch should be dropped")
	}
}

// Poller messages are stamped with their source, batches included, so
// one source's tick never reaches another's poller.
func TestPollMessagesCarryTheirSource(t *testing.T) {
	cmd := wrapPoll("a", tea.Batch(
		func() tea.Msg { return poll.RefreshMsg{} },
		func() tea.Msg { return poll.RefreshMsg{} },
	))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("got %T, want a batch of two", cmd())
	}
	for _, c := range batch {
		pm, ok := c().(pollMsg)
		if !ok || pm.source != "a" {
			t.Errorf("message %#v isn't stamped with its source", c())
		}
	}
	if wrapPoll("a", nil) != nil {
		t.Error("a nil cmd should stay nil")
	}
}

// A covered screen skips its timed fetches and marks them stale; shown
// again, it refetches what it skipped. poll_hidden keeps a source going.
func TestPollPausesWhileCovered(t *testing.T) {
	m := polledModel(t)
	m.sources["a"].inflight, m.sources["b"].inflight = false, false
	m.pollHidden = map[string]bool{"b": true}
	m.setCovered(true)
	if cmd := m.handlePoll(pollMsg{source: "a", msg: poll.RefreshMsg{}}); cmd != nil || !m.sources["a"].stale {
		t.Fatal("a covered screen should skip the fetch and mark it stale")
	}
	if cmd := m.handlePoll(pollMsg{source: "b", msg: poll.RefreshMsg{}}); cmd == nil {
		t.Error("poll_hidden: true should keep fetching while covered")
	}
	if cmd := m.setCovered(false); cmd == nil || m.sources["a"].stale || !m.sources["a"].inflight {
		t.Error("uncovering should refetch the skipped source")
	}
	if cmd := m.setCovered(false); cmd != nil {
		t.Error("uncovering twice refetched again")
	}
}
