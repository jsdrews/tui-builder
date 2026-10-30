package scaffold

import (
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/config"
)

func TestParseParam(t *testing.T) {
	for in, want := range map[string]Param{
		"ns=default":               {"ns", "default", "string"},
		"n=20:int":                 {"n", "20", "int"},
		"all=true:bool":            {"all", "true", "bool"},
		"every=30s:duration":       {"every", "30s", "duration"},
		"url=http://x:8080":        {"url", "http://x:8080", "string"},
		"tag=v1:latest":            {"tag", "v1:latest", "string"},
		"url=http://x:8080:string": {"url", "http://x:8080", "string"},
	} {
		got, err := ParseParam(in)
		if err != nil || got != want {
			t.Errorf("ParseParam(%s) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"ns":             "NAME=VALUE",
		"ns=":            "NAME=VALUE",
		"1ns=x":          "isn't a parameter name",
		"x=:int":         "value is empty",
		"all=maybe:bool": "true or false",
	} {
		if _, err := ParseParam(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseParam(%s) err %v, want %q", in, err, want)
		}
	}
}

func TestTemplateArgv(t *testing.T) {
	argv := strings.Fields("kubectl get pods -n default --selector=app=web -o json")
	got, err := templateArgv(argv, []Param{{Name: "ns", Value: "default"}, {Name: "app", Value: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "kubectl get pods -n ${params.ns} --selector=app=${params.app} -o json"; strings.Join(got, " ") != want {
		t.Errorf("got %s\nwant %s", strings.Join(got, " "), want)
	}
	if strings.Join(argv, " ") != "kubectl get pods -n default --selector=app=web -o json" {
		t.Error("templateArgv changed its input")
	}
	for _, tc := range []struct {
		argv  string
		param Param
		want  string
	}{
		{"tool a a", Param{Name: "x", Value: "a"}, "has \"a\" 2 times"},
		{"tool --a=b,b", Param{Name: "x", Value: "b"}, "more than once"},
		{"tool --x=ab --y=ab", Param{Name: "x", Value: "ab"}, "more than once"},
		{"tool get pods", Param{Name: "x", Value: "nodes"}, "has no \"nodes\""},
		{"pods get pods", Param{Name: "x", Value: "get"}, ""},
	} {
		_, err := templateArgv(strings.Fields(tc.argv), []Param{tc.param})
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v (the program itself isn't a candidate)", tc.argv, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s / %s: err %v, want %q", tc.argv, tc.param.Value, err, tc.want)
		}
	}
	if _, err := templateArgv([]string{"t", "a", "b"}, []Param{{Name: "x", Value: "a"}, {Name: "x", Value: "b"}}); err == nil {
		t.Error("the same --param name twice was accepted")
	}
}

func TestParseColumns(t *testing.T) {
	got, err := ParseColumns("metadata.name=Pod, status.phase ,spec.nodeName=Node")
	if err != nil || len(got) != 3 || got[0] != (ColumnSpec{"metadata.name", "Pod"}) || got[1] != (ColumnSpec{"status.phase", ""}) {
		t.Errorf("ParseColumns = %+v, %v", got, err)
	}
	for _, bad := range []string{"a,,b", "a,a", "=Title"} {
		if _, err := ParseColumns(bad); err == nil {
			t.Errorf("ParseColumns(%q) accepted", bad)
		}
	}
}

var podsSample = Output{Stdout: []byte(`{"items": [
	{"metadata": {"name": "api", "uid": "u1"}, "status": {"phase": "Running"}},
	{"metadata": {"name": "web", "uid": "u2"}, "status": {"phase": "Failed"}}
]}`)}

var podsArgv = strings.Fields("kubectl get pods -n default -o json")

// decode loads a written config and returns it, failing the test when it
// doesn't load.
func decode(t *testing.T, res Result) *config.Config {
	t.Helper()
	path := t.TempDir() + "/c.yaml"
	if err := writeFile(path, res.Config); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Config)
	}
	return c
}

func TestParamsWriteInputs(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: podsArgv, Params: []Param{{"ns", "default", "string"}}}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src := c.Data.Sources["pods"]
	if strings.Join(src.Command, " ") != "kubectl get pods -n ${params.ns} -o json" || src.Parameters["ns"].Default != "default" {
		t.Errorf("source: %v %+v", src.Command, src.Parameters)
	}
	in := c.TUI.Components["ns"]
	if in == nil || in.Type != "input" || in.Initial != "default" {
		t.Errorf("input field: %+v", in)
	}
	if b := c.TUI.Components["pods"].OnInput; b == nil || b.Bind["ns"] != "${input.ns}" {
		t.Errorf("on_input: %+v", b)
	}

	// A bool is a toggle; --no-inputs writes the parameter alone.
	res, err = ExecConfig(ExecOptions{Argv: append(podsArgv, "--watch=false"), Params: []Param{{"watch", "false", "bool"}}}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	if c := decode(t, res); c.TUI.Components["watch"].Type != "toggle" || c.Data.Sources["pods"].Parameters["watch"].Type != "bool" {
		t.Errorf("bool param:\n%s", res.Config)
	}
	res, err = ExecConfig(ExecOptions{Argv: podsArgv, Params: []Param{{"ns", "default", "string"}}, NoInputs: true}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	if c := decode(t, res); c.TUI.Components["ns"] != nil || c.TUI.Components["pods"].OnInput != nil || c.Data.Sources["pods"].Parameters["ns"].Default != "default" {
		t.Errorf("--no-inputs:\n%s", res.Config)
	}
}

// A parameter named like the source gets an input with another name.
func TestInputNameClash(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: podsArgv, Params: []Param{{"pods", "pods", "string"}}}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	if c := decode(t, res); c.TUI.Components["pods_input"] == nil {
		t.Errorf("no pods_input:\n%s", res.Config)
	}
}

func TestNameTitleRefresh(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: podsArgv, Name: "my_pods", Title: "My pods", Refresh: 30 * time.Second}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	if c.Data.Sources["my_pods"] == nil || c.TUI.Components["my_pods"].Title != "My pods" || c.App.Title != "My pods" || c.Data.Sources["my_pods"].Refresh != "30s" {
		t.Errorf("name / title / refresh:\n%s", res.Config)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "name: my_pods (--name)") {
		t.Errorf("summary %q doesn't credit --name", res.Summary)
	}
	if _, err := ExecConfig(ExecOptions{Argv: podsArgv, Name: "my pods"}, podsSample); err == nil {
		t.Error("--name with a space was accepted")
	}
}

func TestColumnsFlag(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: podsArgv, Columns: []ColumnSpec{{"status.phase", ""}, {"metadata.name", "Pod"}, {"spec.nodeName", ""}}}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	cols := decode(t, res).TUI.Components["pods"].Columns
	if len(cols) != 3 || cols[0].Title != "Phase" || cols[1].Title != "Pod" || !cols[1].Bold || len(cols[0].ColorRules) == 0 {
		t.Errorf("--columns keeps order, titles and style:\n%s", res.Config)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "spec.nodeName") {
		t.Errorf("warnings %q, want one for spec.nodeName", res.Warnings)
	}
	if _, err := ExecConfig(ExecOptions{Argv: podsArgv, Columns: []ColumnSpec{{"metadata.name", ""}, {"spec.name", ""}}}, podsSample); err == nil {
		t.Error("two columns titled Name were accepted")
	}
}

func TestComponentFlag(t *testing.T) {
	for kind, want := range map[string]string{"list": "list", "inspector": "inspector", "table": "table"} {
		res, err := ExecConfig(ExecOptions{Argv: podsArgv, Component: kind}, podsSample)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		comp := decode(t, res).TUI.Components["pods"]
		if comp.Type != want {
			t.Errorf("--component %s wrote %s", kind, comp.Type)
		}
		if kind == "list" && comp.Item != "metadata.name" {
			t.Errorf("list item %q, want the identity", comp.Item)
		}
	}
	for kind, want := range map[string]string{"eventlog": "windowed source", "logview": "printed JSON", "tree": "want one of"} {
		if _, err := ExecConfig(ExecOptions{Argv: podsArgv, Component: kind}, podsSample); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("--component %s: %v, want %q", kind, err, want)
		}
	}
	noID := Output{Stdout: []byte(`[{"x": 1}, {"x": 2}]`)}
	if _, err := ExecConfig(ExecOptions{Argv: []string{"t"}, Component: "list"}, noID); err == nil || !strings.Contains(err.Error(), "--columns PATH") {
		t.Errorf("a list with no identity: %v", err)
	}
	// A single object with --columns is a one-row table.
	one := Output{Stdout: []byte(`{"login": "octocat", "id": 1}`)}
	res, err := ExecConfig(ExecOptions{Argv: []string{"gh", "api", "user"}, Columns: []ColumnSpec{{"login", ""}}}, one)
	if err != nil || decode(t, res).TUI.Components["user"].Type != "table" {
		t.Errorf("single object with --columns: %v\n%s", err, res.Config)
	}
}

func TestNoRunWithColumns(t *testing.T) {
	res, err := ExecConfigNoRun(ExecOptions{Argv: podsArgv, Columns: []ColumnSpec{{"metadata.name", "Pod"}}})
	if err != nil {
		t.Fatal(err)
	}
	if comp := decode(t, res).TUI.Components["pods"]; comp.Type != "table" || len(comp.Columns) != 1 {
		t.Errorf("--no-run --columns:\n%s", res.Config)
	}
	if _, err := ExecConfigNoRun(ExecOptions{Argv: podsArgv, Component: "table"}); err == nil || !strings.Contains(err.Error(), "needs --columns") {
		t.Errorf("--no-run --component table without columns: %v", err)
	}
}

// The summary credits each choice to a flag or to inference.
func TestSummary(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: podsArgv}, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(res.Summary, "\n")
	for _, want := range []string{"name: pods (inferred)", "rows: root: items (inferred)", "component: table (inferred)", "mark_key: metadata.uid (inferred)", "columns: 3, ranked (inferred)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(res.Config, &doc); err != nil {
		t.Fatal(err)
	}
}

func writeFile(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }
