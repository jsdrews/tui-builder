package action

import (
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// json: is templated before encoding, types exact input tokens, and
// drops a key whose token resolves empty; a literal null stays.
func TestResolveJSONBody(t *testing.T) {
	a := &cfg.Action{Type: "http", URL: "https://h.test/items", Inputs: map[string]*cfg.Parameter{
		"name": {}, "count": {Type: "int"}, "tag": {},
	}, JSON: map[string]any{
		"name": "${inputs.name}", "count": "${inputs.count}", "tag": "${inputs.tag}",
		"label": `say "${inputs.name}"`, "note": nil,
	}}
	r, err := Resolve(a, Inputs{"name": `a"b`, "count": "3"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":3,"label":"say \"a\"b\"","name":"a\"b","note":null}`
	if r.Body != want || r.ContentType != "application/json" {
		t.Errorf("body %s (%s), want %s", r.Body, r.ContentType, want)
	}
}

func TestResolveFormBody(t *testing.T) {
	a := &cfg.Action{Type: "http", URL: "https://h.test/x", Form: map[string]cfg.QueryValue{
		"a": {"${inputs.a}"}, "b": {"${inputs.b}"}, "tag": {"x", "y"},
	}, Inputs: map[string]*cfg.Parameter{"a": {}, "b": {}}}
	r, _ := Resolve(a, Inputs{"a": "1 2"})
	if r.Body != "a=1+2&tag=x&tag=y" || r.ContentType != "application/x-www-form-urlencoded" {
		t.Errorf("body %q (%s)", r.Body, r.ContentType)
	}
}

// The url's tokens are escaped by where they sit; a segment input keeps
// its "/" escaped.
func TestResolveEscapesURL(t *testing.T) {
	a := &cfg.Action{Type: "http", URL: "https://h.test/apps/${inputs.app}/items/${id}?q=${inputs.q}",
		Inputs: map[string]*cfg.Parameter{"app": {}, "id": {Segment: true}, "q": {}}}
	r, _ := Resolve(a, Inputs{"app": "team/my app", "id": "a/b", "q": "x&y"})
	if want := "https://h.test/apps/team/my%20app/items/a%2Fb?q=x%26y"; r.URL != want {
		t.Errorf("url %q, want %q", r.URL, want)
	}
}
