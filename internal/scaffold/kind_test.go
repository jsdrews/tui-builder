package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jsdrews/tui-builder/internal/config"
)

// loadKind writes b and loads it.
func loadKind(t *testing.T, b []byte) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tui-builder.yaml")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	_, err := config.LoadTo(path, &warn)
	return err
}

func TestKindGoldens(t *testing.T) {
	for _, kind := range Kinds() {
		ex, err := Kind(kind, "demo", "")
		if err != nil {
			t.Fatal(err)
		}
		got := ex.Config
		golden := filepath.Join("testdata", "init", kind+".yaml")
		if *update {
			os.MkdirAll(filepath.Dir(golden), 0o755)
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("init %s differs from %s (go test ./internal/scaffold -update to accept)", kind, golden)
		}
	}
}

func TestKindLoads(t *testing.T) {
	for _, kind := range Kinds() {
		ex, err := Kind(kind, "My: app", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := loadKind(t, ex.Config); err != nil {
			t.Errorf("init %s doesn't load: %v", kind, err)
		}
	}
	if _, err := Kind("tabel", "x", ""); err == nil {
		t.Error("an unknown kind")
	}
}

// Every field the docs say a kind takes is in its example, set or
// commented out, and so is every field of every block it shows.
func TestKindShowsEveryField(t *testing.T) {
	for _, kind := range Kinds() {
		ex, err := Kind(kind, "demo", "")
		if err != nil {
			t.Fatal(err)
		}
		b := ex.Config
		has := func(name string) bool {
			for _, line := range strings.Split(string(b), "\n") {
				line = strings.TrimLeft(line, " ")
				line = strings.TrimLeft(strings.TrimPrefix(line, "# "), " -")
				line = strings.TrimPrefix(strings.TrimLeft(line, "{"), "- ")
				if strings.HasPrefix(line, name+":") || strings.Contains(line, "{"+name+":") || strings.Contains(line, " "+name+":") {
					return true
				}
			}
			return false
		}
		secs, err := fieldsFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, fs := range secs {
			for _, f := range fs {
				if !has(f.Name) {
					t.Errorf("init %s lacks %s", kind, f.Name)
				}
			}
		}
		// Each block the example shows lists its whole section.
		walkEntries(t, kind, func(e *entry) {
			if e.block == nil || e.key == "" {
				return
			}
			sec, ok := blockSection(kind, e.key)
			if !ok {
				return // a key of a block, like auth's basic:
			}
			if _, named := mapBlocks[e.key]; named {
				e = e.block[0]
			}
			if want, got := len(blockFields(kind, sec)), len(e.block); want != got {
				t.Errorf("init %s: %s shows %d fields of %d", kind, e.path, got, want)
			}
		})
	}
}

// walkEntries calls f on every commented entry in kind's example.
func walkEntries(t *testing.T, kind string, f func(*entry)) {
	t.Helper()
	seen := map[string]bool{}
	if _, err := kindWith(kind, "demo", func(e *entry) bool {
		if e.path != "" && !seen[e.path] {
			seen[e.path] = true
			f(e)
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
}

// needsMore are the entries that don't load uncommented on their own,
// and why, by kind:path; a key also covers what's under it. The test
// checks they still fail, so the list can't go stale.
var needsMore = map[string]string{
	"table:columns[].metric":    "a metric takes exactly one of badge, ratio, bar, spark, and not beside value:",
	"inspector:fields[].metric": "a metric takes exactly one of badge, ratio, bar, spark, and not beside path:",
	"table:columns[].distinct":  "distinct: needs a windowed source",
	"table:max_items":           "max_items: needs a span table",
	"http:resume":               "resume: needs follow: true",
	"exec:resume":               "resume: needs follow: true",
	"http:window.page_start":    "page_start: needs page_param:, which is instead of offset_param:",
	"exec:window":               "an exec window's command: must use ${window.offset} and ${window.limit}",
}

// needsMoreOnAll are needsMore on every kind that shows the entry.
var needsMoreOnAll = map[string]string{
	"mark_key":            "mark_key: needs markable: true",
	"on_cursor":           "on_cursor: needs another component on the screen to drive it",
	"on_input":            "on_input: needs an input or toggle on the screen",
	"window.anchor":       "anchor: needs cursor:",
	"window.growing":      "growing: needs an eventlog",
	"window.follow_every": "follow_every: needs growing:",
}

// why is the reason path needs more on kind, if it does.
func why(kind, path string) (string, bool) {
	for k, r := range needsMore {
		if c, p, _ := strings.Cut(k, ":"); c == kind && (p == path || strings.HasPrefix(path, p+".")) {
			return r, true
		}
	}
	for p, r := range needsMoreOnAll {
		if p == path || strings.HasPrefix(path, p+".") {
			return r, true
		}
	}
	return "", false
}

// alsoOn are entries a block needs beside its required keys: one of
// two keys it can't do without.
// By kind:path; a bare path is on every kind.
var alsoOn = map[string][]string{
	"activity":       {"activity.busy"},
	"parameters":     {"parameters.q.type"},
	"http:window":    {"window.offset_param", "window.limit_param"},
	"window.find":    {"window.find.params", "window.find.position"},
	"http:paginate":  {"paginate.page_param"},
	"table:activity": {"activity.busy", "mark_key"},
}

// unit is the entries uncommented to load one entry on its own: it, its
// ancestors, and the required keys of each.
func unit(kind string, root []*entry, path string) map[string]bool {
	on := map[string]bool{}
	var closure func(e *entry)
	closure = func(e *entry) {
		on[e.path] = true
		for _, c := range e.block {
			if c.required {
				closure(c)
			}
		}
	}
	var find func(es []*entry, chain []*entry) bool
	find = func(es []*entry, chain []*entry) bool {
		for _, e := range es {
			c := append(slices.Clone(chain), e)
			if e.path == path {
				for _, a := range c {
					closure(a)
				}
				return true
			}
			if find(e.block, c) {
				return true
			}
		}
		return false
	}
	find(root, nil)
	for p := range on {
		also, ok := alsoOn[kind+":"+p]
		if !ok {
			also = alsoOn[p]
		}
		for _, q := range also {
			// With its ancestors: parameters.q.type needs parameters.q.
			for i := range q {
				if q[i] == '.' {
					on[q[:i]] = true
				}
			}
			on[q] = true
		}
	}
	return on
}

// Each commented field, uncommented on its own (with what it sits in),
// still loads. This is what ties the docs' field list to the code.
func TestKindEachFieldLoads(t *testing.T) {
	for _, kind := range Kinds() {
		roots, err := markerRoots(kind)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		walkEntries(t, kind, func(e *entry) { paths = append(paths, e.path) })
		for _, p := range paths {
			on := unit(kind, roots, p)
			b, err := kindWith(kind, "demo", func(e *entry) bool { return on[e.path] })
			if err != nil {
				t.Fatal(err)
			}
			err = loadKind(t, b)
			why, want := why(kind, p)
			switch {
			case want && err == nil:
				t.Errorf("init %s: %s loads on its own now; drop it from needsMore (%s)", kind, p, why)
			case !want && err != nil:
				t.Errorf("init %s: %s uncommented doesn't load: %v", kind, p, err)
			}
		}
	}
}

// structFor are the config structs that hold each section's fields.
var structFor = map[string][]any{
	"components.md#common-fields":    {config.Component{}},
	"components.md#list":             {config.Component{}},
	"components.md#table":            {config.Component{}},
	"components.md#logview":          {config.Component{}},
	"components.md#textview":         {config.Component{}},
	"components.md#tree":             {config.Component{}},
	"components.md#inspector":        {config.Component{}},
	"components.md#eventlog":         {config.Component{}},
	"components.md#input":            {config.Component{}},
	"components.md#toggle":           {config.Component{}},
	"components.md#menu":             {config.MenuItem{}},
	"components.md#columns":          {config.Column{}},
	"components.md#metrics":          {config.Metric{}},
	"components.md#inspector-fields": {config.InspectorField{}},
	"components.md#color-rules":      {config.ColorRule{}},
	"components.md#colors":           {config.Colors{}},
	"components.md#on_cursor":        {config.OnCursor{}},
	"components.md#on_input":         {config.OnInput{}},
	"components.md#activity":         {config.Activity{}},

	"sources.md#fields-shared-by-leaf-sources": {config.Source{}},
	"sources.md#http":                          {config.Source{}},
	"sources.md#exec":                          {config.Source{}},
	"sources.md#file":                          {config.Source{}},
	"sources.md#websocket":                     {config.Source{}},
	"sources.md#static":                        {config.Source{}},
	"sources.md#merge":                         {config.Source{}},
	"sources.md#merge-children":                {config.MergeChild{}},
	"sources.md#parameters":                    {config.Parameter{}},
	"sources.md#paginate":                      {config.PaginateConfig{}},
	"sources.md#window":                        {config.WindowConfig{}},
	"sources.md#find":                          {config.FindConfig{}},
	"sources.md#growing":                       {config.Growing{}},
	"sources.md#resume":                        {config.ResumeSpec{}},
	"sources.md#cache":                         {config.CacheSpec{}},
	"sources.md#retry":                         {config.RetrySpec{}},
	"sources.md#graphql":                       {config.GraphQLSpec{}},
	// The auth table lists each scheme's keys under it.
	"sources.md#auth": {config.AuthSpec{}, config.BasicAuth{}, config.APIKeyAuth{}, config.OAuth2Auth{}},
}

// The loader ignores keys it doesn't know, so a misspelt doc row would
// load fine: every documented field must be a YAML key of its struct.
func TestEveryFieldIsAKey(t *testing.T) {
	for sec, vs := range structFor {
		keys := map[string]bool{}
		var collect func(reflect.Type)
		collect = func(typ reflect.Type) {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				tag := f.Tag.Get("yaml")
				if strings.Contains(tag, ",inline") {
					collect(f.Type)
					continue
				}
				keys[strings.Split(tag, ",")[0]] = true
			}
		}
		for _, v := range vs {
			collect(reflect.TypeOf(v))
		}
		for _, f := range reference[sec] {
			if !keys[f.Name] {
				t.Errorf("%s: %s isn't a key of %T", sec, f.Name, vs[0])
			}
		}
	}
	// Every section init writes is checked.
	for _, secs := range kindSections {
		for _, sec := range secs {
			if structFor[sec] == nil {
				t.Errorf("structFor has no %s", sec)
			}
		}
	}
	for _, blocks := range []map[string]string{componentBlocks, sourceBlocks} {
		for _, sec := range blocks {
			if structFor[sec] == nil {
				t.Errorf("structFor has no %s", sec)
			}
		}
	}
}

func TestFirstSentence(t *testing.T) {
	for in, want := range map[string]string{
		"One. Two.":                "One.",
		"Like `a. b` here. Next.":  "Like `a. b` here.",
		"e.g. this one. And more.": "e.g. this one.",
		"No stop":                  "No stop",
	} {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// init file's path finds the file it writes beside the config.
func TestKindFile(t *testing.T) {
	ex, err := Kind("file", "demo", "sub")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(ex.Config, []byte("path: sub/services.json")) || len(ex.Files["services.json"]) == 0 {
		t.Errorf("init file -o sub/…: %q, files %v", ex.Config, ex.Files)
	}
	if ex, _ := Kind("file", "demo", "."); !bytes.Contains(ex.Config, []byte("path: services.json")) {
		t.Errorf("init file in .: %s", ex.Config)
	}
}
