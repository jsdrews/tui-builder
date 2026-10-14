package config

import (
	"strings"
	"testing"
)

func TestAuthErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"two schemes", `{type: http, url: "https://h", auth: {bearer: x, basic: {user: u}}}`, "set exactly one of bearer, basic, api_key, oauth2 (got 2)"},
		{"none", `{type: http, url: "https://h", auth: {}}`, "(got 0)"},
		{"on exec", `{type: exec, command: [echo], auth: {bearer: x}}`, "auth: only applies to type: http and websocket"},
		{"api_key in", `{type: http, url: "https://h", auth: {api_key: {in: body, name: k, value: v}}}`, `api_key.in: "body" isn't one of header, query, cookie`},
		{"api_key name", `{type: http, url: "https://h", auth: {api_key: {in: header, value: v}}}`, "api_key.name: required"},
		{"oauth2 on websocket", `{type: websocket, url: "wss://h", auth: {oauth2: {token_url: /t, client_id: a, client_secret: b}}}`, "oauth2: doesn't apply to a websocket"},
		{"oauth2 missing", `{type: http, url: "https://h", auth: {oauth2: {token_url: /t, client_id: a}}}`, "oauth2.client_secret: required"},
		{"owned header", `{type: http, url: "https://h", headers: {authorization: "Bearer x"}, auth: {bearer: y}}`, `"authorization" is set by auth.bearer`},
		{"owned api_key header", `{type: http, url: "https://h", headers: {X-Key: a}, auth: {api_key: {in: header, name: x-key, value: v}}}`, `"X-Key" is set by auth.api_key`},
		{"owned query key", `{type: http, url: "https://h?api_key=1", auth: {api_key: {in: query, name: api_key, value: v}}}`, `"api_key" is set by auth (api_key)`},
		{"typo", `{type: http, url: "https://h", auth: {bearer: x, bearr: y}}`, `auth: unknown field "bearr" (did you mean bearer?)`},
		{"nested typo", `{type: http, url: "https://h", auth: {oauth2: {token_url: /t, client_id: a, client_secrt: b}}}`, `auth.oauth2: unknown field "client_secrt" (did you mean client_secret?)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadData(t, "sources:\n  issues: "+c.src+"\n")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// A preset's auth is inherited whole; a source can drop it and set the
// header itself.
func TestAuthFromPreset(t *testing.T) {
	s := mustLoadData(t, `
presets:
  api: {type: http, url: "https://h", auth: {bearer: "${env.TOKEN}"}}
sources:
  issues: {extends: api}
  other: {extends: api, unset: [auth], headers: {Authorization: "Token x"}}
`)
	if s.Auth.Scheme() != "bearer" || s.Auth.Template("bearer") != "${env.TOKEN}" {
		t.Errorf("auth = %+v", s.Auth)
	}
}

func TestSecretHeaderNames(t *testing.T) {
	for name, want := range map[string]bool{
		"Authorization": true, "proxy-authorization": true, "Cookie": true,
		"X-Api-Key": true, "PRIVATE-TOKEN": true, "X-Client-Secret": true,
		"Accept": false, "User-Agent": false, "Set-Cookie-Policy": false,
	} {
		if got := IsSecretHeader(name); got != want {
			t.Errorf("IsSecretHeader(%q) = %v", name, got)
		}
	}
}
