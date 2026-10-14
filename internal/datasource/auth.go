package datasource

// Applying `auth:` to a request, the OAuth 2 token cache, and redacting
// secrets out of anything a source reports. See plans/http-source.md §7.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// authState is one source's auth: its scheme, and the secrets to mask.
type authState struct {
	spec *cfg.AuthSpec
	// base is the source's url, which a relative token_url resolves
	// against.
	base string
	// secrets are the values masked in everything the source reports:
	// the resolved auth values and secret-named headers.
	secrets []string
}

// minSecret is the shortest value masked. Shorter ones would mask
// ordinary text ("x", "on").
const minSecret = 4

func newAuthState(spec *cfg.AuthSpec, base string, headers map[string]string) *authState {
	a := &authState{spec: spec, base: base}
	for _, v := range spec.Secrets() {
		a.addSecret(v)
	}
	for name, v := range headers {
		if cfg.IsSecretHeader(name) {
			// "Bearer abc…": mask the credential, and the whole value.
			a.addSecret(v)
			if _, cred, ok := strings.Cut(v, " "); ok {
				a.addSecret(cred)
			}
		}
	}
	return a
}

func (a *authState) addSecret(v string) {
	if a == nil || len(v) < minSecret {
		return
	}
	for _, s := range a.secrets {
		if s == v {
			return
		}
	}
	a.secrets = append(a.secrets, v)
}

// redact masks every secret in s, raw and as it appears URL-encoded.
func (a *authState) redact(s string) string {
	if a == nil {
		return s
	}
	for _, secret := range a.secrets {
		for _, form := range []string{secret, neturl.QueryEscape(secret), neturl.PathEscape(secret)} {
			s = strings.ReplaceAll(s, form, "•••")
		}
	}
	if tok := a.currentToken(); len(tok) >= minSecret {
		s = strings.ReplaceAll(s, tok, "•••")
	}
	return s
}

// redactErr masks secrets in an error's text, keeping an *HTTPError's
// structure.
func (a *authState) redactErr(err error) error {
	if a == nil || err == nil {
		return err
	}
	if he, ok := err.(*HTTPError); ok {
		c := *he
		c.Message, c.URL, c.Body = a.redact(c.Message), a.redact(c.URL), a.redact(c.Body)
		return &c
	}
	if msg := a.redact(err.Error()); msg != err.Error() {
		return redactedError{msg: msg, err: err}
	}
	return err
}

type redactedError struct {
	msg string
	err error
}

func (e redactedError) Error() string { return e.msg }
func (e redactedError) Unwrap() error { return e.err }

// emptyErr is the run-time error for a scheme whose secret resolved
// empty, naming the env var behind it when that's what it was.
func (a *authState) emptyErr(field string) error {
	msg := fmt.Sprintf("auth: %s is empty", field)
	if t := a.spec.Template(field); t != "" {
		if m := envOnly.FindStringSubmatch(t); m != nil {
			msg += fmt.Sprintf(" (%s is unset)", t)
		} else {
			msg += fmt.Sprintf(" (from %s)", t)
		}
	}
	return fmt.Errorf("%s", msg)
}

var envOnly = regexp.MustCompile(`^\$\{env\.[A-Za-z0-9_]+\}$`)

// apply sets the scheme on a request: a header, a query parameter or a
// cookie. oauth2 fetches (or reuses) its token first. Query keys are set
// last, over everything else, so they ride on followed next URLs too.
func (a *authState) apply(ctx context.Context, h http.Header, u *neturl.URL, timeout time.Duration) error {
	if a == nil || a.spec == nil {
		return nil
	}
	sp := a.spec
	switch sp.Scheme() {
	case "bearer":
		if sp.Bearer == "" {
			return a.emptyErr("bearer")
		}
		h.Set("Authorization", "Bearer "+sp.Bearer)
	case "basic":
		if sp.Basic.User == "" {
			return a.emptyErr("basic.user")
		}
		r := http.Request{Header: h}
		r.SetBasicAuth(sp.Basic.User, sp.Basic.Pass)
	case "api_key":
		k := sp.APIKey
		if k.Value == "" {
			return a.emptyErr("api_key.value")
		}
		switch k.In {
		case "header":
			h.Set(k.Name, k.Value)
		case "query":
			q := u.Query()
			q.Set(k.Name, k.Value)
			u.RawQuery = q.Encode()
		case "cookie":
			h.Set("Cookie", (&http.Cookie{Name: k.Name, Value: k.Value}).String())
		}
	case "oauth2":
		tok, err := a.token(ctx, timeout)
		if err != nil {
			return err
		}
		h.Set("Authorization", "Bearer "+tok)
	}
	return nil
}

// ── OAuth 2 client credentials ──────────────────────────────────────

// tokenCache holds client-credentials tokens for the life of the
// process, shared by every source with the same token URL, client and
// scopes.
var tokenCache = struct {
	sync.Mutex
	m map[string]cachedToken
}{m: map[string]cachedToken{}}

type cachedToken struct {
	value  string
	expiry time.Time // zero: never
}

// tokenRefreshEarly is how long before expires_in a token is replaced.
const tokenRefreshEarly = 30 * time.Second

func (a *authState) tokenKey() string {
	o := a.spec.OAuth2
	return a.tokenURL() + "\x00" + o.ClientID + "\x00" + strings.Join(o.Scopes, " ")
}

func (a *authState) tokenURL() string { return resolveRef(a.base, a.spec.OAuth2.TokenURL) }

// currentToken is the cached token, if there is one, for redaction.
func (a *authState) currentToken() string {
	if a == nil || a.spec.Scheme() != "oauth2" {
		return ""
	}
	tokenCache.Lock()
	defer tokenCache.Unlock()
	return tokenCache.m[a.tokenKey()].value
}

// forgetToken drops the cached token, after a 401.
func (a *authState) forgetToken() {
	tokenCache.Lock()
	delete(tokenCache.m, a.tokenKey())
	tokenCache.Unlock()
}

// token returns a valid token, fetching one when the cache has none or
// it's within tokenRefreshEarly of expiring.
func (a *authState) token(ctx context.Context, timeout time.Duration) (string, error) {
	key := a.tokenKey()
	tokenCache.Lock()
	c, ok := tokenCache.m[key]
	tokenCache.Unlock()
	if ok && (c.expiry.IsZero() || time.Now().Before(c.expiry.Add(-tokenRefreshEarly))) {
		return c.value, nil
	}
	o := a.spec.OAuth2
	if o.ClientID == "" {
		return "", a.emptyErr("oauth2.client_id")
	}
	if o.ClientSecret == "" {
		return "", a.emptyErr("oauth2.client_secret")
	}
	form := neturl.Values{"grant_type": {"client_credentials"}}
	if len(o.Scopes) > 0 {
		form.Set("scope", strings.Join(o.Scopes, " "))
	}
	if o.ClientAuth != "basic" {
		form.Set("client_id", o.ClientID)
		form.Set("client_secret", o.ClientSecret)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("auth: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	if o.ClientAuth == "basic" {
		req.SetBasicAuth(neturl.QueryEscape(o.ClientID), neturl.QueryEscape(o.ClientSecret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", a.redactErr(fmt.Errorf("auth: token request failed — %w", err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	r := answered(resp, raw)
	if !r.ok() {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		he := (&httpSource{}).httpError(r, parsed)
		return "", a.redactErr(fmt.Errorf("auth: token request failed — %s", a.redact(he.Error())))
	}
	var body struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("auth: token request failed — the response has no access_token")
	}
	c = cachedToken{value: body.AccessToken}
	if secs, ok := toInt(jsonValue(body.ExpiresIn)); ok && secs > 0 {
		c.expiry = time.Now().Add(time.Duration(secs) * time.Second)
	}
	tokenCache.Lock()
	tokenCache.m[key] = c
	tokenCache.Unlock()
	return c.value, nil
}

// jsonValue decodes a raw JSON value (expires_in comes as a number or,
// from some servers, a string).
func jsonValue(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}
