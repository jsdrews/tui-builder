package config

// The request side of an http source: `query:`, the `json:` / `form:`
// bodies, how templates resolve inside them and inside the url, and
// which query keys per-request logic owns. See plans/http-source.md §2–3.

import (
	"encoding/json"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// QueryValue is one `query:` or `form:` value: a string, or a list of
// strings that becomes repeated keys (?tag=a&tag=b).
type QueryValue []string

func (q *QueryValue) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			*q = nil
			return nil
		}
		*q = QueryValue{n.Value}
		return nil
	case yaml.SequenceNode:
		out := make(QueryValue, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: a list item must be a string", item.Line)
			}
			out = append(out, item.Value)
		}
		*q = out
		return nil
	}
	return fmt.Errorf("line %d: want a string or a list of strings", n.Line)
}

// empty reports whether v drops its key: no values, or one empty one.
func (v QueryValue) empty() bool {
	return len(v) == 0 || len(v) == 1 && v[0] == ""
}

// HasBody reports whether a request carries a body: `body:`, `json:`
// or `form:`.
func (s *Source) HasBody() bool {
	return s.Body != "" || s.JSON != nil || len(s.Form) > 0 || s.GraphQL != nil
}

var templateToken = regexp.MustCompile(`\$\{[^}]*\}`)

// exactToken matches a string that is one token and nothing else.
var exactToken = regexp.MustCompile(`^\$\{[^}]*\}$`)

// rewriteTemplates applies fn to every templated string on s. It's the
// one walk behind SubstituteStrings (params, selection) and the config's
// env pass, so a field is templated by every pass or by none.
//
// Two fields get more than fn:
//   - url: each token is replaced on its own and escaped by where it
//     sits. See substituteURL.
//   - json: a value that is exactly one token and resolves empty drops
//     its key (or list item).
func (s *Source) rewriteTemplates(fn func(string) string) {
	s.URL = substituteURL(s.URL, fn)
	s.Body = fn(s.Body)
	s.Method = fn(s.Method)
	s.Path = fn(s.Path)
	for k, v := range s.Headers {
		s.Headers[k] = fn(v)
	}
	for i, c := range s.Command {
		s.Command[i] = fn(c)
	}
	for k, v := range s.Env {
		s.Env[k] = fn(v)
	}
	for i, m := range s.InitialMessages {
		s.InitialMessages[i] = fn(m)
	}
	for _, vals := range s.Query {
		for i, v := range vals {
			vals[i] = fn(v)
		}
	}
	for _, vals := range s.Form {
		for i, v := range vals {
			vals[i] = fn(v)
		}
	}
	if s.JSON != nil {
		s.JSON, _ = substituteJSON(s.JSON, fn)
	}
	// A GraphQL query is never templated; only its variables are.
	if s.GraphQL != nil && s.GraphQL.Variables != nil {
		vars, _ := substituteJSON(s.GraphQL.Variables, fn)
		s.GraphQL.Variables = vars.(map[string]any)
	}
	s.Auth.visit(func(_ string, v *string) { *v = fn(*v) })
	if s.Paginate != nil && s.Paginate.Patch != nil {
		patch, _ := substituteJSON(s.Paginate.Patch, fn)
		s.Paginate.Patch = patch.(map[string]any)
	}
}

// substituteJSON returns v with fn applied to every string in it. ok is
// false for a string that was exactly one token and resolved empty:
// its parent drops it.
func substituteJSON(v any, fn func(string) string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, sub := range x {
			if r, ok := substituteJSON(sub, fn); ok {
				out[k] = r
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(x))
		for _, sub := range x {
			if r, ok := substituteJSON(sub, fn); ok {
				out = append(out, r)
			}
		}
		return out, true
	case string:
		r := fn(x)
		if r == "" && exactToken.MatchString(x) {
			return nil, false
		}
		return r, true
	}
	return v, true
}

// typeJSONParams gives an exact `${params.NAME}` string in json: its
// parameter's type: a number for int, a boolean for bool. Runs before
// the text substitution, which then leaves the typed values alone.
func typeJSONParams(v any, decl map[string]*Parameter, resolved map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, sub := range x {
			out[k] = typeJSONParams(sub, decl, resolved)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, sub := range x {
			out[i] = typeJSONParams(sub, decl, resolved)
		}
		return out
	case string:
		m := paramToken.FindStringSubmatch(x)
		if m == nil || m[0] != x {
			return x
		}
		p, val := decl[m[1]], resolved[m[1]]
		if p == nil || val == "" {
			return x
		}
		switch p.Type {
		case "int", "bool":
			var typed any
			if json.Unmarshal([]byte(val), &typed) == nil {
				return typed
			}
		}
	}
	return v
}

// substituteURL resolves the tokens in a url template one at a time,
// escaping each value by where its token sits:
//   - before the path (a token that is the base, like ${env.API}):
//     inserted as is
//   - in the path: path-escaped, except "/" is kept, so a value like
//     owner/repo still spans two segments
//   - in the query string: query-escaped
//
// A token fn leaves unresolved stays for a later pass.
func substituteURL(u string, fn func(string) string) string {
	if !strings.Contains(u, "${") {
		return u
	}
	pathStart := 0
	if i := strings.Index(u, "://"); i >= 0 {
		pathStart = len(u)
		if j := strings.IndexAny(u[i+3:], "/?#"); j >= 0 {
			pathStart = i + 3 + j
		}
	} else if loc := templateToken.FindStringIndex(u); loc != nil && loc[0] == 0 {
		pathStart = loc[1]
	}
	queryStart := len(u)
	if i := strings.IndexByte(u, '?'); i >= 0 {
		queryStart = i
	}
	var b strings.Builder
	last := 0
	for _, loc := range templateToken.FindAllStringIndex(u, -1) {
		tok := u[loc[0]:loc[1]]
		val := fn(tok)
		b.WriteString(u[last:loc[0]])
		switch {
		case val == tok:
			b.WriteString(tok)
		case loc[0] < pathStart:
			b.WriteString(val)
		case loc[0] > queryStart:
			b.WriteString(neturl.QueryEscape(val))
		default:
			b.WriteString(strings.ReplaceAll(neturl.PathEscape(val), "%2F", "/"))
		}
		last = loc[1]
	}
	b.WriteString(u[last:])
	return b.String()
}

// validateRequest checks the request fields: who may have them, at most
// one body, and that no key per-request logic owns is also set by the
// author in url: or query:.
func (s *Source) validateRequest(path string) error {
	if s.Type != "http" {
		for field, set := range map[string]bool{"query": len(s.Query) > 0, "json": s.JSON != nil, "form": len(s.Form) > 0, "graphql": s.GraphQL != nil} {
			if set {
				return fmt.Errorf("%s.%s: only applies to type: http", path, field)
			}
		}
		return nil
	}
	var bodies []string
	if s.JSON != nil {
		bodies = append(bodies, "json")
	}
	if len(s.Form) > 0 {
		bodies = append(bodies, "form")
	}
	if s.Body != "" {
		bodies = append(bodies, "body")
	}
	if s.GraphQL != nil {
		bodies = append(bodies, "graphql")
	}
	if len(bodies) > 1 {
		return fmt.Errorf("%s: %s are mutually exclusive — a request has one body", path, strings.Join(bodies, " and "))
	}
	if s.JSON != nil {
		if _, err := json.Marshal(s.JSON); err != nil {
			return fmt.Errorf("%s.json: can't be encoded as JSON: %w", path, err)
		}
	}
	if s.GraphQL != nil {
		if err := s.GraphQL.validate(path + ".graphql"); err != nil {
			return err
		}
	}

	owned := s.ownedQueryKeys()
	if len(owned) == 0 {
		return nil
	}
	if _, raw, ok := strings.Cut(s.URL, "?"); ok {
		raw, _, _ = strings.Cut(raw, "#")
		for _, pair := range strings.Split(raw, "&") {
			k, _, _ := strings.Cut(pair, "=")
			if key, err := neturl.QueryUnescape(k); err == nil {
				k = key
			}
			if owner, ok := owned[k]; ok {
				return fmt.Errorf("%s.url: %q is set by %s — leave it out of the url", path, k, owner)
			}
		}
	}
	for _, k := range sortedKeys(s.Query) {
		if owner, ok := owned[k]; ok {
			return fmt.Errorf("%s.query: %q is set by %s", path, k, owner)
		}
	}
	return nil
}

// ownedQueryKeys maps each query key per-request logic writes on every
// request to the field that writes it. An author's value there would be
// overwritten every time, so setting one is a load error rather than a
// silent drop.
//
// Keys written only some of the time aren't owned: search_param, sort_param,
// filters and find.params are set only while the user searches, sorts,
// filters or finds, so a value in url: or query: is the default they
// replace (`?q=tolkien` until the user types a search).
func (s *Source) ownedQueryKeys() map[string]string {
	owned := map[string]string{}
	if a := s.Auth; a.Scheme() == "api_key" && a.APIKey.In == "query" {
		owned[a.APIKey.Name] = "auth (api_key)"
	}
	if p := s.Paginate; p != nil {
		for _, sh := range []struct{ field, key string }{
			{"cursor_param", p.CursorParam}, {"offset_param", p.OffsetParam},
			{"page_param", p.PageParam}, {"limit_param", p.LimitParam},
		} {
			if sh.key != "" {
				owned[sh.key] = "paginate (" + sh.field + ")"
			}
		}
		if !s.HasBody() {
			for k := range p.Patch {
				owned[k] = "paginate (patch)"
			}
		}
	}
	w := s.Window
	if w == nil {
		return owned
	}
	own := func(key, field string) {
		if key != "" {
			owned[key] = "window (" + field + ")"
		}
	}
	own(w.LimitParam, "limit_param")
	if w.Cursor == "" {
		own(w.OffsetParam, "offset_param")
		own(w.PageParam, "page_param")
	}
	// Without a body, an Anchored window's patches go into the query
	// string; with one, into the body.
	if w.Cursor != "" && !s.HasBody() {
		for _, k := range sortedKeys(w.Older) {
			own(k, "older")
		}
		for _, k := range sortedKeys(w.Newer) {
			own(k, "newer")
		}
	}
	return owned
}

// cloneJSON deep-copies a decoded YAML / JSON value.
func cloneJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, sub := range x {
			out[k] = cloneJSON(sub)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, sub := range x {
			out[i] = cloneJSON(sub)
		}
		return out
	}
	return v
}

func cloneQuery(m map[string]QueryValue) map[string]QueryValue {
	if m == nil {
		return nil
	}
	out := make(map[string]QueryValue, len(m))
	for k, v := range m {
		out[k] = append(QueryValue(nil), v...)
	}
	return out
}

// bodyText is the body as text, for checks that look for tokens in it.
func (s *Source) bodyText() string {
	if s.GraphQL != nil {
		b, _ := json.Marshal(s.GraphQL.Body())
		return string(b)
	}
	if s.JSON != nil {
		b, _ := json.Marshal(s.JSON)
		return string(b)
	}
	return s.Body
}
