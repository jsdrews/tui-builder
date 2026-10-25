package build

import (
	"testing"
	"time"

	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestTableOptionsCarryWorkstream4Fields(t *testing.T) {
	th := theme.New(palette.Nord())
	opts := tableOptions(&cfg.Component{
		Type: "table", LoadingLabel: "loading pods…",
		Activity: &cfg.Activity{Status: cfg.Path{"phase"}, Busy: []string{"Pending"}, Revision: cfg.Path{"meta.rev"}},
		Colors:   &cfg.Colors{Mark: "red"},
		Columns:  []cfg.Column{{Title: "Phase", Value: cfg.Path{"phase"}}},
	}, th)
	if opts.LoadingLabel != "loading pods…" {
		t.Errorf("LoadingLabel = %q", opts.LoadingLabel)
	}
	if opts.Revision == nil {
		t.Fatal("activity.revision should set Revision")
	}
	row := table.KeyedRow{Data: map[string]any{"meta": map[string]any{"rev": "42"}}}
	if got := opts.Revision(row); got != "42" {
		t.Errorf("Revision(row) = %q, want 42", got)
	}
	if opts.MarkStyle.GetForeground() == th.Table().MarkStyle.GetForeground() {
		t.Error("colors.mark should change MarkStyle")
	}

	plain := tableOptions(&cfg.Component{Type: "table"}, th)
	if plain.Revision != nil {
		t.Error("no activity.revision should leave Revision nil")
	}
}

func TestEventlogOptionsCarryWorkstream4Fields(t *testing.T) {
	th := theme.New(palette.Nord())
	no := false
	opts := eventlogOptions(&cfg.Component{Type: "eventlog", Gutter: &no, NewFor: "5s",
		Colors: &cfg.Colors{New: "green", Gutter: "blue", Cell: "white", SelectedBG: "red"}}, th)
	if !opts.NoGutter {
		t.Error("gutter: false should set NoGutter")
	}
	if opts.NewFor != 5*time.Second {
		t.Errorf("NewFor = %v, want 5s", opts.NewFor)
	}
	base := th.Eventlog()
	for name, changed := range map[string]bool{
		"new":         opts.NewStyle.GetForeground() != base.NewStyle.GetForeground(),
		"gutter":      opts.GutterStyle.GetForeground() != base.GutterStyle.GetForeground(),
		"cell":        opts.CellStyle.GetForeground() != base.CellStyle.GetForeground(),
		"selected_bg": opts.SelectedStyle.GetBackground() != base.SelectedStyle.GetBackground(),
	} {
		if !changed {
			t.Errorf("colors.%s should change its eventlog style", name)
		}
	}
	if never := eventlogOptions(&cfg.Component{Type: "eventlog", NewFor: "-"}, th); never.NewFor >= 0 {
		t.Errorf(`new_for: "-" should be negative (never), got %v`, never.NewFor)
	}
}

func TestDistinctValuesFromASource(t *testing.T) {
	got := distinctValues([]any{"Running", "", 3.0, true})
	want := []string{"Running", "3", "true"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}
