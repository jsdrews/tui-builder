package main

import (
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// `required: true` on a boot prompt used to pass load and then let an
// empty value through, because the form never heard about it.
func TestPromptFieldsEnforceRequiredAndType(t *testing.T) {
	t.Setenv("GH_USER", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("LIMIT", "abc")
	t.Setenv("NOTE", "")
	fields := promptFields([]cfg.Prompt{
		{Key: "GH_USER", Parameter: cfg.Parameter{Required: true}},
		{Key: "GH_TOKEN", Parameter: cfg.Parameter{Required: true, Mask: true}},
		{Key: "LIMIT", Parameter: cfg.Parameter{Type: "int"}},
		{Key: "NOTE"},
	})
	for i, wantErr := range []bool{true, true, true, false} {
		err := fields[i].Validate()
		if (err != nil) != wantErr {
			t.Errorf("%s: Validate() = %v, want error %v", fields[i].Key(), err, wantErr)
		}
	}
}
