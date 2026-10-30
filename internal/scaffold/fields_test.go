package scaffold

import (
	"slices"
	"strings"
	"testing"
)

// Every section init reads has a table in the docs.
func TestSectionsExist(t *testing.T) {
	for kind, secs := range kindSections {
		for _, s := range secs {
			if len(reference[s]) == 0 {
				t.Errorf("%s: no field table at docs/reference/%s", kind, s)
			}
		}
	}
	for _, blocks := range []map[string]string{componentBlocks, sourceBlocks} {
		for block, s := range blocks {
			if len(reference[s]) == 0 {
				t.Errorf("%s: no field table at docs/reference/%s", block, s)
			}
		}
	}
	for s := range filtered {
		if len(reference[s]) == 0 {
			t.Errorf("no field table at docs/reference/%s", s)
		}
	}
}

// Every shared field's "Valid on/with" cell reads as a set of kinds.
func TestEveryKindCellReads(t *testing.T) {
	for _, kind := range append(slices.Clone(ComponentKinds), SourceKinds...) {
		if _, err := fieldsFor(kind); err != nil {
			t.Error(err)
		}
	}
}

// The `type` rows name every kind init takes.
func TestTypeRowsNameEveryKind(t *testing.T) {
	for sec, kinds := range map[string][]string{
		"components.md#common-fields":              ComponentKinds,
		"sources.md#fields-shared-by-leaf-sources": SourceKinds,
	} {
		i := slices.IndexFunc(reference[sec], func(f Field) bool { return f.Name == "type" })
		if i < 0 {
			t.Fatalf("%s has no type row", sec)
		}
		for _, k := range kinds {
			if !strings.Contains(reference[sec][i].Desc, "`"+k+"`") {
				t.Errorf("%s: the type row doesn't name %s", sec, k)
			}
		}
	}
}

func TestKindsOf(t *testing.T) {
	all := ComponentKinds
	for cell, want := range map[string]string{
		"—":                          "",
		"all":                        "",
		"required when source-bound": "",
		"list, logview, eventlog, auto inspector (with `path:`)":         "list logview inspector eventlog",
		"source-bound markable table; span table; table with `activity`": "table",
		"source-bound target":       "list table logview tree inspector textview eventlog",
		"not with `follow`":         "",
		"not input, toggle or menu": "list table logview tree inspector textview eventlog",
	} {
		got, err := kindsOf(cell, all)
		if err != nil || strings.Join(got, " ") != want {
			t.Errorf("kindsOf(%q) = %q, %v; want %q", cell, got, err, want)
		}
	}
	for cell, want := range map[string]string{
		"required on http; not on exec":                          "http",
		"http: this or `offset_param`; not on exec":              "http",
		"streams: websocket, or exec / http with `follow: true`": "http exec websocket",
		"not `oauth2`": "",
	} {
		got, err := kindsOf(cell, SourceKinds)
		if err != nil || strings.Join(got, " ") != want {
			t.Errorf("kindsOf(%q) = %q, %v; want %q", cell, got, err, want)
		}
	}
	if _, err := kindsOf("any filterable kind", all); err == nil {
		t.Error("an unreadable cell read")
	}
}

// A shared field is kept only on the kinds it's valid on.
func TestFieldsFor(t *testing.T) {
	names := func(kind string) []string {
		secs, err := fieldsFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range secs {
			for _, f := range s {
				out = append(out, f.Name)
			}
		}
		return out
	}
	table, logview, websocket := names("table"), names("logview"), names("websocket")
	for _, c := range []struct {
		in   []string
		name string
		want bool
	}{
		{table, "filterable", true},
		{table, "searchable", false},
		{table, "columns", true},
		{logview, "searchable", true},
		{logview, "markable", false},
		{websocket, "timeout", true},
		{websocket, "initial_messages", true},
		{websocket, "url", true},
	} {
		if slices.Contains(c.in, c.name) != c.want {
			t.Errorf("%s in %v: want %v", c.name, c.in, c.want)
		}
	}
	if _, err := fieldsFor("tabel"); err == nil {
		t.Error("an unknown kind")
	}
}
