package action

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestResolveExecSubstitutesInputs(t *testing.T) {
	a := &cfg.Action{
		Run: []string{"kubectl", "delete", "-n", "${inputs.namespace}", "pod", "${inputs.name}"},
		Inputs: map[string]*cfg.Parameter{
			"namespace": {Required: true},
			"name":      {Required: true},
		},
	}
	r, err := Resolve(a, Inputs{"namespace": "default", "name": "nginx-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := "kubectl delete -n default pod nginx-1"
	if got := strings.Join(r.Argv, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// TestResolveLeavesShellExpansionsAlone pins a bug that shipped in the
// kube example: `${PAGER:-less}` is POSIX default-value syntax the shell
// must receive intact. An earlier substituter treated every ${...} as
// its own and produced `| -R`, a command that fails somewhere far from
// the cause.
func TestResolveLeavesShellExpansionsAlone(t *testing.T) {
	a := &cfg.Action{
		Run: []string{"sh", "-c", "kubectl describe pod ${inputs.name} | ${PAGER:-less} -R"},
		Inputs: map[string]*cfg.Parameter{
			"name": {Required: true},
		},
	}
	r, err := Resolve(a, Inputs{"name": "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Argv[2], "${PAGER:-less}") {
		t.Errorf("shell expansion was eaten: %q", r.Argv[2])
	}
	if !strings.Contains(r.Argv[2], "pod nginx ") {
		t.Errorf("input was not substituted: %q", r.Argv[2])
	}
}

func TestResolveEnvFromEnvironment(t *testing.T) {
	t.Setenv("TEST_ACTION_HOST", "https://example.test")
	a := &cfg.Action{
		Type:   "http",
		URL:    "${env.TEST_ACTION_HOST}/apps/${inputs.app}/sync",
		Inputs: map[string]*cfg.Parameter{"app": {Required: true}},
	}
	r, err := Resolve(a, Inputs{"app": "web"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://example.test/apps/web/sync"; r.URL != want {
		t.Errorf("url = %q, want %q", r.URL, want)
	}
	if r.Method != http.MethodPost {
		t.Errorf("http actions should default to POST, got %q", r.Method)
	}
}

func TestResolveAppliesInputDefaults(t *testing.T) {
	a := &cfg.Action{
		Run:    []string{"echo", "${inputs.mode}"},
		Inputs: map[string]*cfg.Parameter{"mode": {Default: "safe"}},
	}
	r, err := Resolve(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Argv[1] != "safe" {
		t.Errorf("default not applied: %q", r.Argv[1])
	}
}

func TestFinishVerdictDefaults(t *testing.T) {
	exec := &cfg.Action{Run: []string{"true"}}
	r, _ := Resolve(exec, nil)
	if !r.Finish(0, "").OK {
		t.Error("exit 0 should be OK")
	}
	if r.Finish(1, "").OK {
		t.Error("exit 1 should not be OK")
	}

	httpAct := &cfg.Action{Type: "http", URL: "http://x"}
	hr, _ := Resolve(httpAct, nil)
	if !hr.Finish(204, "").OK {
		t.Error("204 should be OK")
	}
	if hr.Finish(403, "").OK {
		t.Error("403 should not be OK")
	}
}

// TestFinishSuccessExpression covers the argocd case: a 409 means "the
// sync you asked for is already running", which is not a failure worth
// painting the badge red.
func TestFinishSuccessExpression(t *testing.T) {
	a := &cfg.Action{
		Type:    "http",
		URL:     "http://x",
		Success: "code < 400 or code == 409",
	}
	r, _ := Resolve(a, nil)
	if !r.Finish(409, "").OK {
		t.Error("409 should be OK under the success expression")
	}
	if r.Finish(500, "").OK {
		t.Error("500 should still fail")
	}
}

// A broken success: expression must not take the action down with it —
// the expression refines a verdict we can already make.
func TestFinishBadSuccessExpressionFallsBack(t *testing.T) {
	a := &cfg.Action{Type: "http", URL: "http://x", Success: "this is not ( valid"}
	r, _ := Resolve(a, nil)
	if !r.Finish(200, "").OK {
		t.Error("a malformed success: should fall back to the per-kind default")
	}
}

func TestFinishErrorMessagePlucksJSONBody(t *testing.T) {
	a := &cfg.Action{
		Type:         "http",
		URL:          "http://x",
		ErrorMessage: "${body.message}",
	}
	r, _ := Resolve(a, nil)
	res := r.Finish(403, `{"message":"permission denied: applications, sync","code":7}`)
	if res.OK {
		t.Fatal("403 should not be OK")
	}
	if res.Summary != "permission denied: applications, sync" {
		t.Errorf("summary = %q, want the plucked body message", res.Summary)
	}
	// The raw body still survives as the console body — plucking changes
	// the head line, it doesn't discard evidence.
	if !strings.Contains(res.Output, "code") {
		t.Errorf("raw body should be preserved in Output, got %q", res.Output)
	}
}

func TestFinishMessageSubstitutesInputs(t *testing.T) {
	a := &cfg.Action{
		Run:     []string{"echo"},
		Inputs:  map[string]*cfg.Parameter{"app": {Required: true}},
		Message: "synced ${inputs.app}",
	}
	r, _ := Resolve(a, Inputs{"app": "web"})
	if got := r.Finish(0, "").Summary; got != "synced web" {
		t.Errorf("summary = %q, want %q", got, "synced web")
	}
}

// defaultSummary for a failing exec falls back to the last line of
// output, which is where CLIs put the reason.
func TestDefaultSummaryUsesLastOutputLine(t *testing.T) {
	a := &cfg.Action{Run: []string{"kubectl"}}
	r, _ := Resolve(a, nil)
	res := r.Finish(1, "some progress\nError from server: nope\n")
	if res.Summary != "Error from server: nope" {
		t.Errorf("summary = %q", res.Summary)
	}
}

func TestDoRoundTrip(t *testing.T) {
	var gotMethod, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(202)
		w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()

	a := &cfg.Action{
		Type:    "http",
		Method:  "POST",
		URL:     srv.URL + "/sync",
		Headers: map[string]string{"Authorization": "Bearer ${inputs.token}"},
		Body:    `{"prune": ${inputs.prune}}`,
		Inputs: map[string]*cfg.Parameter{
			"token": {Required: true},
			"prune": {Type: "bool", Default: "false"},
		},
		Message: "queued",
	}
	r, err := Resolve(a, Inputs{"token": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	res := r.Do(context.Background())

	if gotMethod != "POST" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotAuth != "Bearer abc" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotBody != `{"prune": false}` {
		t.Errorf("body = %q", gotBody)
	}
	if !res.OK || res.Code != 202 || res.Summary != "queued" {
		t.Errorf("result = %+v", res)
	}
}
