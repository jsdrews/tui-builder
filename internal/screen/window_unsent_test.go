package screen

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/query"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// A bare term on a window with only column filters has nowhere to go;
// the screen says so instead of dropping it silently.
func TestUnsentTermsNamesTheColumnSyntax(t *testing.T) {
	m := &Model{
		windows:        map[string]*windowEntry{"t": {source: "s"}},
		windowDefs:     map[string]*cfg.WindowConfig{"s": {Filters: map[string]string{"Status": "status", "Author": "author"}}},
		windowSearches: map[string]bool{"s": false},
	}
	terms := []query.Term{{Title: "Author", Raw: "author:ann"}, {Raw: "foo"}}
	want := `"foo" not sent — this table filters only by column: author:…, status:…`
	if got := m.unsentTerms("t", terms); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := m.unsentTerms("t", terms[:1]); got != "" {
		t.Errorf("scoped only: got %q", got)
	}
	m.windowSearches["s"] = true
	if got := m.unsentTerms("t", terms); got != "" {
		t.Errorf("with search: got %q", got)
	}
}
