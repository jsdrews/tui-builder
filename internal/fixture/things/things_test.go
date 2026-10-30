package things

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"gopkg.in/yaml.v3"
)

func do(t *testing.T, s http.Handler, method, url, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+Token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: body isn't a JSON object: %q", method, url, rec.Body.String())
		}
	}
	return rec.Code, out
}

type operation struct {
	OperationID string         `yaml:"operationId"`
	Tags        []string       `yaml:"tags"`
	Security    []any          `yaml:"security"`
	Responses   map[string]any `yaml:"responses"`
}

type specDoc struct {
	// Path items hold operations by method, plus a `parameters:` list.
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Enum []string `yaml:"enum"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func spec(t *testing.T) specDoc {
	t.Helper()
	var d specDoc
	if err := yaml.Unmarshal(Spec, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// Every operation the spec documents is served, behind auth, and has the
// FastAPI trimmings: an operationId, tags, bearer security and a 422.
func TestEveryDocumentedOperationIsServed(t *testing.T) {
	d := spec(t)
	n := 0
	for path, item := range d.Paths {
		for method, node := range item {
			if method == "parameters" {
				continue
			}
			var op operation
			if err := node.Decode(&op); err != nil {
				t.Fatal(err)
			}
			n++
			name := strings.ToUpper(method) + " " + path
			if op.OperationID == "" || len(op.Tags) == 0 || len(op.Security) == 0 || op.Responses["422"] == nil {
				t.Errorf("%s: missing operationId, tags, security or a 422", name)
			}
			url := pathParam.ReplaceAllString(path, "1")
			body := ""
			if method == "post" || method == "patch" {
				body = `{"name":"x","owner_id":1}`
			}
			s := New()
			code, _ := do(t, s, strings.ToUpper(method), url, body)
			if code == http.StatusNotFound || code == http.StatusMethodNotAllowed || code >= 500 {
				t.Errorf("%s → %d", name, code)
			}
			req := httptest.NewRequest(strings.ToUpper(method), url, nil)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s without a token → %d, want 401", name, rec.Code)
			}
		}
	}
	if n != 8 {
		t.Errorf("spec has %d operations, want 8", n)
	}
}

func TestSpecServedWithoutAuth(t *testing.T) {
	for _, url := range []string{"/openapi.yaml", "/openapi.json"} {
		rec := httptest.NewRecorder()
		New().ServeHTTP(rec, httptest.NewRequest("GET", url, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "list_things_things_get") {
			t.Errorf("%s → %d", url, rec.Code)
		}
	}
}

func TestPaging(t *testing.T) {
	s := New()
	code, p := do(t, s, "GET", "/things?page=3&per_page=50", "")
	if code != 200 || p["total"] != float64(seedThings) || len(p["entries"].([]any)) != seedThings-100 {
		t.Fatalf("page 3 → %d %v entries of total %v", code, len(p["entries"].([]any)), p["total"])
	}
	if first := p["entries"].([]any)[0].(map[string]any); first["id"] != float64(101) {
		t.Errorf("page 3 starts at id %v, want 101", first["id"])
	}
	if code, _ := do(t, s, "GET", "/things?per_page=101", ""); code != 422 {
		t.Errorf("per_page=101 → %d, want 422", code)
	}
}

// The spec's ThingOrder lists status; the server refuses it with a 400
// and serves every other value.
func TestRefusedSort(t *testing.T) {
	enum := spec(t).Components.Schemas["ThingOrder"].Enum
	for _, o := range RefusedOrders {
		if !slices.Contains(enum, o) {
			t.Fatalf("%s isn't in ThingOrder; the refusal would be documented", o)
		}
	}
	s := New()
	for _, o := range enum {
		code, body := do(t, s, "GET", "/things?order_by="+o, "")
		want := 200
		if slices.Contains(RefusedOrders, o) {
			want = 400
		}
		if code != want {
			t.Errorf("order_by=%s → %d %v, want %d", o, code, body, want)
		}
	}
	_, p := do(t, s, "GET", "/things?order_by=-size&per_page=2", "")
	e := p["entries"].([]any)
	if e[0].(map[string]any)["size"].(float64) < e[1].(map[string]any)["size"].(float64) {
		t.Error("order_by=-size isn't descending")
	}
}

// The __icontains filters work but the spec doesn't mention them.
func TestUndocumentedFilters(t *testing.T) {
	if strings.Contains(string(Spec), "__icontains:") || strings.Contains(string(Spec), "name: name__icontains") {
		t.Fatal("the spec documents an __icontains filter; it's meant to be hidden")
	}
	s := New()
	for _, tc := range []struct {
		url  string
		want float64
	}{
		{"/things?name__icontains=THING-01", 10},             // thing-010 … thing-019
		{"/things?description__icontains=brook", 18},         // owner 2's 24, less the 6 with no description
		{"/things?status=failed&name__icontains=thing-1", 5}, // documented + undocumented
		{"/owners?name__icontains=a", 2},                     // Ada, Dana
	} {
		_, p := do(t, s, "GET", tc.url, "")
		if p["total"] != tc.want {
			t.Errorf("%s → total %v, want %v", tc.url, p["total"], tc.want)
		}
	}
}

func TestWrites(t *testing.T) {
	s := New()
	code, made := do(t, s, "POST", "/things", `{"name":"new","owner_id":2,"tags":["x"]}`)
	if code != 201 || made["status"] != "pending" {
		t.Fatalf("create → %d %v", code, made)
	}
	if code, _ := do(t, s, "POST", "/things", `{"name":"new"}`); code != 422 {
		t.Errorf("create without owner_id → %d, want 422", code)
	}
	id := "/things/121"
	if code, up := do(t, s, "PATCH", id, `{"status":"active","description":null}`); code != 200 || up["status"] != "active" || up["description"] != nil {
		t.Errorf("patch → %d %v", code, up)
	}
	if code, _ := do(t, s, "DELETE", id, ""); code != 204 {
		t.Errorf("delete → %d", code)
	}
	if code, _ := do(t, s, "GET", id, ""); code != 404 {
		t.Errorf("get after delete → %d, want 404", code)
	}
	if _, p := do(t, New(), "GET", "/things", ""); p["total"] != float64(seedThings) {
		t.Error("a write leaked into a fresh Server")
	}
}

// /events sends a thing's status as JSON at once, and wants the token.
func TestEvents(t *testing.T) {
	srv := httptest.NewServer(New())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/events"
	ctx := context.Background()
	if _, resp, err := websocket.Dial(ctx, url, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %v %v", resp, err)
	}
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + Token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]string
	if err := json.Unmarshal(b, &ev); err != nil || ev["name"] != "thing-001" || !slices.Contains(Statuses, ev["status"]) || ev["at"] == "" {
		t.Errorf("first event %s: %v", b, err)
	}
}
