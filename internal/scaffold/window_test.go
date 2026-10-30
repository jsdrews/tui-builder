package scaffold

import (
	"strings"
	"testing"
	"time"
)

var pageSample = Output{Stdout: []byte(`{"total": 120, "items": [
	{"id": 1, "title": "Dune", "author": "herbert", "year": 1965},
	{"id": 2, "title": "Emma", "author": "austen", "year": 1815}
]}`)}

func marks(t *testing.T, specs ...string) []WindowMark {
	t.Helper()
	var out []WindowMark
	for _, s := range specs {
		m, err := ParseWindow(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

var booksArgv = strings.Fields("books search --offset 0 --limit 50 --q dune --author=herbert --sort title --dir asc")

func TestSeekableTable(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: booksArgv, Window: WindowOptions{Marks: marks(t, "offset=0", "limit=50", "search=dune")}}, pageSample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, comp := c.Data.Sources["books"], c.TUI.Components["books"]
	if got := strings.Join(src.Command, " "); got != "books search --offset ${window.offset} --limit ${window.limit} --q ${window.search} --author=herbert --sort title --dir asc" {
		t.Errorf("command: %s", got)
	}
	if src.Window == nil || src.Window.TotalPath != "total" || src.Root != "items" {
		t.Errorf("window: %+v root %q\n%s", src.Window, src.Root, res.Config)
	}
	if comp.Type != "table" || !comp.Filterable || comp.Markable {
		t.Errorf("a seekable table is filterable (search marked) and not markable:\n%s", res.Config)
	}
	for _, col := range comp.Columns {
		if col.Sortable != nil && *col.Sortable {
			t.Errorf("column %s sortable without sort tokens", col.Title)
		}
	}
}

func TestSeekableSortAndFilters(t *testing.T) {
	res, err := ExecConfig(ExecOptions{Argv: booksArgv, Window: WindowOptions{Marks: marks(t,
		"offset=0", "limit=50", "filter.author=herbert", "sort=title", "sort_dir=asc")}}, pageSample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, comp := c.Data.Sources["books"], c.TUI.Components["books"]
	if !strings.Contains(strings.Join(src.Command, " "), "--author=${window.filters.author} --sort ${window.sort} --dir ${window.sort_dir}") {
		t.Errorf("command: %v", src.Command)
	}
	if src.Window.Filters["Author"] != "author" || !comp.Filterable {
		t.Errorf("filters: %+v\n%s", src.Window.Filters, res.Config)
	}
	if !strings.Contains(string(res.Config), "sortable: true") {
		t.Errorf("sort and sort_dir marked, but nothing sorts:\n%s", res.Config)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "column's title in ${window.sort}") {
		t.Errorf("summary doesn't say what ${window.sort} carries: %q", res.Summary)
	}
}

func TestWindowRefusals(t *testing.T) {
	array := Output{Stdout: []byte(`[{"id":1,"title":"a"},{"id":2,"title":"b"}]`)}
	for _, tc := range []struct {
		argv  string
		marks []string
		out   Output
		opts  func(*ExecOptions)
		want  string
	}{
		{"t --offset 0 --limit 5", []string{"offset=0", "limit=5"}, array, nil, "needs a total"},
		{"t --offset 0", []string{"offset=0"}, pageSample, nil, "${window.offset} and ${window.limit}"},
		{"t --offset 0 --limit 5 --after x --dir older", []string{"offset=0", "limit=5", "cursor=x", "dir=older"}, pageSample, nil, "not both"},
		{"t --after x --limit 5", []string{"cursor=x", "limit=5"}, pageSample, nil, "anchored window needs"},
		{"t --offset 0 --limit 5", []string{"offset=0", "limit=5"}, pageSample, func(o *ExecOptions) { o.Window.TotalPath = "nope" }, "no number at nope"},
		{"t --offset 0 --limit 5", []string{"offset=0", "limit=5"}, pageSample, func(o *ExecOptions) { o.Refresh = time.Second }, "isn't polled"},
		{"t --offset 0 --limit 5", []string{"offset=0", "limit=5"}, pageSample, func(o *ExecOptions) { o.Window.Growing = true }, "only an eventlog"},
		{"t --offset 0 --limit 5", []string{"offset=0", "limit=5"}, pageSample, func(o *ExecOptions) { o.Component = "list" }, "paged by a table or an eventlog"},
		{"t --offset 0 --limit 5 --a z", []string{"offset=0", "limit=5", "filter.zzz=z"}, pageSample, nil, "no column shows zzz"},
		{"t --offset 0 --limit 5", []string{"offset=9"}, pageSample, nil, `has no "9"`},
		{"t", nil, pageSample, func(o *ExecOptions) { o.Window.TotalPath = "total" }, "no window"},
	} {
		opts := ExecOptions{Argv: strings.Fields(tc.argv), Window: WindowOptions{Marks: marks(t, tc.marks...)}}
		if tc.opts != nil {
			tc.opts(&opts)
		}
		if _, err := ExecConfig(opts, tc.out); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: %v, want %q", tc.argv, tc.marks, err, tc.want)
		}
	}
	if _, err := ExecConfigNoRun(ExecOptions{Argv: []string{"t", "${window.offset}", "${window.limit}"}}); err == nil || !strings.Contains(err.Error(), "from a sample") {
		t.Errorf("--no-run with a window: %v", err)
	}
	if _, err := ExecConfig(ExecOptions{Argv: []string{"t", "${window.offset}", "${window.limit}"}}, Output{Stream: true, Stdout: []byte("x\n")}); err == nil || !strings.Contains(err.Error(), "isn't paged") {
		t.Errorf("a windowed stream: %v", err)
	}
}

var logSample = Output{Stdout: []byte(`[
	{"id": "e3", "ts": "2026-10-29T10:00:03Z", "msg": "ERROR disk full on /var", "host": "a"},
	{"id": "e2", "ts": "2026-10-29T10:00:02Z", "msg": "WARN slow request", "host": "b"},
	{"id": "e1", "ts": "2026-10-29T10:00:01Z", "msg": "started", "host": "a"}
]`)}

// Tokens written straight into the command; an anchored window is an
// eventlog, keyed, with its text, mark and level colours.
func TestAnchoredEventlog(t *testing.T) {
	argv := []string{"logs", "--after=${window.cursor}", "--dir", "${window.dir}", "--limit", "${window.limit}", "--grep=${window.search}"}
	res, err := ExecConfig(ExecOptions{Argv: argv}, logSample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, ev := c.Data.Sources["logs"], c.TUI.Components["logs"]
	if src.Window.Cursor != "ts" || ev.Type != "eventlog" || ev.Key[0] != "id" || ev.Text[0] != "msg" || ev.Mark[0] != "ts" || !ev.Filterable {
		t.Errorf("anchored eventlog:\n%s", res.Config)
	}
	if len(ev.ColorRules) != 2 || ev.ColorRules[0].Color != "theme:error" {
		t.Errorf("level colours: %+v", ev.ColorRules)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "next step:") {
		t.Errorf("no jump-to next step in %q", res.Summary)
	}
	got := strings.Join(SampleArgv(argv), " ")
	if got != "logs --dir older --limit 50" {
		t.Errorf("SampleArgv = %s", got)
	}
}

func TestAnchoredSpanTable(t *testing.T) {
	argv := []string{"logs", "--after=${window.cursor}", "--dir", "${window.dir}", "--limit", "${window.limit}"}
	res, err := ExecConfig(ExecOptions{Argv: argv, Component: "table"}, logSample)
	if err != nil {
		t.Fatal(err)
	}
	comp := decode(t, res).TUI.Components["logs"]
	if comp.Type != "table" || len(comp.MarkKey) == 0 || strings.Contains(string(res.Config), "sortable") {
		t.Errorf("span table:\n%s", res.Config)
	}
}

// A seekable sample that reads as events is an eventlog; --growing
// follows it.
func TestSeekableEventsGrowing(t *testing.T) {
	sample := Output{Stdout: []byte(`{"count": 3, "results": [
		{"id": 1, "created": "2026-10-29T10:00:01Z", "stdout": "TASK [setup]", "event": "playbook_on_task_start"},
		{"id": 2, "created": "2026-10-29T10:00:02Z", "stdout": "ok: [web]", "event": "runner_on_ok"}
	]}`)}
	argv := strings.Fields("awx-events --offset 0 --limit 50")
	res, err := ExecConfig(ExecOptions{Argv: argv, Window: WindowOptions{Marks: marks(t, "offset=0", "limit=50"), Growing: true, FollowEvery: 5 * time.Second}}, sample)
	if err != nil {
		t.Fatal(err)
	}
	c := decode(t, res)
	src, ev := c.Data.Sources["awx_events"], c.TUI.Components["awx_events"]
	if ev.Type != "eventlog" || ev.Text[0] != "stdout" || ev.Key[0] != "id" || src.Window.TotalPath != "count" || src.Window.Growing == nil || src.Window.FollowEvery != "5s" {
		t.Errorf("growing eventlog:\n%s", res.Config)
	}
	if _, err := ExecConfig(ExecOptions{Argv: argv, Window: WindowOptions{Marks: marks(t, "offset=0", "limit=50"), FollowEvery: time.Second}}, sample); err == nil || !strings.Contains(err.Error(), "needs --growing") {
		t.Errorf("--follow-every alone: %v", err)
	}
}

func TestParseWindow(t *testing.T) {
	if m, err := ParseWindow("filter.author=x"); err != nil || m.Token != "filters.author" {
		t.Errorf("filter mark: %+v %v", m, err)
	}
	for _, bad := range []string{"offset", "page=1", "filter.=x", "filter.a-b=x"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) accepted", bad)
		}
	}
}

// A --param and a --window in one command keep their own namespaces.
func TestParamsAndWindowTogether(t *testing.T) {
	argv := strings.Fields("books --shelf scifi --offset 0 --limit 50")
	res, err := ExecConfig(ExecOptions{Argv: argv, Params: []Param{{"shelf", "scifi", "string"}}, Window: WindowOptions{Marks: marks(t, "offset=0", "limit=50")}}, pageSample)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(decode(t, res).Data.Sources["books"].Command, " "); got != "books --shelf ${params.shelf} --offset ${window.offset} --limit ${window.limit}" {
		t.Errorf("command: %s", got)
	}
}
