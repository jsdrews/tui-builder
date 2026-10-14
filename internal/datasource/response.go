package datasource

// The response side of an http source: one place that sends a request
// and reads what came back, decides whether a status is data, empty or
// an error, finds the error's message, and reads response-level values
// through locators. See plans/http-source.md §4–5.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
)

// response is one answered request, whatever its status.
type response struct {
	method string
	url    string // the URL that answered, after any redirects
	status int
	header http.Header
	raw    []byte
}

func (r *response) ok() bool { return r.status/100 == 2 }

// send issues one request against url with body, under the source's
// timeout, and reads the whole response.
func (s *httpSource) send(ctx context.Context, url, body string) (*response, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	resp, err := s.doWith(ctx, url, body)
	if err != nil {
		// A transport error quotes the URL, query api_key and all.
		return nil, s.auth.redactErr(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return answered(resp, raw), nil
}

func answered(resp *http.Response, raw []byte) *response {
	r := &response{method: resp.Request.Method, url: resp.Request.URL.String(), status: resp.StatusCode, header: resp.Header, raw: raw}
	return r
}

// empty reports a response that answers with the empty result rather
// than data or an error: a status listed in empty_on, a 204, or a 2xx
// with no body.
func (s *httpSource) empty(r *response) bool {
	if s.emptyOn[r.status] {
		return true
	}
	return r.ok() && (r.status == http.StatusNoContent || len(strings.TrimSpace(string(r.raw))) == 0)
}

// parse decodes a JSON response. It returns nil for the empty result.
// A non-2xx is an *HTTPError; so is a 2xx whose explicit error_path
// resolves to something. Either way the decoded body comes back too, so
// a caller can keep the data that arrived alongside the error.
func (s *httpSource) parse(r *response) (any, error) {
	if s.empty(r) {
		return nil, nil
	}
	var parsed any
	perr := json.Unmarshal(r.raw, &parsed)
	if !r.ok() {
		if perr != nil {
			parsed = nil
		}
		return parsed, s.httpError(r, parsed)
	}
	if perr != nil {
		return nil, fmt.Errorf("parse json: %w", perr)
	}
	if s.errorPath != "" {
		if v := s.locate(s.errorPath, parsed, r, nil); present(v) {
			return parsed, s.masked(&HTTPError{Status: r.status, Message: messageText(v), Method: r.method, URL: r.url, Body: snippet(r.raw, detailBytes)})
		}
	}
	return parsed, nil
}

// partial is the data kept alongside an error: what root: finds in the
// error's body, when root is set and finds something.
func (s *httpSource) partial(parsed any) any {
	if s.root == "" || parsed == nil {
		return nil
	}
	if v := applyRoot(parsed, s.root); present(v) {
		return v
	}
	return nil
}

// HTTPError is a response that failed: a non-2xx status, or a 2xx whose
// error_path found an error. Its message reads
// "HTTP 404 Not Found — <message>"; Detail has the request and body for
// the output console.
type HTTPError struct {
	Status  int
	Message string
	Method  string
	URL     string
	Body    string
}

func (e *HTTPError) Error() string {
	head := fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status))
	head = strings.TrimSpace(head)
	if e.Message == "" {
		return head
	}
	return head + " — " + e.Message
}

// Detail is the console's longer form: the request, and what the server
// said.
func (e *HTTPError) Detail() string {
	d := e.Method + " " + e.URL
	if e.Body != "" {
		d += "\n\n" + e.Body
	}
	return d
}

const (
	messageBytes = 200  // a message's longest, when it's a body snippet
	detailBytes  = 2000 // the body shown in Detail
)

// errorGuesses are where APIs commonly put an error's message, tried in
// order when error_path is unset.
var errorGuesses = []string{"message", "error.message", "error", "error_description", "detail", "title", "errors.0.message"}

// httpError builds the error for a non-2xx response: the message from
// error_path, else the first built-in guess that finds a string, else
// the start of the body.
func (s *httpSource) httpError(r *response, parsed any) *HTTPError {
	e := &HTTPError{Status: r.status, Method: r.method, URL: r.url, Body: snippet(r.raw, detailBytes)}
	e.Message = s.errorMessage(r, parsed)
	return s.masked(e)
}

func (s *httpSource) errorMessage(r *response, parsed any) string {
	if s.errorPath != "" {
		if v := s.locate(s.errorPath, parsed, r, nil); present(v) {
			return messageText(v)
		}
	}
	if parsed != nil {
		for _, path := range errorGuesses {
			if str, ok := Get(parsed, path).(string); ok && strings.TrimSpace(str) != "" {
				return str
			}
		}
	}
	return snippet(r.raw, messageBytes)
}

// masked is e with the source's secrets masked out of it.
func (s *httpSource) masked(e *HTTPError) *HTTPError {
	if s.auth == nil {
		return e
	}
	return s.auth.redactErr(e).(*HTTPError)
}

// locate reads a response-level value. A locator is a dot-path into the
// raw body (before root:), or:
//   - header:NAME  a response header (case-insensitive; the first value)
//   - link:REL     the URL of the Link header entry with that rel,
//     resolved against the request URL
//   - last:PATH    a dot-path into the last of items
//
// Anything not there is nil.
func (s *httpSource) locate(loc string, parsed any, r *response, items []any) any {
	switch {
	case strings.HasPrefix(loc, "header:"):
		if v := r.header.Get(strings.TrimPrefix(loc, "header:")); v != "" {
			return v
		}
		return nil
	case strings.HasPrefix(loc, "link:"):
		if u := linkRel(r.header.Values("Link"), strings.TrimPrefix(loc, "link:")); u != "" {
			return resolveRef(r.url, u)
		}
		return nil
	case strings.HasPrefix(loc, "last:"):
		if len(items) == 0 {
			return nil
		}
		return Get(items[len(items)-1], strings.TrimPrefix(loc, "last:"))
	}
	return Get(parsed, loc)
}

// linkRel finds the URL of the RFC 8288 Link entry whose rel includes
// rel. An entry marked results="false" (Sentry's last page) counts as
// absent.
func linkRel(headers []string, rel string) string {
	for _, h := range headers {
		for _, entry := range splitLinks(h) {
			open, close := strings.IndexByte(entry, '<'), strings.IndexByte(entry, '>')
			if open < 0 || close < open {
				continue
			}
			target := entry[open+1 : close]
			params := map[string]string{}
			for _, p := range strings.Split(entry[close+1:], ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
				if ok {
					params[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
				}
			}
			if !containsField(params["rel"], rel) || params["results"] == "false" {
				continue
			}
			return target
		}
	}
	return ""
}

// splitLinks splits a Link header on the commas between entries, not
// the ones inside a <URL>.
func splitLinks(h string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range h {
		switch c {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, h[start:i])
				start = i + 1
			}
		}
	}
	return append(out, h[start:])
}

// containsField reports whether the space-separated list has field
// (case-insensitively, as rel values compare).
func containsField(list, field string) bool {
	for _, f := range strings.Fields(list) {
		if strings.EqualFold(f, field) {
			return true
		}
	}
	return false
}

// resolveRef resolves a possibly relative URL against base (RFC 3986).
func resolveRef(base, ref string) string {
	b, err := neturl.Parse(base)
	if err != nil {
		return ref
	}
	u, err := b.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}

// present reports a value that is there: not nil, not "", and not an
// empty list or object.
func present(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// messageText renders a located error value as one line: a string as
// itself, anything else as compact JSON.
func messageText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return snippet(b, messageBytes)
}

// snippet is the start of b as trimmed text, cut at n bytes.
func snippet(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
