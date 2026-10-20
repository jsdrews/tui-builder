package config

import (
	"strings"
	"testing"
)

func presetConfig(a *Action) *Config {
	five := "5s"
	return &Config{
		Data: DataBlock{Presets: map[string]*Source{
			"api": {Type: "http", URL: "https://api.test/v1", Timeout: five, Format: "json",
				Headers: map[string]string{"Accept": "application/json", "X-Team": "a"},
				Query:   map[string]QueryValue{"lang": {"en"}},
				Auth:    &AuthSpec{Bearer: "${env.TOKEN}"},
				Retry:   &RetrySpec{Max: 3}},
		}},
		Actions: map[string]*Action{"x": a},
	}
}

// An action takes url, headers, query, auth and timeout from its preset,
// and nothing else.
func TestActionExtendsPreset(t *testing.T) {
	a := &Action{Extends: "api", URL: "/items/${inputs.id}", Method: "DELETE",
		Headers: map[string]string{"X-Team": ""}, Inputs: map[string]*Parameter{"id": {}}}
	c := presetConfig(a)
	if err := resolveActionPresets(c); err != nil {
		t.Fatal(err)
	}
	if a.Kind() != "http" || a.URL != "https://api.test/v1/items/${inputs.id}" || a.Timeout != "5s" ||
		a.Auth.Bearer != "${env.TOKEN}" || a.Headers["Accept"] != "application/json" || a.Query["lang"][0] != "en" {
		t.Errorf("merged = %+v", a)
	}
	if _, ok := a.Headers["X-Team"]; ok {
		t.Error(`"" didn't drop an inherited header`)
	}
	if a.Inherited["auth"] != "api" || a.ExtendsChain[0] != "api" {
		t.Errorf("inherited %v, chain %v", a.Inherited, a.ExtendsChain)
	}
	if err := validateActionDefs(c.Actions); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestActionPresetErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a       *Action
		mutate  func(*Config)
		wantErr string
	}{
		{"unknown preset", &Action{Extends: "nope", URL: "/x"}, nil, `"nope"`},
		{"exec", &Action{Extends: "api", Run: []string{"true"}, Type: "exec"}, nil, "only applies to an http action"},
		{"unset unknown", &Action{Extends: "api", URL: "/x", Unset: []string{"retry"}}, nil, "unset"},
		{"unset without extends", &Action{Type: "http", URL: "/x", Unset: []string{"auth"}}, nil, "only applies with extends:"},
		{"params", &Action{Extends: "api", URL: "/x"}, func(c *Config) { c.Data.Presets["api"].URL = "https://api.test/${params.org}" },
			"preset \"api\" uses ${params.org}, which an action can't fill"},
	} {
		c := presetConfig(tc.a)
		if tc.mutate != nil {
			tc.mutate(c)
		}
		err := resolveActionPresets(c)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestActionUnsetDropsInherited(t *testing.T) {
	a := &Action{Extends: "api", URL: "/x", Unset: []string{"auth", "query"}}
	if err := resolveActionPresets(presetConfig(a)); err != nil {
		t.Fatal(err)
	}
	if a.Auth != nil || len(a.Query) != 0 {
		t.Errorf("auth %v, query %v", a.Auth, a.Query)
	}
}
