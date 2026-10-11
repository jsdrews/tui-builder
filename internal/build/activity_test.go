package build

import (
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestBusyFunc(t *testing.T) {
	item := func(phase string, gen, observed int) any {
		return map[string]any{"status": map[string]any{"phase": phase, "observedGeneration": observed}, "generation": gen}
	}
	byValue := busyFunc(&cfg.Activity{Status: cfg.Path{"status.phase"}, Busy: []string{"Pending", "Terminating"}})
	if status, busy := byValue(item("Pending", 1, 1)); status != "Pending" || !busy {
		t.Errorf("Pending = (%q, %v), want busy with its status as the label", status, busy)
	}
	if status, busy := byValue(item("Running", 1, 1)); status != "Running" || busy {
		t.Errorf("Running = (%q, %v), want settled, still reporting its status", status, busy)
	}

	byExpr := busyFunc(&cfg.Activity{Status: cfg.Path{"status.phase"}, BusyWhen: "status.observedGeneration < generation"})
	if _, busy := byExpr(item("Running", 2, 1)); !busy {
		t.Error("a rollout still behind its generation should be busy")
	}
	if _, busy := byExpr(item("Running", 2, 2)); busy {
		t.Error("a caught-up rollout should be settled")
	}
	if busyFunc(nil) != nil {
		t.Error("no activity block, no predicate")
	}
}
