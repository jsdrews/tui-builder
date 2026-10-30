package gen

import (
	"context"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/jsdrews/tui-builder/internal/columns"
)

// schemaOf loads a one-schema spec and returns the schema named Row.
func schemaOf(t *testing.T, row string) *openapi3.Schema {
	t.Helper()
	spec := "openapi: 3.0.3\ninfo: {title: t, version: '1'}\npaths: {}\ncomponents:\n  schemas:\n    Row:\n" + indent(row, "      ")
	loader := openapi3.NewLoader()
	loader.IncludeOrigin = true
	doc, err := loader.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return doc.Components.Schemas["Row"].Value
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, "\t")
		lines[i] = pad + strings.Repeat("  ", len(l)-len(trimmed)) + trimmed
	}
	return strings.Join(lines, "\n") + "\n"
}

const testRow = `
type: object
required: [id, name]
properties:
	id: {type: integer}
	name: {type: string}
	state: {type: string, enum: [running, failed]}
	ready: {type: boolean}
	created: {type: string, format: date-time}
	restarts: {type: integer}
	region: {type: string}
	notes: {type: string}
	body: {type: string, maxLength: 500}
	tags: {type: array, items: {type: string}}
	owners: {type: array, items: {type: object, properties: {n: {type: string}}}}
	labels: {type: object, additionalProperties: {type: string}}
	spec: {type: object, properties: {name: {type: string}, deep: {type: object, properties: {x: {type: string}}}}}
	secret: {type: string, writeOnly: true}
	either: {oneOf: [{type: string}, {type: integer}]}
	maybe: {anyOf: [{type: string}, {type: "null"}]}
`

// Scalars, two-level dot-paths and lists of scalars are columns; lists of
// objects, maps, deeper objects, writeOnly fields and alternatives of
// different shapes aren't.
func TestSchemaFields(t *testing.T) {
	var got []string
	for _, f := range schemaFields(schemaOf(t, testRow)) {
		got = append(got, f.path)
	}
	want := "id name state ready created restarts region notes body tags spec.name maybe"
	if strings.Join(got, " ") != want {
		t.Errorf("fields:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestRank(t *testing.T) {
	fs := schemaFields(schemaOf(t, testRow))
	cols := rank(fs, identityOf(fs, "pods"))
	got := map[string]columns.Column{}
	for _, c := range cols {
		got[c.Path] = c
	}
	for path, want := range map[string]columns.Rank{
		"name": columns.Identity, "state": columns.Status, "ready": columns.Status,
		"created": columns.Time, "id": columns.Number, "restarts": columns.Number,
		"region": columns.Short, "tags": columns.Short, "notes": columns.Long, "body": columns.Long,
	} {
		if got[path].Rank != want {
			t.Errorf("%s ranked %d, want %d", path, got[path].Rank, want)
		}
	}
	if got["spec.name"].Title != "Spec name" || got["name"].Title != "Name" {
		t.Errorf("clashing titles: %q, %q", got["name"].Title, got["spec.name"].Title)
	}
	if !got["ready"].Bool || got["id"].Sort != "number" || strings.Join(got["state"].Values, ",") != "running,failed" {
		t.Errorf("ready %+v, id %+v, state %+v", got["ready"], got["id"], got["state"])
	}
	if k := markKey(schemaOf(t, testRow), fs, "pods"); k != "id" {
		t.Errorf("mark key %q, want the required id", k)
	}
}

// With no name-like field, the id the detail binds is the identity.
func TestIdentityWithoutName(t *testing.T) {
	fs := schemaFields(schemaOf(t, "type: object\nproperties:\n\tslug: {type: string}\n\tsize: {type: integer}"))
	if id := identityOf(fs, "reports"); id != "slug" {
		t.Errorf("identity %q, want slug", id)
	}
}

// A detail's inspector colours its enum fields, each rule scoped by path.
func TestInspectorRules(t *testing.T) {
	rules := inspectorRules(schemaOf(t, testRow))
	if rules == nil || len(rules.Content) != 4 {
		t.Fatalf("rules: %v", rules)
	}
	first := rules.Content[0]
	if get(first, "when").Value != "running" || get(first, "path").Value != "state" {
		t.Errorf("first rule: %s", flowLine(first))
	}
}

// The fixture's things table is the plan's: name, status, time, the
// three numbers, tags, and the long text in the last slot.
func TestFixtureColumns(t *testing.T) {
	res, err := Generate(context.Background(), Options{Input: Input{Spec: "../fixture/things/things.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Config)
	start := strings.Index(out, "    things:\n      type: table")
	block := out[start : start+strings.Index(out[start:], "    thing:\n")]
	var titles []string
	for _, l := range strings.Split(block, "\n") {
		if i := strings.Index(l, "title: "); i >= 0 && strings.Contains(l, "- ") && !strings.Contains(l, "#") {
			titles = append(titles, strings.SplitN(l[i+7:], ",", 2)[0])
		}
	}
	if got := strings.Join(titles, ", "); got != "Name, Status, Created at, Id, Size, Owner id, Tags, Description" {
		t.Errorf("columns: %s\n%s", got, block)
	}
	if !strings.Contains(block, "# - {title: Team, value: labels.team}") {
		t.Errorf("labels aren't commented out:\n%s", block)
	}
}
