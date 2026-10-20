package action

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// An http action sends its auth, query and the default User-Agent, and
// masks its secrets in what it reports.
func TestDoAuthQueryAndRedaction(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(403)
		io.WriteString(w, `{"error":"token s3cr3t-tok rejected"}`)
	}))
	defer srv.Close()
	a := &cfg.Action{Type: "http", URL: srv.URL + "/x?keep=1", Auth: &cfg.AuthSpec{Bearer: "s3cr3t-tok"},
		Query:  map[string]cfg.QueryValue{"q": {"${inputs.q}"}, "empty": {"${inputs.none}"}},
		Inputs: map[string]*cfg.Parameter{"q": {}, "none": {}}, ErrorMessage: "${output}"}
	r, err := Resolve(a, Inputs{"q": "a b"})
	if err != nil {
		t.Fatal(err)
	}
	res := r.Do(t.Context())
	if got.Header.Get("Authorization") != "Bearer s3cr3t-tok" || got.URL.Query().Get("q") != "a b" ||
		got.URL.Query().Get("keep") != "1" || got.URL.Query().Has("empty") || !strings.HasPrefix(got.UserAgent(), "tui-builder/") {
		t.Errorf("request %v %v %q", got.Header, got.URL, got.UserAgent())
	}
	if strings.Contains(res.Summary+res.Output, "s3cr3t-tok") {
		t.Errorf("secret leaked: %q / %q", res.Summary, res.Output)
	}
}
