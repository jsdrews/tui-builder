package screen

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/poll"
)

// pollMsg carries one of a source's poller messages: its tick, or the
// RefreshMsg a tick turns into. tuilib's poll messages say nothing about
// whose they are — two pollers would accept each other's ticks — so each
// source's are wrapped with its name, and scoped to the screen like the
// screen's other messages.
type pollMsg struct {
	source string
	msg    tea.Msg
}

// startPolls arms a poller for every source that refreshes on a timer.
// Streams, windowed sources (their remote binding polls) and
// cursor-driven sources (their driver refetches them) aren't polled here.
func (m *Model) startPolls() []tea.Cmd {
	var cmds []tea.Cmd
	for name, entry := range m.sources {
		d := entry.src.Refresh()
		if d <= 0 || entry.poll != nil || entry.streaming || m.windowedSource(name) {
			continue
		}
		if _, cursor := m.cursorCaches[name]; cursor {
			continue
		}
		p := poll.New(poll.Options{Interval: d})
		entry.poll = &p
		cmds = append(cmds, wrapPoll(name, p.Init()))
	}
	return cmds
}

// handlePoll feeds a source's poller its tick, and refetches on the
// RefreshMsg that comes back — unless the last fetch is still out.
func (m *Model) handlePoll(x pollMsg) tea.Cmd {
	entry, ok := m.sources[x.source]
	if !ok || entry.poll == nil {
		return nil
	}
	if _, refresh := x.msg.(poll.RefreshMsg); refresh {
		if entry.inflight {
			return nil
		}
		return m.startFetch(x.source)
	}
	return wrapPoll(x.source, entry.poll.Update(x.msg))
}

// wrapPoll stamps whatever cmd produces with source, walking batches.
func wrapPoll(source string, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			out := make(tea.BatchMsg, 0, len(batch))
			for _, c := range batch {
				out = append(out, wrapPoll(source, c))
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return pollMsg{source: source, msg: msg}
	}
}
