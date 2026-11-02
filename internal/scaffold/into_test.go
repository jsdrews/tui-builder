package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// added is a result to splice: the pods table.
func added(t *testing.T, opts ...func(*ExecOptions)) Result {
	t.Helper()
	o := ExecOptions{Argv: podsArgv}
	for _, f := range opts {
		f(&o)
	}
	res, err := ExecConfig(o, podsSample)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func splice(t *testing.T, old string, res Result, place string) (Spliced, error) {
	t.Helper()
	return SpliceInto([]byte(old), res, place, t.TempDir())
}

// diffLines is the lines the splice added and removed, as +/- lines.
func diffLines(sp Spliced) (plus, minus []string) {
	for _, l := range strings.Split(UnifiedDiff("c", sp.Old, sp.New), "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			plus = append(plus, l[1:])
		case strings.HasPrefix(l, "-"):
			minus = append(minus, l[1:])
		}
	}
	return plus, minus
}

const lone = `# My app.
app:
  title: Mine   # keep this comment

data:
  sources:
    things: {type: static, data: [{a: 1}]}

    # others: {type: static, data: []}

tui:
  components:
    things_list:
      type: list
      source: things
      item: a
  screen:
    layout:
      component: things_list
`

func TestSpliceLoneComponent(t *testing.T) {
	sp, err := splice(t, lone, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	plus, minus := diffLines(sp)
	if len(minus) != 1 || strings.TrimSpace(minus[0]) != "component: things_list" {
		t.Errorf("removed %q; only the lone component line should go", minus)
	}
	for _, want := range []string{"    pods:", "      vstack:", "        - component: things_list", "        - component: pods"} {
		if !strings.Contains(strings.Join(plus, "\n"), want) {
			t.Errorf("added lines lack %q:\n%s", want, strings.Join(plus, "\n"))
		}
	}
	if !strings.Contains(string(sp.New), "title: Mine   # keep this comment") || !strings.Contains(string(sp.New), "    # others:") {
		t.Error("comments were lost")
	}
	// The source goes after the commented-out entry, keeping the blank
	// line spacing.
	out := string(sp.New)
	if strings.Index(out, "# others") > strings.Index(out, "    pods:\n") {
		t.Errorf("the source went above the commented-out entry:\n%s", out)
	}
	if sp.Summary != "added source `pods`, table `pods` to the screen" {
		t.Errorf("summary %q", sp.Summary)
	}
}

func TestSpliceFlowLayoutAndVstack(t *testing.T) {
	flow := strings.Replace(lone, "    layout:\n      component: things_list\n", "    layout: {component: things_list}\n", 1)
	sp, err := splice(t, flow, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, minus := diffLines(sp); len(minus) != 1 {
		t.Errorf("a flow layout should change one line, changed %q", minus)
	}
	stacked := strings.Replace(lone, "    layout:\n      component: things_list\n", "    layout:\n      vstack:\n        - {fixed: 3, component: things_list}\n", 1)
	sp, err = splice(t, stacked, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, minus := diffLines(sp); len(minus) != 0 {
		t.Errorf("appending to a vstack shouldn't change a line: %q", minus)
	}
	if !strings.Contains(string(sp.New), "        - {fixed: 3, component: things_list}\n        - component: pods") {
		t.Errorf("vstack append:\n%s", sp.New)
	}
}

func TestSpliceTabs(t *testing.T) {
	tabs := `tui:
  components:
    a: {type: list, items: [x]}
  screen:
    tabs:
      - label: A
        layout: {component: a}
`
	sp, err := splice(t, tabs, added(t, func(o *ExecOptions) { o.Title = "My pods" }), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sp.New), "      - label: My pods\n        layout:\n          component: pods") || !strings.Contains(sp.Summary, `as tab "My pods"`) {
		t.Errorf("tab:\n%s\n%s", sp.New, sp.Summary)
	}
	// A config with no data: block gets one, before tui:.
	if !strings.HasPrefix(string(sp.New), "data:\n  sources:\n") {
		t.Errorf("no data: block made:\n%s", sp.New)
	}
	if _, err := splice(t, tabs, added(t, func(o *ExecOptions) { o.Title = "A" }), ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a duplicate tab label: %v", err)
	}
	if _, err := splice(t, tabs, added(t), "stack"); err == nil || !strings.Contains(err.Error(), "use --place tab") {
		t.Errorf("--place stack on tabs: %v", err)
	}
}

const multi = `tui:
  components:
    home:
      type: menu
      items:
        - {label: First, screen: first}
    first_list: {type: list, items: [x]}
  initial: home
  screens:
    home:
      layout: {component: home}
    first:
      layout: {component: first_list}
`

func TestSpliceScreenAndMenu(t *testing.T) {
	sp, err := splice(t, multi, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	out := string(sp.New)
	if !strings.Contains(out, "    pods:\n      title: Pods\n      layout:\n        component: pods") || !strings.Contains(out, "        - {label: Pods, screen: pods}") {
		t.Errorf("screen and menu item:\n%s", out)
	}
	if sp.Summary != "added source `pods`, table `pods` to screen `pods`, linked from menu `home`" {
		t.Errorf("summary %q", sp.Summary)
	}
	// No menu on the initial screen: onto its layout, with a note.
	noMenu := strings.Replace(multi, "  initial: home", "  initial: first", 1)
	sp, err = splice(t, noMenu, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sp.Summary, "to screen `first`") || len(sp.Notes) != 1 {
		t.Errorf("no menu: %q %q", sp.Summary, sp.Notes)
	}
	// --place screen without a menu: a screen nothing links to, noted.
	sp, err = splice(t, noMenu, added(t), "screen")
	if err != nil || !strings.Contains(sp.Notes[0], "nothing links to") {
		t.Errorf("--place screen, no menu: %v %q", err, sp.Notes)
	}
	if _, err := splice(t, lone, added(t), "screen"); err == nil || !strings.Contains(err.Error(), "doesn't turn it into several") {
		t.Errorf("--place screen on one screen: %v", err)
	}
}

func TestSpliceInputs(t *testing.T) {
	res := added(t, func(o *ExecOptions) { o.Params = []Param{{"ns", "default", "string"}} })
	sp, err := splice(t, lone, res, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"    ns:\n      type: input", "on_input:"} {
		if !strings.Contains(string(sp.New), want) {
			t.Errorf("lacks %q:\n%s", want, sp.New)
		}
	}
	if !strings.Contains(sp.Summary, "input fields ns") {
		t.Errorf("summary %q", sp.Summary)
	}
}

// A 4-space file gets 4-space entries.
func TestSpliceIndentStep(t *testing.T) {
	four := `data:
    sources:
        things: {type: static, data: [{a: 1}]}
tui:
    components:
        things_list: {type: list, source: things, item: a}
    screen:
        layout:
            component: things_list
`
	sp, err := splice(t, four, added(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sp.New), "        pods:\n            type: exec") || !strings.Contains(string(sp.New), "            vstack:\n                - component: things_list") {
		t.Errorf("4-space file:\n%s", sp.New)
	}
}

func TestSpliceRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ cfg, want string }{
		"crlf":        {strings.ReplaceAll(lone, "\n", "\r\n"), "CRLF"},
		"tabs":        {strings.Replace(lone, "  title", "\ttitle", 1), "tab"},
		"two docs":    {lone + "---\napp: {}\n", "more than one YAML document"},
		"flow parent": {strings.Replace(lone, "  sources:\n    things: {type: static, data: [{a: 1}]}\n", "  sources: {things: {type: static, data: [{a: 1}]}}\n", 1), "written inline"},
		"merge":       {strings.Replace(lone, "data:\n  sources:\n", "base: &b {x: {type: static, data: []}}\ndata:\n  sources:\n    <<: *b\n", 1), "<<:"},
		"keep chomp":  {strings.Replace(lone, "    things: {type: static, data: [{a: 1}]}\n\n    # others: {type: static, data: []}\n", "    things:\n      type: static\n      data: |+\n        x\n\n", 1), "|+"},
		"hstack":      {strings.Replace(lone, "    layout:\n      component: things_list\n", "    layout:\n      hstack:\n        - component: things_list\n", 1), "hstack or zstack"},
		"name clash":  {strings.Replace(lone, "things:", "pods:", 1), "source `pods` already exists — pass --name pods2"},
		"comp clash":  {strings.Replace(lone, "things_list:\n", "pods:\n", 1), "component `pods` already exists"},
	} {
		if _, err := splice(t, tc.cfg, added(t), ""); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// Every example config takes the pods table, or refuses with a reason;
// none is changed beyond the new entries.
func TestSpliceEveryExample(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	ok, refused := 0, map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		res := added(t, func(o *ExecOptions) { o.Name = "added_pods" })
		sp, err := SpliceInto(b, res, "", filepath.Dir(f))
		if err != nil {
			if strings.Contains(err.Error(), "bug in add exec") {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
			refused[firstWords(err.Error())]++
			if strings.Contains(err.Error(), "doesn't load") {
				t.Logf("%s: %v", filepath.Base(f), err)
			}
			continue
		}
		ok++
		if _, minus := diffLines(sp); len(minus) > 1 {
			t.Errorf("%s: changed %d existing lines: %q", filepath.Base(f), len(minus), minus)
		}
	}
	t.Logf("%d of %d examples took it; refused: %v", ok, len(files), refused)
	if ok < len(files)/2 {
		t.Errorf("only %d of %d examples took it", ok, len(files))
	}
}

func firstWords(s string) string {
	if i := strings.Index(s, ":"); i >= 0 && i < 12 {
		s = s[i+1:]
	}
	f := strings.Fields(s)
	return strings.Join(f[:min(5, len(f))], " ")
}

func TestUnifiedDiff(t *testing.T) {
	a := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	b := "a\nb\nX\nc\nd\ne\nf\ng\nh\ni\nY\n"
	got := UnifiedDiff("f", []byte(a), []byte(b))
	want := "--- f\n+++ f\n@@ -1,5 +1,6 @@\n a\n b\n+X\n c\n d\n e\n@@ -7,4 +8,4 @@\n g\n h\n i\n-j\n+Y\n"
	if got != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if UnifiedDiff("f", []byte(a), []byte(a)) != "" {
		t.Error("no change should be no diff")
	}
}
