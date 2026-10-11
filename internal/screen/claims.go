package screen

// Claims: making the rows an action targets spin from the moment it
// starts, instead of waiting for the next poll to notice.
//
// A claim belongs to one run, and every run that can carry one has a run
// tag: the shell tags the runs it launches with RunKey(action, target),
// fan-out tags each row's run the same way, and an action started from a
// key or a form goes out through runner.GoWith under a tag of our own. The
// tag is registered with the rows and the claim before the run starts;
// the claim opens on runner.CaptureStarted, which comes after any confirm,
// and closes with Done on runner.Captured. Both reach every stacked
// screen, and only the screen that registered the tag acts on it.

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/activity"
	"github.com/jsdrews/tuilib/pkg/app"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// claimRun is one run's claim: which rows of which component, and the
// operation once it's open.
type claimRun struct {
	component string
	keys      []string
	claim     *cfg.Claim
	action    string
	op        activity.Op
	open      bool
	// receipt is the statusbar line to post when a run this screen
	// launched itself succeeds. The shell posts its own receipt for runs
	// it launched (Action.Receipt), but not for ours.
	receipt string
}

var claimSeq atomic.Uint64

// newClaimTag makes a run tag for a run this screen launches itself.
func newClaimTag(action string) string {
	return fmt.Sprintf("claim\x00%s\x00%d", action, claimSeq.Add(1))
}

// registerClaim records that the run tagged tag claims keys of component.
// Registering a tag again (the menu is rebuilt every time it opens)
// replaces the entry; only a run that actually starts opens a claim.
func (m *Model) registerClaim(tag, component string, keys []string, def *cfg.Action, action, receipt string) {
	if def.Claim == nil || len(keys) == 0 {
		return
	}
	if m.claims == nil {
		m.claims = map[string]*claimRun{}
	}
	if old, ok := m.claims[tag]; ok && old.open {
		return
	}
	m.claims[tag] = &claimRun{component: component, keys: keys, claim: def.Claim, action: action, receipt: receipt}
}

// openClaim starts the claim registered for a run that just started.
func (m *Model) openClaim(tag string) tea.Cmd {
	cr, ok := m.claims[tag]
	if !ok || cr.open {
		return nil
	}
	c := m.tree.Components[cr.component]
	if c == nil {
		delete(m.claims, tag)
		return nil
	}
	mode := activity.Held
	if cr.claim.IsObserved() {
		mode = activity.Observed
	}
	op, cmd := build.Dispatch(c, cr.keys, cr.claim.Label, mode)
	cr.op, cr.open = op, true
	if m.claimOps == nil {
		m.claimOps = map[uint64]*claimRun{}
	}
	m.claimOps[op.ID()] = cr
	return tagCursorFocused(cmd, cr.component)
}

// closeClaim tells the component a claimed run answered, and posts the
// claim's receipt for a run this screen launched. handled is false for a
// tag with no open claim, which the caller reports as usual.
// A failed run withdraws its rows' claim; the failure itself is reported
// by the run's own summary.
func (m *Model) closeClaim(tag string, err error) (tea.Cmd, bool) {
	cr, ok := m.claims[tag]
	if !ok {
		return nil, false
	}
	delete(m.claims, tag)
	if !cr.open {
		return nil, false
	}
	if c := m.tree.Components[cr.component]; c != nil {
		build.Done(c, cr.op, err)
	}
	if !cr.claim.IsObserved() || err != nil {
		delete(m.claimOps, cr.op.ID())
	}
	if err == nil && cr.receipt != "" {
		return app.Info(cr.receipt), true
	}
	return nil, true
}

// unobserved reports an `until: status` claim that ended without any
// read seeing its rows busy: the work finished between polls, was a
// no-op, or never started. Without this the row just goes back to what
// it said, and the action looks ignored.
func (m *Model) unobserved(msg activity.UnobservedMsg) tea.Cmd {
	cr, ok := m.claimOps[msg.Op]
	if !ok {
		return nil
	}
	delete(m.claimOps, msg.Op)
	what := "no change observed"
	if msg.Changed {
		what = "finished between polls"
	}
	// Name the rows by what they show where we can (a table's first
	// cell), not by the mark_key the claim holds them by.
	names := make([]string, len(msg.Keys))
	c := m.tree.Components[cr.component]
	for i, k := range msg.Keys {
		names[i] = k
		if c != nil {
			if sel, ok := c.RowsByKey[k]; ok && sel.String != "" {
				names[i] = sel.String
			}
		}
	}
	return app.Info(fmt.Sprintf("%s %s: %s", cr.action, strings.Join(names, ", "), what))
}

// claimKeys lists the row keys of a selection, for a claim.
func claimKeys(sels []build.Selection) []string {
	var keys []string
	for _, s := range sels {
		if s.Key != "" {
			keys = append(keys, s.Key)
		}
	}
	return keys
}
