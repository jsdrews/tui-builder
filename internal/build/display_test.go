package build

import (
	"strings"
	"testing"

	"github.com/jsdrews/tuilib/pkg/inspector"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Bold wraps the cell outside its color rule, so the rule's reset (the
// foreground only) leaves bold and the row background alone.
func TestBoldColumn(t *testing.T) {
	th := theme.Nord()
	col := cfg.Column{Value: cfg.Path{"s"}, Bold: true, ColorRules: []cfg.ColorRule{{When: "up", Color: "green"}}}
	got := columnCell(col, map[string]any{"s": "up"}, th)
	if !strings.HasPrefix(got, "\x1b[1m\x1b[") || !strings.HasSuffix(got, "\x1b[39m\x1b[22m") {
		t.Errorf("cell = %q", got)
	}
	col.Bold = false
	if strings.Contains(columnCell(col, map[string]any{"s": "up"}, th), "\x1b[1m") {
		t.Error("bold without bold: true")
	}
}

// An auto inspector's rules color leaves by path, "*" matching any one
// key or index; a rule with no path applies everywhere.
func TestColorAutoFields(t *testing.T) {
	th := theme.Nord()
	data := map[string]any{"status": "failed", "note": "failed", "items": []any{map[string]any{"state": "failed"}}}
	fields := inspector.FromAny(data)
	colorAutoFields(fields, nil, []cfg.ColorRule{
		{Path: "status", When: "failed", Color: "red"},
		{Path: "items.*.state", When: "failed", Color: "red"},
	}, th)
	byLabel := map[string]inspector.Field{}
	for _, f := range fields {
		byLabel[f.Label] = f
	}
	if !strings.Contains(byLabel["status"].Value, "\x1b[") || strings.Contains(byLabel["note"].Value, "\x1b[") {
		t.Errorf("status %q, note %q", byLabel["status"].Value, byLabel["note"].Value)
	}
	if v := byLabel["items"].Children[0].Children[0].Value; !strings.Contains(v, "\x1b[") {
		t.Errorf("items.0.state = %q", v)
	}
	fields = inspector.FromAny(data)
	colorAutoFields(fields, nil, []cfg.ColorRule{{When: "failed", Color: "red"}}, th)
	for _, f := range fields {
		if f.Label == "note" && !strings.Contains(f.Value, "\x1b[") {
			t.Error("a rule with no path didn't apply to every leaf")
		}
	}
}
