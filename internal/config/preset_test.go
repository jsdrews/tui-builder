package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// loadData loads a config whose data block is data, with one table
// bound to the source named "issues".
func loadData(t *testing.T, data string) (*Config, error) {
	t.Helper()
	src := "data:\n" + indent(data) + `
tui:
  components:
    issues:
      type: table
      source: issues
      columns: [{title: Title, value: title}]
  screen:
    title: T
    layout: {component: issues}
`
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func indent(s string) string {
	lines := strings.Split(strings.Trim(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func mustLoadData(t *testing.T, data string) *Source {
	t.Helper()
	c, err := loadData(t, data)
	if err != nil {
		t.Fatal(err)
	}
	return c.Data.Sources["issues"]
}

func TestPresetMerge(t *testing.T) {
	s := mustLoadData(t, `
presets:
  github:
    type: http
    url: https://api.github.com
    timeout: 20s
    headers: {Accept: application/vnd.github+json, X-Drop: "1"}
    parameters:
      owner: {type: string, default: jsdrews}
sources:
  issues:
    extends: github
    url: /repos/${params.owner}/${params.repo}/issues
    headers: {X-Drop: "", X-Extra: "2"}
    parameters:
      repo: {type: string, default: tui-builder}
`)
	if s.Type != "http" {
		t.Errorf("type = %q, want http inherited", s.Type)
	}
	if want := "https://api.github.com/repos/${params.owner}/${params.repo}/issues"; s.URL != want {
		t.Errorf("url = %q, want %q", s.URL, want)
	}
	if s.Timeout != "20s" {
		t.Errorf("timeout = %q, want 20s inherited", s.Timeout)
	}
	if want := map[string]string{"Accept": "application/vnd.github+json", "X-Extra": "2"}; !reflect.DeepEqual(s.Headers, want) {
		t.Errorf("headers = %v, want %v", s.Headers, want)
	}
	if _, ok := s.Parameters["owner"]; !ok {
		t.Errorf("parameters = %v, want the preset's owner too", s.Parameters)
	}
	if _, ok := s.Parameters["repo"]; !ok {
		t.Errorf("parameters = %v, want the source's repo", s.Parameters)
	}
	if !reflect.DeepEqual(s.ExtendsChain, []string{"github"}) {
		t.Errorf("chain = %v", s.ExtendsChain)
	}
	if s.Inherited["timeout"] != "github" || s.Inherited["type"] != "github" {
		t.Errorf("inherited = %v, want timeout and type from github", s.Inherited)
	}
	if _, ok := s.Inherited["url"]; ok {
		t.Errorf("inherited = %v: a joined url is the source's own", s.Inherited)
	}
}

func TestPresetSharedNotAliased(t *testing.T) {
	c, err := loadData(t, `
presets:
  base: {type: http, url: "https://h", headers: {A: "1"}}
sources:
  issues: {extends: base}
  other: {extends: base}
`)
	if err != nil {
		t.Fatal(err)
	}
	c.Data.Sources["issues"].Headers["A"] = "changed"
	if got := c.Data.Sources["other"].Headers["A"]; got != "1" {
		t.Errorf("other's header = %q: sources share the preset's map", got)
	}
}

func TestPresetChainAndUnset(t *testing.T) {
	s := mustLoadData(t, `
presets:
  org:
    type: http
    url: https://api.example.com/v1
    headers: {Authorization: "Bearer ${env.T}"}
    paginate: {strategy: link, next_path: next}
  api:
    extends: org
    timeout: 5s
sources:
  issues:
    extends: api
    url: issues
    unset: [paginate]
`)
	if s.URL != "https://api.example.com/v1/issues" {
		t.Errorf("url = %q", s.URL)
	}
	if s.Paginate != nil {
		t.Errorf("paginate = %+v, want it unset", s.Paginate)
	}
	if !reflect.DeepEqual(s.ExtendsChain, []string{"api", "org"}) {
		t.Errorf("chain = %v", s.ExtendsChain)
	}
	if s.Inherited["headers"] != "org" || s.Inherited["timeout"] != "api" {
		t.Errorf("inherited = %v, want headers from org and timeout from api", s.Inherited)
	}
}

func TestPresetStructuredBlockReplacedWhole(t *testing.T) {
	s := mustLoadData(t, `
presets:
  p: {type: http, url: "https://h", paginate: {strategy: link, next_path: next, max_pages: 3}}
sources:
  issues: {extends: p, paginate: {strategy: link, next_path: links.next}}
`)
	if s.Paginate.NextPath != "links.next" || s.Paginate.MaxPages != 0 {
		t.Errorf("paginate = %+v, want the source's whole block", s.Paginate)
	}
}

func TestJoinPresetURL(t *testing.T) {
	cases := []struct{ base, rel, want string }{
		{"https://h/v1", "/users", "https://h/v1/users"},
		{"https://h/v1/", "users", "https://h/v1/users"},
		{"https://h/v1", "", "https://h/v1"},
		{"https://h/v1", "https://other/x", "https://other/x"},
		{"https://h/v1", "${env.BASE}/x", "${env.BASE}/x"},
		{"https://h/v1?a=1&b=2", "/u?b=3&c=4", "https://h/v1/u?a=1&b=3&c=4"},
		{"https://h/v1?a=1", "?a=2", "https://h/v1?a=2"},
		{"", "/u", "/u"},
	}
	for _, c := range cases {
		if got := joinPresetURL(c.base, c.rel); got != c.want {
			t.Errorf("join(%q, %q) = %q, want %q", c.base, c.rel, got, c.want)
		}
	}
}

func TestPresetErrors(t *testing.T) {
	cases := []struct{ name, data, want string }{
		{"unknown extends", `
presets:
  github: {type: http, url: "https://h"}
sources:
  issues: {extends: githb}`, `extends "githb", which isn't a preset under data.presets (did you mean github?)`},
		{"cycle", `
presets:
  a: {extends: b}
  b: {extends: a}
sources:
  issues: {extends: a}`, "extends cycle a → b → a"},
		{"type mismatch", `
presets:
  p: {type: websocket, url: "wss://h"}
sources:
  issues: {extends: p, type: http}`, `type "http" doesn't match preset p's "websocket"`},
		{"pipe in preset", `
presets:
  p: {type: http, url: "https://h", pipe: [{type: filter, where: "true"}]}
sources:
  issues: {extends: p}`, "pipe: isn't allowed in a preset"},
		{"operator preset", `
presets:
  p: {type: filter}
sources:
  issues: {type: static, data: []}`, `type "filter" isn't a leaf kind`},
		{"unknown key", `
presets:
  p: {type: http, url: "https://h", timout: 5s}
sources:
  issues: {extends: p}`, `data.presets.p: unknown field "timout" (did you mean timeout?)`},
		{"unset unknown", `
presets:
  p: {type: http, url: "https://h"}
sources:
  issues: {extends: p, unset: [paginte]}`, `data.sources.issues.unset: unknown field "paginte" (did you mean paginate?)`},
		{"unset without extends", `
sources:
  issues: {type: static, data: [], unset: [root]}`, "unset: needs extends:"},
		{"name clash", `
presets:
  issues: {type: http, url: "https://h"}
sources:
  issues: {type: static, data: []}`, "presets and sources share one namespace"},
		{"bound to a preset", `
presets:
  p: {type: http, url: "https://h"}
sources:
  issues: {type: passthrough, from: p}`, `upstream "p" is a preset`},
		{"undeclared param after merge", `
presets:
  p: {type: http, url: "https://h/${params.region}"}
sources:
  issues: {extends: p}`, "region"},
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

func TestComponentBoundToPreset(t *testing.T) {
	_, err := loadData(t, `
presets:
  issues: {type: http, url: "https://h"}
sources:
  other: {extends: issues}`)
	if err == nil || !strings.Contains(err.Error(), `"issues" is a preset`) {
		t.Errorf("err = %v, want the preset hint", err)
	}
}
