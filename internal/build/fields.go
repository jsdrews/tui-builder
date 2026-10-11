package build

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jsdrews/tuilib/pkg/form"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/expr"
)

// FieldValidator is the form's per-field check for an action input or a
// boot prompt: the declared type's format check, then the field's own
// `validate:` expression. nil when there is nothing to check — the form
// treats a nil Validate as "always acceptable".
//
// An empty answer is left to Required: this runs after it, so a blank
// optional field stays blank rather than failing a check it was never
// obliged to satisfy. A yes/no answer is never empty, so it's always
// checked.
func FieldValidator(p cfg.Parameter) func(any) error {
	typeCheck := typeValidator(p.Type)
	var prog *expr.Program
	if p.Validate != "" {
		prog, _ = expr.Compile(p.Validate) // validated at load
	}
	if typeCheck == nil && prog == nil {
		return nil
	}
	invalid := p.Invalid
	if invalid == "" {
		invalid = "not accepted"
	}
	return func(v any) error {
		if s, ok := v.(string); ok && s == "" {
			return nil
		}
		if typeCheck != nil {
			if err := typeCheck(v); err != nil {
				return err
			}
		}
		if prog != nil {
			if ok, err := expr.EvalBool(prog, map[string]any{"value": v}); err != nil || !ok {
				return fmt.Errorf("%s", invalid)
			}
		}
		return nil
	}
}

// typeValidator is the format check for a declared data type, or nil.
func typeValidator(typ string) func(any) error {
	switch typ {
	case "int":
		return func(v any) error {
			s, _ := v.(string)
			if _, err := strconv.Atoi(s); err != nil {
				return fmt.Errorf("must be a whole number")
			}
			return nil
		}
	case "duration":
		return func(v any) error {
			s, _ := v.(string)
			if _, err := time.ParseDuration(s); err != nil {
				return fmt.Errorf("must be a duration, e.g. 30s or 5m")
			}
			return nil
		}
	}
	return nil
}

// FormField builds the form field that collects p, for an action input
// or a boot prompt. The data type picks the widget: options make a
// select, bool a yes/no toggle, mask a masked input, anything else a
// text input. initial is the text field's starting value (a prompt
// pre-fills from the environment); selects and toggles start from
// p.Default.
//
// Required and the field's check are the form's own enforcement: submit
// refuses, the label gains a "*", and the offending field's border tints
// with the reason. A select always has a highlighted option, so required
// there means "picked on purpose": it starts with nothing highlighted.
func FormField(key, label string, p cfg.Parameter, initial string) form.Field {
	check := FieldValidator(p)
	switch {
	case len(p.Options) > 0:
		return form.Select(form.SelectOptions{
			Key:         key,
			Label:       label,
			Options:     append([]string(nil), p.Options...),
			Initial:     indexOf(p.Options, p.Default),
			RequirePick: p.Required,
			Validate:    check,
		})
	case p.Type == "bool":
		return form.Confirm(form.ConfirmOptions{
			Key:      key,
			Label:    label,
			Initial:  p.Default == "true",
			Validate: check,
		})
	case p.Mask:
		// Display-only: the value is still the real string everywhere
		// else.
		return form.Password(form.PasswordOptions{
			Key:         key,
			Label:       label,
			Placeholder: p.Placeholder,
			Initial:     initial,
			Required:    p.Required,
			Validate:    check,
			MaskChar:    MaskChar(),
		})
	}
	return form.Text(form.TextOptions{
		Key:         key,
		Label:       label,
		Placeholder: p.Placeholder,
		Initial:     initial,
		Required:    p.Required,
		Validate:    check,
	})
}

// indexOf finds want in opts, or 0: a select's default is written as the
// option itself, the same way as every other input's.
func indexOf(opts []string, want string) int {
	for i, o := range opts {
		if o == want {
			return i
		}
	}
	return 0
}
