package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSubstituteURLEscapesByPosition(t *testing.T) {
	vals := map[string]string{
		"${env.BASE}":    "https://h.test/api",
		"${params.repo}": "owner/my repo",
		"${params.q}":    "a b&c=d",
		"${params.id}":   "x?y#z",
		"${env.HOST}":    "h.test:8080",
	}
	fn := func(s string) string {
		if v, ok := vals[s]; ok {
			return v
		}
		return s
	}
	cases := []struct{ in, want string }{
		{"${env.BASE}/repos/${params.repo}", "https://h.test/api/repos/owner/my%20repo"},
		{"https://h.test/items/${params.id}", "https://h.test/items/x%3Fy%23z"},
		{"https://h.test/s?q=${params.q}&n=1", "https://h.test/s?q=a+b%26c%3Dd&n=1"},
		{"https://${env.HOST}/x/${params.id}", "https://h.test:8080/x/x%3Fy%23z"},
		{"https://h.test/${window.cursor}", "https://h.test/${window.cursor}"},
		{"https://h.test/plain", "https://h.test/plain"},
	}
	for _, c := range cases {
		if got := substituteURL(c.in, fn); got != c.want {
			t.Errorf("substituteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBindParamsTypesJSON(t *testing.T) {
	s := &Source{
		Type: "http",
		URL:  "https://h.test",
		Parameters: map[string]*Parameter{
			"limit":  {Type: "int", Default: "25"},
			"active": {Type: "bool", Default: "true"},
			"name":   {Type: "string", Default: "42"},
			"after":  {Type: "string"},
		},
		JSON: map[string]any{
			"limit":  "${params.limit}",
			"active": "${params.active}",
			"name":   "${params.name}",
			"label":  "n=${params.limit}",
			"after":  "${params.after}",
			"list":   []any{"${params.after}", "keep"},
		},
	}
	if err := s.BindParams(nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"limit":  float64(25),
		"active": true,
		"name":   "42",
		"label":  "n=25",
		"list":   []any{"keep"},
	}
	if !reflect.DeepEqual(s.JSON, want) {
		t.Errorf("json = %#v\nwant %#v", s.JSON, want)
	}
}

func TestQueryValueUnmarshal(t *testing.T) {
	var got struct {
		Query map[string]QueryValue `yaml:"query"`
	}
	src := "query: {a: x, n: 5, tags: [p, q], none: null}"
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]QueryValue{"a": {"x"}, "n": {"5"}, "tags": {"p", "q"}, "none": nil}
	if !reflect.DeepEqual(got.Query, want) {
		t.Errorf("query = %#v", got.Query)
	}
	if err := yaml.Unmarshal([]byte("query: {a: {b: c}}"), &got); err == nil {
		t.Error("a map value should be an error")
	}
}

func TestRequestErrors(t *testing.T) {
	cases := []struct{ name, data, want string }{
		{"two bodies", `
sources:
  issues: {type: http, url: "https://h", body: "{}", json: {a: 1}}`, "json and body are mutually exclusive"},
		{"query on exec", `
sources:
  issues: {type: exec, command: [echo], query: {a: b}}`, "issues.query: only applies to type: http"},
		{"owned in query", `
sources:
  issues:
    type: http
    url: "https://h"
    query: {offset: "5"}
    window: {offset_param: offset, limit_param: limit, search_param: q}`, `query: "offset" is set by window (offset_param)`},
		{"owned in url", `
sources:
  issues:
    type: http
    url: "https://h?limit=10"
    window: {offset_param: offset, limit_param: limit, search_param: q}`, `url: "limit" is set by window (limit_param)`},
		{"patch key owned without a body", `
sources:
  issues:
    type: http
    url: "https://h?before=x"
    window: {cursor: ts, search_param: q, older: {before: "${window.cursor}"}, newer: {after: "${window.cursor}"}}`, `"before" is set by window (older)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadData(t, c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// A key the window writes only some of the time (search, sort, filters)
// may carry a default in the url.
func TestSometimesWrittenKeysTakeDefaults(t *testing.T) {
	if _, err := loadData(t, `
sources:
  issues:
    type: http
    url: "https://h?q=tolkien&sort=title"
    window: {offset_param: offset, limit_param: limit, search_param: q, sort_param: sort, total_path: total}`); err != nil {
		t.Fatal(err)
	}
}

func TestPresetQueryMerges(t *testing.T) {
	s := mustLoadData(t, `
presets:
  p:
    type: http
    url: "https://h"
    query: {per_page: "100", state: open, drop: x}
sources:
  issues: {extends: p, query: {state: closed, drop: "", labels: [a, b]}}
`)
	want := map[string]QueryValue{"per_page": {"100"}, "state": {"closed"}, "labels": {"a", "b"}}
	if !reflect.DeepEqual(s.Query, want) {
		t.Errorf("query = %v, want %v", s.Query, want)
	}
}

// ${env.*} goes through the same walk as every other token: escaped by
// where it sits in the url, and typed out of json: when it's empty.
func TestSubstituteEnvUsesTheSameWalk(t *testing.T) {
	t.Setenv("TB_TEST_Q", "a b")
	t.Setenv("TB_TEST_EMPTY", "")
	c := &Config{Data: DataBlock{Sources: map[string]*Source{"s": {
		Type: "http",
		URL:  "https://h/x?q=${env.TB_TEST_Q}",
		JSON: map[string]any{"q": "${env.TB_TEST_Q}", "gone": "${env.TB_TEST_EMPTY}"},
	}}}}
	c.SubstituteEnv()
	s := c.Data.Sources["s"]
	if s.URL != "https://h/x?q=a+b" {
		t.Errorf("url = %q", s.URL)
	}
	if !reflect.DeepEqual(s.JSON, map[string]any{"q": "a b"}) {
		t.Errorf("json = %#v", s.JSON)
	}
}
