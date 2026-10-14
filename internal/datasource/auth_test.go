package datasource

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// echoAuth answers with what arrived: the Authorization and X-Api-Key
// headers, the cookie, and the query string.
func echoAuth(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization": r.Header.Get("Authorization"),
			"api_key":       r.Header.Get("X-Api-Key"),
			"cookie":        r.Header.Get("Cookie"),
			"query":         r.URL.RawQuery,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func authFetch(t *testing.T, url string, a *cfg.AuthSpec) map[string]any {
	t.Helper()
	got, err := fetch(t, &cfg.Source{Type: "http", URL: url, Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	return got.(map[string]any)
}

func TestAuthSchemes(t *testing.T) {
	srv := echoAuth(t)
	cases := []struct {
		name  string
		auth  *cfg.AuthSpec
		field string
		want  string
	}{
		{"bearer", &cfg.AuthSpec{Bearer: "tok123"}, "authorization", "Bearer tok123"},
		{"basic", &cfg.AuthSpec{Basic: &cfg.BasicAuth{User: "sk_live", Pass: ""}}, "authorization", "Basic c2tfbGl2ZTo="},
		{"api_key header", &cfg.AuthSpec{APIKey: &cfg.APIKeyAuth{In: "header", Name: "X-Api-Key", Value: "k123"}}, "api_key", "k123"},
		{"api_key query", &cfg.AuthSpec{APIKey: &cfg.APIKeyAuth{In: "query", Name: "api_key", Value: "k 1"}}, "query", "a=1&api_key=k+1"},
		{"api_key cookie", &cfg.AuthSpec{APIKey: &cfg.APIKeyAuth{In: "cookie", Name: "session", Value: "s123"}}, "cookie", "session=s123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := authFetch(t, srv.URL+"/?a=1", c.auth)[c.field]; got != c.want {
				t.Errorf("%s = %q, want %q", c.field, got, c.want)
			}
		})
	}
}

// A query api_key rides on followed next URLs too, which paginate
// otherwise uses exactly as sent.
func TestAuthQueryKeyOnNextURLs(t *testing.T) {
	var keys []string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.URL.Query().Get("api_key"))
		if r.URL.Query().Get("page") == "" {
			fmt.Fprintf(w, `{"next":%q,"items":[1]}`, srvURL+"/?page=2")
			return
		}
		io.WriteString(w, `{"next":null,"items":[2]}`)
	}))
	defer srv.Close()
	srvURL = srv.URL
	_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL, Root: "items",
		Auth:     &cfg.AuthSpec{APIKey: &cfg.APIKeyAuth{In: "query", Name: "api_key", Value: "k123"}},
		Paginate: &cfg.PaginateConfig{Strategy: "link", NextPath: "next"}})
	if err != nil || len(keys) != 2 || keys[0] != "k123" || keys[1] != "k123" {
		t.Errorf("keys = %v, err = %v", keys, err)
	}
}

func TestAuthEmptySecretNamesTheEnvVar(t *testing.T) {
	srv := echoAuth(t)
	c := loadAuthSource(t, srv.URL, `auth: {bearer: "${env.TB_TEST_UNSET_TOKEN}"}`)
	c.SubstituteEnv()
	_, err := fetch(t, c.Data.Sources["s"])
	if err == nil || err.Error() != "auth: bearer is empty (${env.TB_TEST_UNSET_TOKEN} is unset)" {
		t.Errorf("err = %v", err)
	}
}

// loadAuthSource loads a one-source config through the real loader, so
// auth's templates are captured as they are in use.
func loadAuthSource(t *testing.T, url, auth string) *cfg.Config {
	t.Helper()
	path := t.TempDir() + "/c.yaml"
	src := fmt.Sprintf(`
data:
  sources:
    s:
      type: http
      url: %s
      %s
tui:
  components:
    t: {type: table, source: s, columns: [{title: A, value: a}]}
  screen: {title: T, layout: {component: t}}
`, url, auth)
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Secrets never reach an error: not in the message, the URL or the body.
func TestAuthRedactsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"message":"bad key k123secret, header %s"}`, r.Header.Get("X-Token"))
	}))
	defer srv.Close()
	_, err := fetch(t, &cfg.Source{Type: "http", URL: srv.URL + "/x",
		Headers: map[string]string{"X-Token": "hdrsecret"},
		Auth:    &cfg.AuthSpec{APIKey: &cfg.APIKeyAuth{In: "query", Name: "key", Value: "k123secret"}}})
	he, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	all := he.Error() + he.Detail()
	for _, secret := range []string{"k123secret", "hdrsecret"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q leaked:\n%s", secret, all)
		}
	}
	if !strings.Contains(he.Detail(), "key=•••") {
		t.Errorf("detail = %s, want the query key masked", he.Detail())
	}
}

// The client-credentials grant: one token request, cached and shared,
// and refetched once when a request comes back 401.
func TestAuthOAuth2(t *testing.T) {
	var tokenHits, apiHits int
	issued := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits++
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "shh-secret" || r.Form.Get("scope") != "read write" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		issued++
		fmt.Fprintf(w, `{"access_token":"tok%d","token_type":"bearer","expires_in":3600}`, issued)
	})
	mux.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		// The first token is revoked: the server only takes tok2.
		if r.Header.Get("Authorization") != "Bearer tok2" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `[1]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := func() *cfg.Source {
		return &cfg.Source{Type: "http", URL: srv.URL + "/api/items", Auth: &cfg.AuthSpec{OAuth2: &cfg.OAuth2Auth{
			TokenURL: "/oauth/token", ClientID: "app", ClientSecret: "shh-secret", Scopes: []string{"read", "write"},
		}}}
	}
	if _, err := fetch(t, src()); err != nil {
		t.Fatal(err)
	}
	if tokenHits != 2 || apiHits != 2 {
		t.Errorf("token %d, api %d: want a 401, one refetch and one retry", tokenHits, apiHits)
	}
	// A second source with the same client shares the cached token.
	if _, err := fetch(t, src()); err != nil {
		t.Fatal(err)
	}
	if tokenHits != 2 {
		t.Errorf("token requests = %d: the cached token should be reused", tokenHits)
	}

	bad := src()
	bad.Auth.OAuth2.ClientSecret = "wrong-secret"
	bad.Auth.OAuth2.Scopes = []string{"other"}
	_, err := fetch(t, bad)
	if err == nil || !strings.HasPrefix(err.Error(), "auth: token request failed — HTTP 401 Unauthorized — invalid_client") {
		t.Errorf("err = %v", err)
	}
}

func TestRedactLeavesShortValues(t *testing.T) {
	a := newAuthState(&cfg.AuthSpec{Basic: &cfg.BasicAuth{User: "bob", Pass: "hunter22"}}, "", nil)
	if got := a.redact("bob's password is hunter22"); got != "bob's password is •••" {
		t.Errorf("got %q", got)
	}
}
