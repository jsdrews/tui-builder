package config

// `graphql:` on an http source: shorthand for a POST whose JSON body is
// {query, variables}, whose errors on a 200 are errors. See
// plans/http-source.md §9.

import "fmt"

// GraphQLSpec is a GraphQL request.
type GraphQLSpec struct {
	// Query is the GraphQL document. It's sent exactly as written and
	// never templated: values reach it through Variables.
	Query string `yaml:"query,omitempty"`
	// Variables are templated like json:: an exact-token string takes
	// its parameter's type, and one that resolves empty drops its key.
	Variables map[string]any `yaml:"variables,omitempty"`
}

// GraphQLErrorPath is the error_path a graphql: source has unless it
// sets its own: a GraphQL API reports errors on a 200.
const GraphQLErrorPath = "errors.0.message"

func (g *GraphQLSpec) Clone() *GraphQLSpec {
	if g == nil {
		return nil
	}
	out := *g
	if g.Variables != nil {
		out.Variables = cloneJSON(g.Variables).(map[string]any)
	}
	return &out
}

// Body is the request body graphql: stands for.
func (g *GraphQLSpec) Body() map[string]any {
	body := map[string]any{"query": g.Query}
	if len(g.Variables) > 0 {
		body["variables"] = g.Variables
	}
	return body
}

func (g *GraphQLSpec) validate(path string) error {
	if g.Query == "" {
		return fmt.Errorf("%s.query: required — the GraphQL document to send", path)
	}
	return nil
}
