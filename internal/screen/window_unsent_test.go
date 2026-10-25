package screen

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/query"
	"github.com/jsdrews/tuilib/pkg/remote"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	ds "github.com/jsdrews/tui-builder/internal/datasource"
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

// A comparing term travels as a Compare when its column has a param for
// it, and is dropped otherwise — never searched for as text — with a
// notice naming the comparison and the column.
func TestComparisonsTranslate(t *testing.T) {
	routes := &cfg.WindowConfig{
		Filters:      map[string]string{"Name": "name__icontains"},
		FilterParams: map[string]map[string]string{"Name": {"contains": "name__icontains"}, "Year": {"gte": "year__gte"}},
	}
	terms := query.Parse("name:Tolk year>=1950 year<2000 hobbit", []string{"Name", "Year"})
	q := translateWindowQuery(remote.Window{Filter: remote.Filter{Terms: terms}}, routes)
	if q.Filters["Name"] != "tolk" {
		t.Errorf("contains term: Filters = %v", q.Filters)
	}
	if len(q.Compares) != 1 || q.Compares[0] != (ds.Comparison{Title: "Year", Op: "gte", Value: "1950"}) {
		t.Errorf("compares = %+v", q.Compares)
	}
	if q.Search != "hobbit" {
		t.Errorf("search = %q; an unsupported comparison must not become search text", q.Search)
	}
	if got := unsupportedComparisons(routes, terms); got != "`<` isn't supported on Year" {
		t.Errorf("notice = %q", got)
	}
}
