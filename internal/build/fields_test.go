package build

import (
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestFieldValidator(t *testing.T) {
	name := FieldValidator(cfg.Parameter{Validate: "value matches '^[a-z-]+$'", Invalid: "lowercase only"})
	if err := name("web-1x"); err == nil || err.Error() != "lowercase only" {
		t.Errorf("bad name: got %v, want the invalid message", err)
	}
	if err := name("web"); err != nil {
		t.Errorf("good name: %v", err)
	}
	if err := name(""); err != nil {
		t.Errorf("an empty optional answer isn't checked, got %v", err)
	}

	// The type's format check runs first, then the expression.
	replicas := FieldValidator(cfg.Parameter{Type: "int", Validate: "int(value) <= 10"})
	if err := replicas("ten"); err == nil || err.Error() != "must be a whole number" {
		t.Errorf("non-number: got %v", err)
	}
	if err := replicas("12"); err == nil || err.Error() != "not accepted" {
		t.Errorf("over the limit: got %v, want the default message", err)
	}
	if err := replicas("3"); err != nil {
		t.Errorf("in range: %v", err)
	}

	// A yes/no answer is a bool and is always checked.
	accept := FieldValidator(cfg.Parameter{Type: "bool", Validate: "value == true", Invalid: "must accept"})
	if err := accept(false); err == nil {
		t.Error("false should fail value == true")
	}
	if err := accept(true); err != nil {
		t.Errorf("true: %v", err)
	}

	if FieldValidator(cfg.Parameter{}) != nil {
		t.Error("nothing to check should give a nil validator")
	}
}

func TestFormFieldEnforcesRequiredOnEveryKind(t *testing.T) {
	for name, p := range map[string]cfg.Parameter{
		"text":   {Required: true},
		"masked": {Required: true, Mask: true},
		"select": {Required: true, Options: []string{"eu", "us"}},
	} {
		f := FormField("k", "K", p, "")
		if err := f.Validate(); err == nil {
			t.Errorf("%s: an unanswered required field should fail", name)
		}
	}
	// An optional select starts on its default and passes.
	if err := FormField("k", "K", cfg.Parameter{Options: []string{"eu", "us"}, Default: "us"}, "").Validate(); err != nil {
		t.Errorf("optional select: %v", err)
	}
	// A select's validate sees the picked option.
	f := FormField("k", "K", cfg.Parameter{Options: []string{"eu", "us"}, Default: "us", Validate: "value == 'eu'"}, "")
	if err := f.Validate(); err == nil {
		t.Error("select validate should reject us")
	}
}
