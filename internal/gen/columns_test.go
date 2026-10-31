package gen

import (
	"context"
	"slices"
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
	// A nested object's name is an identity too, titled by its parent.
	if got["spec.name"].Title != "Spec" || got["spec.name"].Rank != columns.Identity || got["name"].Title != "Name" {
		t.Errorf("nested name: %q (%d), name %q", got["spec.name"].Title, got["spec.name"].Rank, got["name"].Title)
	}
	// An id keeps its rank for its style, but sorts as minor.
	if !got["id"].Minor || got["region"].Minor {
		t.Errorf("minor: id %v, region %v", got["id"].Minor, got["region"].Minor)
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

// The fixture's things table: name, status, size, the short text and
// the labels, the bookkeeping timestamp, and the long text in the last
// slot. The ids are minor: owner_id is hidden for its filter, id for
// the detail's bind.
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
	if got := strings.Join(titles, ", "); got != "Name, Status, Size, Tags, Team, Env, Created at, Description, Owner id, Id" {
		t.Errorf("columns: %s\n%s", got, block)
	}
	if !strings.Contains(block, "- {title: Owner id, value: owner_id, hidden: true}") {
		t.Errorf("owner_id, a filter's column, isn't hidden:\n%s", block)
	}
}

// An event-shaped row: what you'd look at first is the title, the
// venue's name, the times, the artists and categories; the bookkeeping
// timestamps, coordinates and ids come after.
const eventRow = `
type: object
properties:
	id: {type: string, format: uuid}
	title: {type: string}
	description: {type: string}
	venue: {type: object, properties: {id: {type: string, format: uuid}, name: {type: string}, address: {type: string}, lat: {type: number}, lng: {type: number}}}
	start_time: {type: string, format: date-time}
	end_time: {type: string, format: date-time}
	categories: {type: array, items: {type: string}}
	artist_names: {type: array, items: {type: string}}
	price: {type: object, properties: {min: {type: number}}}
	image_url: {type: string, format: uri}
	created_at: {type: string, format: date-time}
	updated_at: {type: string, format: date-time}
`

func TestRankEvents(t *testing.T) {
	fs := schemaFields(schemaOf(t, eventRow))
	kept, rest := columns.Pick(rank(fs, identityOf(fs, "events")), columns.DefaultMax)
	var titles []string
	for _, c := range kept {
		titles = append(titles, c.Title)
	}
	if got := strings.Join(titles, ", "); got != "Title, Venue, Start time, End time, Categories, Artist names, Address, Description" {
		t.Errorf("kept: %s", got)
	}
	var paths []string
	for _, c := range rest {
		paths = append(paths, c.Path)
	}
	// Minor, in tier order: nested fields, bookkeeping, coordinates, ids
	// and links.
	if got := strings.Join(paths, " "); got != "price.min created_at updated_at venue.lat venue.lng id venue.id image_url" {
		t.Errorf("rest: %s", got)
	}
}

// A window's filters find their parameters through a column's
// underscored path (venue.id → venue_id) and a range named after the
// column less _time (start_time → start_after, start_before), and the
// columns they name stay in the table.
func TestWindowFilterParams(t *testing.T) {
	q := map[string]*openapi3.Parameter{}
	for _, n := range []string{"venue_id", "start_after", "start_before", "created_from", "created_to"} {
		q[n] = &openapi3.Parameter{Name: n, In: "query"}
	}
	if p := paramFor(q, "venue.id"); p != "venue_id" {
		t.Errorf("paramFor(venue.id) = %q", p)
	}
	for path, want := range map[string]string{"start_time": "start_after", "created_at": "created_from", "end_time": ""} {
		if lo, _ := rangePair(q, path); lo != want {
			t.Errorf("rangePair(%s) = %q, want %q", path, lo, want)
		}
	}
}

func TestKeepFiltered(t *testing.T) {
	kept := []columns.Column{{Path: "title", Rank: columns.Identity}, {Path: "when", Rank: columns.Time}, {Path: "notes", Rank: columns.Long}}
	rest := []columns.Column{{Path: "tags", Rank: columns.Short}, {Path: "owner_id", Rank: columns.Short, Minor: true}}
	kept, rest, hidden := keepFiltered(kept, rest, []string{"tags", "owner_id"})
	var got []string
	for _, cs := range [][]columns.Column{kept, rest, hidden} {
		var p []string
		for _, c := range cs {
			p = append(p, c.Path)
		}
		got = append(got, strings.Join(p, " "))
	}
	// tags takes when's place; owner_id, minor, is hidden instead.
	if strings.Join(got, " | ") != "title tags notes | when | owner_id" {
		t.Errorf("kept | rest | hidden = %s", strings.Join(got, " | "))
	}
}

// A GitHub-shaped spec: /orgs/{org}/issues has no /orgs list, but
// /user/orgs ends in the same segment, so the orgs are a list to pick
// from (bound by login, since {org} isn't an id) rather than a prompt.
// Left out by the selection, a hint names it. No security scheme warns.
const orgsSpec = `openapi: 3.0.3
info: {title: Hub, version: '1'}
servers: [{url: 'https://api.example.com'}]
paths:
  /user/orgs:
    get:
      operationId: orgs/list-mine
      tags: [orgs]
      responses:
        '200': {description: ok, content: {application/json: {schema: {type: array, items: {$ref: '#/components/schemas/Org'}}}}}
  /orgs/{org}/issues:
    get:
      operationId: issues/list-for-org
      tags: [issues]
      parameters: [{name: org, in: path, required: true, schema: {type: string}}]
      responses:
        '200': {description: ok, content: {application/json: {schema: {type: array, items: {$ref: '#/components/schemas/Issue'}}}}}
components:
  schemas:
    Org: {type: object, properties: {id: {type: integer}, login: {type: string}}}
    Issue: {type: object, properties: {id: {type: integer}, title: {type: string}}}
`

func TestOrgsPickedFromSameEndingList(t *testing.T) {
	gen := func(sel Select) Result {
		t.Helper()
		res, err := Generate(context.Background(), Options{Input: Input{Spec: "-", Stdin: strings.NewReader(orgsSpec)}, Select: sel})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := gen(Select{})
	out := string(res.Config)
	if strings.Contains(out, "prompts:") || !strings.Contains(out, "url: /orgs/${params.org}/issues") || !strings.Contains(out, "bind: {org: '${selection.Login}'}") {
		t.Errorf("the orgs aren't a list to pick from:\n%s", out)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "no security scheme") }) {
		t.Errorf("no warning about the missing auth: %q", res.Warnings)
	}
	res = gen(Select{Tags: []string{"issues"}})
	if !strings.Contains(string(res.Config), "prompts:") || len(res.Hints) != 1 || !strings.Contains(res.Hints[0], "--operation orgs/list-mine (GET /user/orgs)") {
		t.Errorf("--tag issues: hints %q", res.Hints)
	}
}
