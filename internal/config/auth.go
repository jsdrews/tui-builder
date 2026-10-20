package config

// `auth:` on an http or websocket source: exactly one scheme, applied to
// every request. See plans/http-source.md §7.

import (
	"fmt"
	"regexp"
	"strings"
)

// AuthSpec holds exactly one scheme.
type AuthSpec struct {
	// Bearer sends `Authorization: Bearer <value>`.
	Bearer string `yaml:"bearer,omitempty"`
	// Basic sends HTTP basic auth.
	Basic *BasicAuth `yaml:"basic,omitempty"`
	// APIKey sends a key in a header, the query string or a cookie.
	APIKey *APIKeyAuth `yaml:"api_key,omitempty"`
	// OAuth2 fetches a token with the client-credentials grant and sends
	// it as a bearer token. http only.
	OAuth2 *OAuth2Auth `yaml:"oauth2,omitempty"`

	// templates holds each secret's template as written, before any
	// substitution, so a run-time "is empty" error can name the env var
	// that was unset.
	templates map[string]string
	// scheme is the scheme as loaded. A bearer whose ${env.X} resolves
	// empty must still be a bearer — one that fails as empty — rather
	// than no auth at all.
	scheme string
}

// BasicAuth is HTTP basic auth. Pass may be empty (some APIs take a key
// as the user).
type BasicAuth struct {
	User string `yaml:"user,omitempty"`
	Pass string `yaml:"pass,omitempty"`
}

// APIKeyAuth sends Value as Name: a header, a query parameter or a cookie.
type APIKeyAuth struct {
	In    string `yaml:"in,omitempty"`
	Name  string `yaml:"name,omitempty"`
	Value string `yaml:"value,omitempty"`
}

// OAuth2Auth is the OAuth 2 client-credentials grant.
type OAuth2Auth struct {
	// TokenURL may be relative to the source's url.
	TokenURL     string   `yaml:"token_url,omitempty"`
	ClientID     string   `yaml:"client_id,omitempty"`
	ClientSecret string   `yaml:"client_secret,omitempty"`
	Scopes       []string `yaml:"scopes,omitempty"`
	// ClientAuth is how the client authenticates to the token URL:
	// "body" (the default: client_id and client_secret in the form) or
	// "basic".
	ClientAuth string `yaml:"client_auth,omitempty"`
}

// Scheme names the one scheme set: bearer, basic, api_key or oauth2.
func (a *AuthSpec) Scheme() string {
	if a == nil {
		return ""
	}
	if a.scheme != "" {
		return a.scheme
	}
	set := a.schemes()
	if len(set) == 1 {
		return set[0]
	}
	return ""
}

func (a *AuthSpec) schemes() []string {
	var out []string
	if a.Bearer != "" {
		out = append(out, "bearer")
	}
	if a.Basic != nil {
		out = append(out, "basic")
	}
	if a.APIKey != nil {
		out = append(out, "api_key")
	}
	if a.OAuth2 != nil {
		out = append(out, "oauth2")
	}
	return out
}

// Secrets lists the scheme's secret values as they stand: resolved,
// once substitution has run.
func (a *AuthSpec) Secrets() []string {
	if a == nil {
		return nil
	}
	var out []string
	add := func(v string) {
		if v != "" {
			out = append(out, v)
		}
	}
	add(a.Bearer)
	if a.Basic != nil {
		add(a.Basic.User)
		add(a.Basic.Pass)
	}
	if a.APIKey != nil {
		add(a.APIKey.Value)
	}
	if a.OAuth2 != nil {
		add(a.OAuth2.ClientSecret)
	}
	return out
}

// Template returns field's value as written in the config ("bearer",
// "basic.user", "api_key.value", "oauth2.client_id", …), before
// substitution.
func (a *AuthSpec) Template(field string) string {
	if a == nil {
		return ""
	}
	return a.templates[field]
}

// captureTemplates records each templated field as written. Load calls
// it once, before anything substitutes.
func (a *AuthSpec) captureTemplates() {
	a.scheme = a.Scheme()
	a.templates = map[string]string{}
	a.visit(func(field string, v *string) { a.templates[field] = *v })
}

// visit hands out every templated field by name and pointer.
func (a *AuthSpec) visit(fn func(field string, v *string)) {
	if a == nil {
		return
	}
	fn("bearer", &a.Bearer)
	if a.Basic != nil {
		fn("basic.user", &a.Basic.User)
		fn("basic.pass", &a.Basic.Pass)
	}
	if a.APIKey != nil {
		fn("api_key.value", &a.APIKey.Value)
	}
	if o := a.OAuth2; o != nil {
		fn("oauth2.token_url", &o.TokenURL)
		fn("oauth2.client_id", &o.ClientID)
		fn("oauth2.client_secret", &o.ClientSecret)
	}
}

// Rewrite returns a copy of the spec with fn applied to every templated
// field: an http action resolves its inputs into it at run time.
func (a *AuthSpec) Rewrite(fn func(string) string) *AuthSpec {
	out := a.Clone()
	out.visit(func(_ string, v *string) { *v = fn(*v) })
	return out
}

// Clone deep-copies the spec.
func (a *AuthSpec) Clone() *AuthSpec {
	if a == nil {
		return nil
	}
	out := *a
	if a.Basic != nil {
		b := *a.Basic
		out.Basic = &b
	}
	if a.APIKey != nil {
		k := *a.APIKey
		out.APIKey = &k
	}
	if a.OAuth2 != nil {
		o := *a.OAuth2
		o.Scopes = append([]string(nil), a.OAuth2.Scopes...)
		out.OAuth2 = &o
	}
	return &out
}

// OwnedHeader is the header auth sets, or "" for a query api_key.
func (a *AuthSpec) OwnedHeader() string {
	switch a.Scheme() {
	case "bearer", "basic", "oauth2":
		return "Authorization"
	case "api_key":
		switch a.APIKey.In {
		case "header":
			return a.APIKey.Name
		case "cookie":
			return "Cookie"
		}
	}
	return ""
}

// validateAuth checks the scheme and where it puts the secret.
func (s *Source) validateAuth(path string) error {
	a := s.Auth
	if a == nil {
		return nil
	}
	path += ".auth"
	if s.Type != "http" && s.Type != "websocket" {
		return fmt.Errorf("%s: only applies to type: http and websocket", path)
	}
	if n := len(a.schemes()); n != 1 {
		return fmt.Errorf("%s: set exactly one of bearer, basic, api_key, oauth2 (got %d)", path, n)
	}
	switch a.Scheme() {
	case "basic":
		if a.Basic.User == "" {
			return fmt.Errorf("%s.basic.user: required", path)
		}
	case "api_key":
		k := a.APIKey
		switch k.In {
		case "header", "query", "cookie":
		case "":
			return fmt.Errorf("%s.api_key.in: required (header, query or cookie)", path)
		default:
			return fmt.Errorf("%s.api_key.in: %q isn't one of header, query, cookie", path, k.In)
		}
		if k.Name == "" {
			return fmt.Errorf("%s.api_key.name: required — the header, query parameter or cookie the key goes in", path)
		}
	case "oauth2":
		o := a.OAuth2
		if s.Type == "websocket" {
			return fmt.Errorf("%s.oauth2: doesn't apply to a websocket — use bearer with a token from the environment", path)
		}
		for field, v := range map[string]string{"token_url": o.TokenURL, "client_id": o.ClientID, "client_secret": o.ClientSecret} {
			if v == "" {
				return fmt.Errorf("%s.oauth2.%s: required", path, field)
			}
		}
		switch o.ClientAuth {
		case "", "body", "basic":
		default:
			return fmt.Errorf("%s.oauth2.client_auth: %q isn't body or basic", path, o.ClientAuth)
		}
	}

	if h := a.OwnedHeader(); h != "" {
		for name := range s.Headers {
			if strings.EqualFold(name, h) {
				return fmt.Errorf("%s.headers: %q is set by auth.%s — set one or the other (`unset: [auth]` to drop an inherited auth)", strings.TrimSuffix(path, ".auth"), name, a.Scheme())
			}
		}
	}
	return nil
}

// secretHeader matches a header name whose value is treated as a
// secret and masked in output.
var secretHeader = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|cookie)$|token|key|secret`)

// IsSecretHeader reports whether a header's value is a secret, by name.
func IsSecretHeader(name string) bool { return secretHeader.MatchString(name) }
