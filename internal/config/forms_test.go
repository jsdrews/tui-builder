package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfirmAcceptsAStringOrAMap(t *testing.T) {
	var b ActionBinding
	if err := yaml.Unmarshal([]byte(`confirm: "Delete it?"`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Confirm.Message != "Delete it?" || b.Confirm.Default != "" {
		t.Errorf("string form = %+v", b.Confirm)
	}
	b = ActionBinding{}
	if err := yaml.Unmarshal([]byte(`confirm: {message: "Restart?", default: yes}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Confirm.Message != "Restart?" || b.Confirm.Default != "yes" {
		t.Errorf("map form = %+v", b.Confirm)
	}
}

func TestConfirmValidation(t *testing.T) {
	cases := []struct {
		confirm Confirm
		want    string
	}{
		{Confirm{Message: "Sure?"}, ""},
		{Confirm{Message: "Sure?", Default: "yes"}, ""},
		{Confirm{Message: "Sure?", Default: "maybe"}, "confirm.default"},
		{Confirm{Default: "yes"}, "needs a `message:`"},
	}
	for _, tc := range cases {
		c := promptConfig()
		bindKey(c, "d")
		c.TUI.Screen.Actions[0].Confirm = tc.confirm
		err := c.Validate()
		if tc.want == "" {
			if err != nil {
				t.Errorf("%+v: %v", tc.confirm, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: want %q, got %v", tc.confirm, tc.want, err)
		}
	}
}

func TestFormCheckValidation(t *testing.T) {
	cases := []struct {
		name string
		p    Parameter
		want string
	}{
		{"compiles", Parameter{Validate: "len(value) > 2", Invalid: "too short"}, ""},
		{"bad expression", Parameter{Validate: "value >"}, "validate"},
		{"invalid alone", Parameter{Invalid: "nope"}, "`invalid:` needs `validate:`"},
	}
	for _, tc := range cases {
		// As an action input…
		c := promptConfig()
		bindKey(c, "d")
		p := tc.p
		c.Actions["run"].Inputs = map[string]*Parameter{"name": &p}
		c.TUI.Screen.Actions[0].Bind = map[string]string{"name": "x"}
		err := c.Validate()
		// …and as a boot prompt.
		c2 := promptConfig()
		c2.App.Prompts = []Prompt{{Key: "NAME", Parameter: tc.p}}
		err2 := c2.Validate()
		for where, e := range map[string]error{"input": err, "prompt": err2} {
			if tc.want == "" {
				if e != nil {
					t.Errorf("%s as %s: %v", tc.name, where, e)
				}
				continue
			}
			if e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Errorf("%s as %s: want %q, got %v", tc.name, where, tc.want, e)
			}
		}
	}
}

func TestFormCheckRejectedOnSourceParameters(t *testing.T) {
	c := promptConfig()
	c.Data.Sources["items"].Parameters = map[string]*Parameter{"q": {Validate: "len(value) > 0"}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "only apply where a form collects") {
		t.Errorf("want validate rejected on a source parameter, got %v", err)
	}
}
