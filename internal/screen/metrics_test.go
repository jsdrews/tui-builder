package screen

import (
	"path/filepath"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// The metrics example's Ready column draws "3/3", but a binding reading
// ${selection.Ready} gets the ready count.
func TestMetricsExampleSelectsRawValues(t *testing.T) {
	c, err := cfg.Load(filepath.Join("..", "..", "examples", "metrics.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, c)
	runToQuiescence(t, m, drainCmd(m.OnEnter(nil))...)

	sel := selectionFrom(m.tree.Components["deployments"])
	if got := sel.Cells[1]; got != "3" {
		t.Errorf("${selection.Ready} = %q, want 3", got)
	}
	if got := sel.Cells[0]; got != "web" {
		t.Errorf("${selection.Name} = %q, want web", got)
	}
}
