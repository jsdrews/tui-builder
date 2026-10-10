package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestEventlogItems(t *testing.T) {
	c := &Component{Cfg: &cfg.Component{
		Type: "eventlog", Key: cfg.Path{"id"}, Text: cfg.Path{"stdout"}, Mark: cfg.Path{"created"},
	}}
	items := EventlogItems(c, []any{
		map[string]any{"id": 7, "stdout": "TASK [install] ***\nok: [web1]\n", "created": "10:00:01"},
		map[string]any{"id": 8, "stdout": "", "event": "playbook_on_start"},
	}, theme.Nord())

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Key != "7" || items[0].Mark != "10:00:01" {
		t.Errorf("first item key/mark = %q/%q", items[0].Key, items[0].Mark)
	}
	if len(items[0].Lines) != 2 || items[0].Lines[1] != "ok: [web1]" {
		t.Errorf("first item lines = %q, want the two lines without a trailing blank", items[0].Lines)
	}
	if len(items[1].Lines) != 0 {
		t.Errorf("an empty text should draw as no lines (tuilib's \"no output\"), got %q", items[1].Lines)
	}
	if items[1].Data == nil {
		t.Error("the raw item should ride along as Data for enter and the inspector")
	}
}

func TestItemSelectionFlattensFields(t *testing.T) {
	sel := ItemSelection("4012", map[string]any{
		"event":      "runner_on_ok",
		"counter":    37,
		"event_data": map[string]any{"host": "web1", "res": []any{1, 2}},
	})
	if got := Substitute("${selection}", sel); got != "4012" {
		t.Errorf("${selection} = %q, want the item key", got)
	}
	for tmpl, want := range map[string]string{
		"${selection.event}":           "runner_on_ok",
		"${selection.counter}":         "37",
		"${selection.event_data.host}": "web1",
	} {
		if got := Substitute(tmpl, sel); got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
	}
}
