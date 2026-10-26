package datasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// replyWith serves one fixed response.
func replyWith(t *testing.T, status int, header map[string]string, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, d *cfg.Source) (any, error) {
	t.Helper()
	s, err := newHTTP(d)
	if err != nil {
		t.Fatal(err)
	}
	return s.Fetch(context.Background())
}

func TestHTTPEmptyResults(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		src    cfg.Source
		want   any
	}{
		{"empty_on 404", 404, `{"message":"Not Found"}`, cfg.Source{EmptyOn: []int{404}}, nil},
		{"204", 204, "", cfg.Source{}, nil},
		{"blank 200", 200, "  \n", cfg.Source{}, nil},
		{"blank text", 200, "", cfg.Source{Format: "text"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := replyWith(t, c.status, nil, c.body)
			d := c.src
			d.Type, d.URL = "http", srv.URL
			got, err := fetch(t, &d)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %#v, %v; want %#v, nil", got, err, c.want)
			}
		})
	}
}

func TestHTTPErrorMessages(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		header    map[string]string
		body      string
		errorPath string
		want      string
	}{
		{"message", 404, nil, `{"message":"Not Found","documentation_url":"x"}`, "", "HTTP 404 Not Found — Not Found"},
		{"error.message", 400, nil, `{"error":{"message":"No such customer","type":"invalid"}}`, "", "HTTP 400 Bad Request — No such customer"},
		{"error string", 401, nil, `{"error":"invalid_token"}`, "", "HTTP 401 Unauthorized — invalid_token"},
		{"detail", 403, nil, `{"detail":"Authentication credentials were not provided."}`, "", "HTTP 403 Forbidden — Authentication credentials were not provided."},
		{"graphql errors", 400, nil, `{"errors":[{"message":"bad query"}]}`, "", "HTTP 400 Bad Request — bad query"},
		{"not json", 502, nil, "<html>bad gateway</html>", "", "HTTP 502 Bad Gateway — <html>bad gateway</html>"},
		{"error_path", 422, nil, `{"message":"generic","errs":{"why":"specific"}}`, "errs.why", "HTTP 422 Unprocessable Entity — specific"},
		{"error_path header", 500, map[string]string{"X-Error": "from a header"}, `{}`, "header:x-error", "HTTP 500 Internal Server Error — from a header"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := replyWith(t, c.status, c.header, c.body)
			_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL + "/x", ErrorPath: c.errorPath})
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			var he *HTTPError
			if !errors.As(err, &he) || !strings.Contains(he.Detail(), "GET "+srv.URL+"/x") {
				t.Errorf("detail = %v", err)
			}
		})
	}
}

// On a 2xx only an explicit error_path makes an error, and whatever root:
// finds alongside it is kept.
func TestHTTPErrorOn2xx(t *testing.T) {
	body := `{"data":{"items":[{"id":1}]},"errors":[{"message":"field x is deprecated"}],"message":"ok"}`
	srv := replyWith(t, 200, nil, body)

	got, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Root: "data.items"})
	if err != nil {
		t.Fatalf("a 2xx with a message field isn't an error without error_path: %v", err)
	}

	got, err = fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Root: "data.items", ErrorPath: "errors.0.message"})
	if err == nil || err.Error() != "HTTP 200 OK — field x is deprecated" {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(got, []any{map[string]any{"id": float64(1)}}) {
		t.Errorf("partial data = %#v, want the items kept", got)
	}
}

func TestHTTPTotalFromHeader(t *testing.T) {
	srv := replyWith(t, 200, map[string]string{"X-Total-Count": "1234"}, `[{"id":1}]`)
	s, _ := newHTTP(&cfg.Source{Type: "http", URL: srv.URL, Window: &cfg.WindowConfig{
		OffsetParam: "_start", LimitParam: "_limit", SearchParam: "q", TotalPath: "header:x-total-count",
	}})
	page, err := s.(WindowedSource).FetchWindow(context.Background(), WindowQuery{Limit: 10})
	if err != nil || page.Total != 1234 {
		t.Errorf("total = %d, %v", page.Total, err)
	}
}

// strategy: link with next_path: link:next follows the Link header,
// resolving relative URLs, and Sentry's results="false" ends the walk.
func TestHTTPPaginateLinkHeader(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Query().Get("cursor") {
		case "":
			w.Header().Set("Link", `<?cursor=2>; rel="next"; results="true", <https://x.test/prev>; rel="previous"`)
			io.WriteString(w, `[{"n":1}]`)
		case "2":
			w.Header().Set("Link", `<?cursor=3>; rel="next"; results="false"`)
			io.WriteString(w, `[{"n":2}]`)
		default:
			t.Errorf("followed a results=\"false\" link")
		}
	}))
	defer srv.Close()
	got, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL + "/issues", Paginate: &cfg.PaginateConfig{Strategy: "link", NextPath: "link:next"}})
	if err != nil || len(got.([]any)) != 2 || hits != 2 {
		t.Errorf("got %v, %v after %d requests", got, err, hits)
	}
}

// A relative next URL in the body resolves against the page that sent it.
func TestHTTPPaginateRelativeBodyNext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/items" {
			io.WriteString(w, `{"next":"/v1/items/page2","results":[1]}`)
			return
		}
		fmt.Fprintf(w, `{"next":null,"results":[2]}`)
	}))
	defer srv.Close()
	got, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL + "/v1/items", Root: "results", Paginate: &cfg.PaginateConfig{Strategy: "link", NextPath: "next"}})
	if err != nil || !reflect.DeepEqual(got, []any{float64(1), float64(2)}) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestLocateLast(t *testing.T) {
	s := &httpSource{}
	r := &response{header: http.Header{}}
	items := []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}
	if got := s.locate("last:id", nil, r, items); got != "b" {
		t.Errorf("last:id = %v", got)
	}
	if got := s.locate("last:id", nil, r, nil); got != nil {
		t.Errorf("last:id on an empty page = %v, want nil", got)
	}
}

func TestLinkRel(t *testing.T) {
	h := []string{`<https://a.test/?page=2>; rel="next last", <https://a.test/?a=1,2>; rel=prev`}
	if got := linkRel(h, "next"); got != "https://a.test/?page=2" {
		t.Errorf("next = %q", got)
	}
	if got := linkRel(h, "prev"); got != "https://a.test/?a=1,2" {
		t.Errorf("prev = %q (a comma inside <> isn't a separator)", got)
	}
	if got := linkRel(h, "first"); got != "" {
		t.Errorf("first = %q", got)
	}
}

// Only "understood, won't serve" counts as a refusal; anything that
// might work if asked again is a failure.
func TestHTTPErrorRefusal(t *testing.T) {
	for status, want := range map[int]bool{400: true, 422: true, 401: false, 403: false, 404: false, 429: false, 500: false, 503: false} {
		if got := (&HTTPError{Status: status}).Refusal(); got != want {
			t.Errorf("status %d: Refusal() = %v, want %v", status, got, want)
		}
	}
}
