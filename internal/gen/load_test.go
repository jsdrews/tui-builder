package gen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
)

const remoteSpec = `openapi: 3.0.3
info: {title: Remote, version: "1"}
servers: [{url: /api/v3}]
paths:
  /me:
    get:
      responses:
        "200":
          description: OK
          content: {application/json: {schema: {$ref: "schemas.yaml#/User"}}}
components:
  securitySchemes:
    Oidc: {type: openIdConnect, openIdConnectUrl: "%s/.well-known/openid-configuration"}
security: [{Oidc: [read]}]
`

// A spec fetched from a URL: --header is sent, a $ref to another URL
// resolves, a relative server resolves against the spec's URL, and
// OpenID Connect discovery finds the token URL.
func TestLoadFromURL(t *testing.T) {
	var sawHeader bool
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spec/openapi.yaml":
			sawHeader = r.Header.Get("Authorization") == "Bearer spec-token"
			w.Write([]byte(strings.Replace(remoteSpec, "%s", srv.URL, 1)))
		case "/spec/schemas.yaml":
			w.Write([]byte("User: {type: object, properties: {login: {type: string}}}\n"))
		case "/.well-known/openid-configuration":
			w.Write([]byte(`{"token_endpoint": "https://idp.example.com/token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	res, err := Generate(context.Background(), Options{Input: Input{
		Spec:    srv.URL + "/spec/openapi.yaml",
		Headers: http.Header{"Authorization": {"Bearer spec-token"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !sawHeader {
		t.Error("--header wasn't sent")
	}
	out := string(res.Config)
	for _, want := range []string{
		"default: " + srv.URL + "/api/v3",
		"token_url: https://idp.example.com/token",
		"scopes: [read]",
		"From the OpenID Connect discovery document",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "spec-token") {
		t.Error("a --header value reached the config")
	}
}

func TestLoadTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	_, err := Generate(context.Background(), Options{Input: Input{Spec: srv.URL + "/x.yaml", Timeout: 100 * time.Millisecond}})
	if err == nil || !strings.Contains(err.Error(), "Timeout") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("a slow URL: %v", err)
	}
}

func TestLoadFromStdin(t *testing.T) {
	spec := `{"openapi": "3.0.3", "info": {"title": "Piped", "version": "1"}, "servers": [{"url": "https://p.example.com"}],
	  "paths": {"/v": {"get": {"responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"type": "object", "properties": {"v": {"type": "string"}}}}}}}}}}}`
	res, err := Generate(context.Background(), Options{Input: Input{Spec: "-", Stdin: strings.NewReader(spec)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Config), "from stdin (Piped 1)") {
		t.Errorf("header:\n%s", res.Config)
	}
}

func TestLoadRefusals(t *testing.T) {
	for _, tc := range []struct {
		spec, want string
	}{
		{`swagger: "2.0"` + "\ninfo: {title: x, version: '1'}\npaths: {/a: {get: {responses: {'200': {description: ok}}}}}\n", "3.0 and 3.1"},
		{"openapi: 3.0.3\ninfo: {title: x, version: '1'}\npaths: {}\n", "no paths"},
		{"not: [valid", "load stdin"},
	} {
		_, err := Generate(context.Background(), Options{Input: Input{Spec: "-", Stdin: strings.NewReader(tc.spec)}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.spec[:12], err, tc.want)
		}
	}
	if _, err := Generate(context.Background(), Options{Input: Input{Spec: "testdata/none.yaml"}}); err == nil {
		t.Error("a missing file loaded")
	}
}

func TestSelect(t *testing.T) {
	ops := []op{
		{method: "GET", path: "/a", op: opWith("list_a", "alpha")},
		{method: "POST", path: "/a", op: opWith("create_a", "alpha")},
		{method: "GET", path: "/b", op: opWith("list_b", "beta")},
		{method: "GET", path: "/c", op: opWith("get_c")},
	}
	for _, tc := range []struct {
		sel  Select
		want string
	}{
		{Select{}, "GET /a POST /a GET /b GET /c"},
		{Select{Tags: []string{"alpha"}}, "GET /a POST /a"},
		{Select{Tags: []string{"al*"}, ExcludeOperations: []string{"create_*"}}, "GET /a"},
		{Select{Operations: []string{"list_*"}}, "GET /a GET /b"},
		{Select{ExcludeTags: []string{"beta"}}, "GET /a POST /a GET /c"},
		{Select{ReadOnly: true}, "GET /a GET /b GET /c"},
	} {
		var got []string
		for _, o := range ops {
			if tc.sel.selected(o) {
				got = append(got, o.String())
			}
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("%+v: %s, want %s", tc.sel, strings.Join(got, " "), tc.want)
		}
	}
}

func opWith(id string, tags ...string) *openapi3.Operation {
	return &openapi3.Operation{OperationID: id, Tags: tags}
}
