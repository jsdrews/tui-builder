// Package action is the write side of the data layer: it turns a
// declared cfg.Action plus a set of input values into something
// concrete, and normalises whatever comes back into one Result.
//
// It sits alongside internal/pipeline rather than inside it. Pipelines
// are the read side — polled, refreshed, cached — and an action is the
// one thing in the config that must never be re-run just because a timer
// fired. Keeping them in separate packages is that invariant made
// structural.
//
// Like the rest of the data layer this package MUST NOT import
// internal/screen, internal/build, or any tuilib package; the boundary
// is enforced by scripts/check-data-layer-boundary.sh. That constraint
// is what splits resolution from execution: Resolve turns inputs into a
// concrete argv or *http.Request and stops. Running an exec action in a
// TUI wants tuilib's runner.CaptureWith (so output streams into the
// console), which lives on the other side of the boundary — so the
// consumer runs it, we only say what to run.
package action

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// defaultHTTPTimeout bounds an http action that doesn't declare one.
// Actions are user-initiated and synchronous from the user's point of
// view, so this is deliberately shorter than a source's polling budget.
const defaultHTTPTimeout = 30 * time.Second

// Result is the normalised outcome of any action kind. The two kinds
// report success differently — an exit status and an HTTP status aren't
// the same thing — so the normalisation happens here, once, rather than
// at every consumer.
type Result struct {
	// OK is the verdict: per-kind default, or the action's `success:`
	// expression when it declares one.
	OK bool
	// Code is the exit status (exec) or HTTP status (http).
	Code int
	// Output is stdout (exec) or the response body (http).
	Output string
	// Summary is the head line — what paints the statusbar and heads the
	// console entry. Comes from the action's message: / error_message:
	// when set, otherwise a per-kind default.
	Summary string
}

// Resolved is a fully-substituted action, ready to run. Exec actions
// carry an argv; http actions carry everything needed to build a
// request. Produced by Resolve, consumed by the TUI (which runs exec
// through tuilib's capture) or by Do (which runs http here).
type Resolved struct {
	Kind string
	// Argv is the concrete command line for an exec action.
	Argv []string
	// The http fields, post-substitution.
	Method  string
	URL     string
	Headers map[string]string
	Body    string
	Timeout time.Duration

	def *cfg.Action
	// vals is the resolved input set, kept so message: / error_message:
	// can name what the action acted on ("synced ${inputs.app}") rather
	// than only reporting a status code.
	vals map[string]string
}

// Values is the effective input set this action resolved with —
// bind: values and form entries, with declared Defaults filled in.
//
// Callers preview an action before running it (a confirm message naming
// what is about to happen) and MUST substitute against this rather than
// against the raw bind map, or a defaulted input renders empty in the
// preview while the argv gets the real value. A confirm that disagrees
// with what runs is worse than no confirm.
func (r *Resolved) Values() map[string]string { return r.vals }

// Inputs collects the values an action's declared inputs resolve to.
// Built by the caller from a binding's bind: map, the generated form's
// values, and each input's Default.
type Inputs map[string]string

// Resolve substitutes inputs into an action's templated fields and
// returns something concrete. Missing values resolve to the input's
// Default, then to the empty string — the same forgiving behaviour the
// cursor-fetch path uses, so a URL stays well-formed rather than
// carrying a literal ${inputs.x} to the far end.
func Resolve(a *cfg.Action, in Inputs) (*Resolved, error) {
	vals := withDefaults(a, in)
	r := &Resolved{Kind: a.Kind(), def: a, vals: vals}

	switch a.Kind() {
	case "exec":
		r.Argv = make([]string, len(a.Run))
		for i, arg := range a.Run {
			r.Argv[i] = substitute(arg, vals)
		}
		if len(r.Argv) == 0 {
			return nil, fmt.Errorf("action has an empty run argv")
		}
	case "http":
		r.Method = a.Method
		if r.Method == "" {
			r.Method = http.MethodPost
		}
		r.URL = substitute(a.URL, vals)
		r.Body = substitute(a.Body, vals)
		r.Headers = make(map[string]string, len(a.Headers))
		for k, v := range a.Headers {
			r.Headers[k] = substitute(v, vals)
		}
		r.Timeout = defaultHTTPTimeout
		if a.Timeout != "" {
			d, err := time.ParseDuration(a.Timeout)
			if err != nil {
				return nil, fmt.Errorf("timeout %q: %w", a.Timeout, err)
			}
			r.Timeout = d
		}
	default:
		return nil, fmt.Errorf("unknown action type %q", a.Type)
	}
	return r, nil
}

// Do runs a resolved http action and normalises the response. Exec
// actions are not run here — see the package doc.
func (r *Resolved) Do(ctx context.Context) Result {
	if r.Kind != "http" {
		return Result{Summary: fmt.Sprintf("action kind %q is not run by Do", r.Kind)}
	}
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return Result{Summary: err.Error()}
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	// A JSON body with no declared content type is the overwhelmingly
	// common case and forgetting the header produces a confusing 4xx
	// from the far end rather than an error here.
	if r.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Summary: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	return r.Finish(resp.StatusCode, string(raw))
}

// Finish normalises a raw (code, output) pair into a Result, applying
// the action's success: / message: / error_message: declarations.
//
// Exported because the exec path produces its code and output on the
// TUI side (tuilib's capture owns the process) and needs the same
// verdict logic applied to it.
func (r *Resolved) Finish(code int, output string) Result {
	res := Result{Code: code, Output: output}
	res.OK = r.verdict(code, output)

	tmpl := r.def.Message
	if !res.OK {
		tmpl = r.def.ErrorMessage
	}
	if tmpl != "" {
		res.Summary = substitute(tmpl, r.messageVals(code, output))
		return res
	}
	res.Summary = r.defaultSummary(res.OK, code, output)
	return res
}

// verdict decides OK. Per-kind defaults unless the action declares a
// success: expression — which exists because plenty of tools lie about
// their status (grep exits 1 on no-match; an API may 409 a sync that is
// already running, which isn't a failure worth painting red).
func (r *Resolved) verdict(code int, output string) bool {
	if src := r.def.Success; src != "" {
		p, err := expr.Compile(src)
		if err == nil {
			ok, err := expr.EvalBool(p, map[string]any{
				"code":   code,
				"output": output,
				"body":   parseJSON(output),
			})
			if err == nil {
				return ok
			}
		}
		// A malformed success: expression falls through to the default
		// rather than failing the action. The expression is a refinement
		// of a verdict we can already make; refusing to report anything
		// because the refinement is broken would be worse than reporting
		// the honest default.
	}
	if r.Kind == "http" {
		return code < 400
	}
	return code == 0
}

// defaultSummary is the head line when the action declares no message.
func (r *Resolved) defaultSummary(ok bool, code int, output string) string {
	if r.Kind == "http" {
		if ok {
			return fmt.Sprintf("%s %s → %d", r.Method, r.URL, code)
		}
		return fmt.Sprintf("%s %s failed → %d", r.Method, r.URL, code)
	}
	name := r.Argv[0]
	if ok {
		return name + " complete"
	}
	// The last non-empty line of output is where CLIs put the reason.
	if line := lastLine(output); line != "" {
		return line
	}
	return fmt.Sprintf("%s exited %d", name, code)
}

// messageVals is the substitution environment for message: /
// error_message:. Inputs stay available so a summary can name what it
// acted on; body.* reaches into a parsed JSON response, which is how an
// API's own error text gets into the statusbar instead of a status code.
func (r *Resolved) messageVals(code int, output string) map[string]string {
	vals := make(map[string]string, len(r.vals)+2)
	for k, v := range r.vals {
		vals[k] = v
	}
	vals["code"] = strconv.Itoa(code)
	vals["output"] = strings.TrimSpace(output)
	flattenJSON("body", parseJSON(output), vals)
	return vals
}

// withDefaults fills unset inputs from their declared Default.
func withDefaults(a *cfg.Action, in Inputs) map[string]string {
	vals := make(map[string]string, len(a.Inputs))
	for name, p := range a.Inputs {
		if p != nil && p.Default != "" {
			vals[name] = p.Default
		}
	}
	for k, v := range in {
		vals[k] = v
	}
	return vals
}

// substitute resolves the token forms this package owns and leaves
// everything else exactly as written:
//
//	${inputs.x} / ${x}   an input value (bare names are inputs)
//	${env.X}             an environment variable
//	${body.x}            a field of a parsed JSON response (message only)
//
// Leaving unrecognised tokens literal is load-bearing, not lazy. An exec
// action's argv routinely contains shell syntax that looks like a token
// but isn't: `sh -c "… | ${PAGER:-less} -R"` is a POSIX default-value
// expansion the shell must see intact. Eating it produces `| -R`, a
// command that fails in a way pointing nowhere near this function.
//
// A recognised token that has no value still resolves to "" — a
// half-substituted `${inputs.namespace}` reaching kubectl is worse than
// an empty one, and the same forgiving rule already governs cursor-driven
// fetches.
func substitute(s string, vals map[string]string) string {
	if s == "" || !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		token := s[i+2 : i+j]
		if v, ok := resolveToken(token, vals); ok {
			b.WriteString(v)
		} else {
			// Not ours — put it back verbatim, braces and all.
			b.WriteString(s[i : i+j+1])
		}
		s = s[i+j+1:]
	}
	return b.String()
}

// resolveToken maps one token body to its value, reporting whether this
// package owns the token at all.
func resolveToken(token string, vals map[string]string) (string, bool) {
	switch {
	case strings.HasPrefix(token, "env."):
		return os.Getenv(strings.TrimPrefix(token, "env.")), true
	case strings.HasPrefix(token, "inputs."):
		return vals[strings.TrimPrefix(token, "inputs.")], true
	case strings.HasPrefix(token, "body."), token == "code", token == "output":
		v, ok := vals[token]
		return v, ok
	}
	// A bare name is an input when one is declared under it. Anything
	// else — shell expansions especially — is left alone.
	v, ok := vals[token]
	return v, ok
}

// parseJSON decodes output as JSON, returning nil when it isn't. Used
// so success: and message: can reach into an API response without the
// caller having to know whether the far end speaks JSON.
func parseJSON(output string) any {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		return nil
	}
	return v
}

// flattenJSON walks a decoded JSON value into dotted string keys, so
// ${body.error.message} resolves without a path evaluator. Only scalars
// land in the map — a template substituting a whole object would just
// produce noise.
func flattenJSON(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flattenJSON(prefix+"."+k, t[k], out)
		}
	case string:
		out[prefix] = t
	case float64:
		out[prefix] = strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		out[prefix] = strconv.FormatBool(t)
	}
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
