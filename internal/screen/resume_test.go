package screen

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// The resume example drops its stream every four seconds of log. It
// reconnects by itself, asks for the lines since the last one shown, and
// skips the second it re-sends: every line once, nothing missed.
func TestResumableStreamReconnectsExactly(t *testing.T) {
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "resume.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, c)
	lines := func() []string {
		var out []string
		for _, l := range m.tree.Components["log"].Logview.Lines() {
			out = append(out, strings.TrimSpace(l))
		}
		return out
	}
	done := func() bool {
		for _, l := range lines() {
			if strings.Contains(l, "request 7.b") {
				return true
			}
		}
		return false
	}
	inbox := append([]tea.Msg{tea.WindowSizeMsg{Width: 100, Height: 40}}, drainCmd(m.OnEnter(nil))...)
	deadline := time.Now().Add(20 * time.Second)
	for len(inbox) > 0 && !done() && time.Now().Before(deadline) {
		msg := inbox[0]
		inbox = inbox[1:]
		if isTimerMsg(msg) {
			continue
		}
		_, cmd := m.Update(msg)
		inbox = append(inbox, drainCmd(cmd)...)
	}
	got := lines()
	if !done() {
		t.Fatalf("the stream didn't get past its first drop; log:\n%s", strings.Join(got, "\n"))
	}
	seen := map[string]int{}
	reconnected := false
	for _, l := range got {
		if i := strings.Index(l, "request "); i >= 0 {
			seen[l[i:]]++
		}
		reconnected = reconnected || strings.Contains(l, "reconnected")
	}
	for s := 0; s <= 7; s++ {
		for _, part := range []string{"a", "b"} {
			key := "request " + string(rune('0'+s)) + "." + part + " served"
			if seen[key] != 1 {
				t.Errorf("%q shown %d times, want once", key, seen[key])
			}
		}
	}
	if !reconnected {
		t.Error("the reconnect should be marked in the log")
	}
}
