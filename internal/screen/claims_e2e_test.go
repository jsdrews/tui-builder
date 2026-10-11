package screen

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	taction "github.com/jsdrews/tuilib/pkg/action"
	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/runner"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func newClaimHarness(t *testing.T, srv *podsStub, claim *cfg.Claim, multi bool) *windowHarness {
	t.Helper()
	c := cfg.Config{
		Data: cfg.DataBlock{Sources: map[string]*cfg.Source{
			"pods": cfg.NewEntry(&cfg.Source{Type: "http", URL: srv.URL, Refresh: "1h"}),
		}},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{"pods": {
				Type: "table", Title: "Pods", Source: "pods", MarkKey: cfg.Path{"uid"}, Markable: true,
				Columns: []cfg.Column{
					{Title: "Name", Width: 10, Value: cfg.Path{"name"}},
					{Title: "Phase", Width: 16, Value: cfg.Path{"status.phase"}},
				},
				Activity: &cfg.Activity{Status: cfg.Path{"status.phase"}, Busy: []string{"Pending"}},
			}},
			Screen: cfg.Screen{
				Layout: cfg.Node{Component: "pods"},
				Actions: []cfg.ActionBinding{{
					Key: "enter", Action: "restart", From: "pods",
					Bind: map[string]string{"name": "${selection.Name}"},
				}},
			},
		},
		Actions: map[string]*cfg.Action{"restart": {
			Run: []string{"true"}, Inputs: map[string]*cfg.Parameter{"name": {Required: true}},
			Claim: claim, Multi: multi,
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	h := &windowHarness{t: t, model: m, root: root}
	h.drain(h.model.Init())
	return h
}

// pick presses enter without running the launch it returns, and hands
// back the run tags the pick registered: the test then plays the run's
// start and end by hand.
func (h *windowHarness) pick() []string {
	h.t.Helper()
	h.model, _ = h.model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	var tags []string
	for tag := range h.root.claims {
		tags = append(tags, tag)
	}
	if len(tags) == 0 {
		h.t.Fatal("the pick registered no claim")
	}
	return tags
}

func TestHeldClaimSpinsUntilTheRunReturns(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Restarting"}, false)
	tbl := h.root.tree.Components["pods"].Table
	base := tbl.ActivityCount() // api is Pending

	tag := h.pick()[0]
	if got := tbl.ActivityCount(); got != base {
		t.Fatalf("the claim opened before the run started (%d busy)", got)
	}
	h.send(runner.CaptureStarted{Tag: tag})
	if got := tbl.ActivityCount(); got != base+1 {
		t.Fatalf("%d rows busy after the run started, want %d", got, base+1)
	}
	if got := h.view(); !strings.Contains(got, "Restarting") {
		t.Errorf("the claimed row should show the claim's label; view:\n%s", got)
	}
	h.send(runner.Captured{Tag: tag})
	if got := tbl.ActivityCount(); got != base {
		t.Errorf("%d rows busy after the run returned, want %d — an until: done claim ends with the run", got, base)
	}
	if len(h.root.claims) != 0 {
		t.Error("the finished run's claim is still registered")
	}
}

func TestFailedRunWithdrawsTheClaim(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Running", Until: "status"}, false)
	tbl := h.root.tree.Components["pods"].Table
	base := tbl.ActivityCount()

	tag := h.pick()[0]
	h.send(runner.CaptureStarted{Tag: tag})
	h.send(runner.Captured{Tag: tag, Err: errors.New("exit status 1")})
	if got := tbl.ActivityCount(); got != base {
		t.Errorf("%d rows busy after the run failed, want %d — a failure withdraws the claim", got, base)
	}
}

// until: status: the run only acknowledges; the polled status takes over,
// and once reads keep showing no change the claim ends and says so.
func TestObservedClaimHandsOverToThePoll(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Running", Until: "status"}, false)
	tbl := h.root.tree.Components["pods"].Table
	base := tbl.ActivityCount()

	tag := h.pick()[0]
	h.send(runner.CaptureStarted{Tag: tag})
	h.send(runner.Captured{Tag: tag})
	if got := tbl.ActivityCount(); got != base+1 {
		t.Fatalf("%d rows busy after the run acknowledged, want %d — an until: status claim waits for the data", got, base+1)
	}

	// settle: 1 (the default) allows one read that repeats the status
	// from before the action, for a server that reports new work late.
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := tbl.ActivityCount(); got != base+1 {
		t.Fatalf("%d rows busy after one unchanged read, want %d — settle 1 allows one", got, base+1)
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if got := tbl.ActivityCount(); got != base {
		t.Errorf("%d rows busy after a second read showed no change, want %d", got, base)
	}
	if got := h.view(); !strings.Contains(got, "restart web: no change") {
		t.Errorf("an unobserved claim should be reported; view:\n%s", got)
	}
}

// Fan-out makes one claim per marked row, so one row's failure stops only
// that row.
func TestFanOutClaimsEachRow(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	srv.set(func(s *podsStub) { s.phases["u2"] = "Running" })
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Restarting"}, true)
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	tbl := h.root.tree.Components["pods"].Table
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}) // mark all

	tags := h.pick()
	if len(tags) != 2 {
		t.Fatalf("registered %d claims for two marked rows, want one per row", len(tags))
	}
	for _, tag := range tags {
		h.send(runner.CaptureStarted{Tag: tag})
	}
	if got := tbl.ActivityCount(); got != 2 {
		t.Fatalf("%d rows busy, want both", got)
	}
	h.send(runner.Captured{Tag: tags[0], Err: errors.New("refused")})
	if got := tbl.ActivityCount(); got != 1 {
		t.Errorf("%d rows busy after one row's run failed, want 1", got)
	}
}

// A pick from the menu is launched by the shell, tagged RunKey(action,
// the menu's target); the claim has to be registered under that tag, and
// the receipt says what the claim means.
func TestMenuPickRegistersItsClaimUnderTheShellsTag(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Running", Until: "status"}, false)

	set := h.root.Actions()
	if len(set.Actions) != 1 || set.Actions[0].Run == nil {
		t.Fatalf("want one Run action, got %+v", set.Actions)
	}
	a := set.Actions[0]
	if _, ok := h.root.claims[taction.RunKey(a, set.Target)]; !ok {
		t.Errorf("no claim registered under the shell's tag for target %q; have %v", set.Target, h.root.claims)
	}
	if a.Receipt != "Running requested" {
		t.Errorf("receipt = %q, want %q", a.Receipt, "Running requested")
	}
}

// End to end through the runner: the real CaptureStarted / Captured a
// GoWith run emits open and close the claim, and nothing is left behind.
func TestClaimRidesARealRun(t *testing.T) {
	srv := newPodsStub()
	defer srv.Close()
	h := newClaimHarness(t, srv, &cfg.Claim{Label: "Restarting"}, false)
	tbl := h.root.tree.Components["pods"].Table
	base := tbl.ActivityCount()

	h.send(tea.KeyMsg{Type: tea.KeyEnter})
	if len(h.root.claims) != 0 || len(h.root.claimOps) != 0 {
		t.Errorf("claims left registered after the run finished: %v / %v", h.root.claims, h.root.claimOps)
	}
	if got := tbl.ActivityCount(); got != base {
		t.Errorf("%d rows busy after the run finished, want %d", got, base)
	}
	// The statusbar truncates at this width; "Restarting complet…".
	if got := h.view(); !strings.Contains(got, "Restarting compl") {
		t.Errorf("the claim's default success message didn't reach the statusbar; view:\n%s", got)
	}
}
