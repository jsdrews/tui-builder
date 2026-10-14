package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestGraphQLErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"with json", `{type: http, url: "https://h", graphql: {query: "{ x }"}, json: {a: 1}}`, "json and graphql are mutually exclusive"},
		{"on exec", `{type: exec, command: [echo], graphql: {query: "{ x }"}}`, "graphql: only applies to type: http"},
		{"no query", `{type: http, url: "https://h", graphql: {variables: {a: 1}}}`, "graphql.query: required"},
		{"typo", `{type: http, url: "https://h", graphql: {query: "{ x }", varables: {}}}`, `graphql: unknown field "varables" (did you mean variables?)`},
		{"patch with graphql is fine, raw body isn't", `{type: http, url: "https://h", body: "{}", graphql: {query: "{ x }"}}`, "body and graphql are mutually exclusive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadData(t, "sources:\n  issues: "+c.src+"\n")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// Variables are templated, with types; the query never is.
func TestGraphQLTemplating(t *testing.T) {
	s := &Source{
		Type:       "http",
		URL:        "https://h",
		Parameters: map[string]*Parameter{"first": {Type: "int", Default: "50"}, "owner": {Default: "jsdrews"}},
		GraphQL: &GraphQLSpec{
			Query:     `query($first: Int) { a(first: $first, note: "${params.owner}") }`,
			Variables: map[string]any{"first": "${params.first}", "owner": "${params.owner}", "after": "${params.missing_is_dropped}"},
		},
	}
	s.Parameters["missing_is_dropped"] = &Parameter{}
	if err := s.BindParams(nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.GraphQL.Query, "${params.owner}") {
		t.Errorf("query was templated: %s", s.GraphQL.Query)
	}
	want := map[string]any{"first": float64(50), "owner": "jsdrews"}
	if !reflect.DeepEqual(s.GraphQL.Variables, want) {
		t.Errorf("variables = %#v", s.GraphQL.Variables)
	}
}
