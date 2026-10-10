package config

import (
	"strings"
	"testing"
)

func windowedTableConfig(debounce string, windowed bool) *Config {
	src := &Source{Type: "http", URL: "http://example/books"}
	if windowed {
		src.Window = &WindowConfig{OffsetParam: "offset", LimitParam: "limit", SearchParam: "q"}
	}
	return &Config{
		Data: DataBlock{Sources: map[string]*Source{"books": src}},
		TUI: TUIBlock{
			Components: map[string]*Component{"books": {
				Type: "table", Source: "books", SortDebounce: debounce,
				Columns: []Column{{Title: "Title", Value: Path{"title"}}},
			}},
			Screen: Screen{Layout: Node{Component: "books"}},
		},
	}
}

func TestSortDebounceValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		debounce string
		windowed bool
		wantErr  string
	}{
		{"windowed table", "300ms", true, ""},
		{"zero sorts on every change", "0", true, ""},
		{"local table has nothing to debounce", "300ms", false, "only applies to a table bound to a windowed source"},
		{"not a duration", "soon", true, "invalid sort_debounce"},
		{"negative", "-1s", true, "invalid sort_debounce"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := windowedTableConfig(tc.debounce, tc.windowed).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}
