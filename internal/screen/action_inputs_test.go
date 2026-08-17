package screen

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/geom"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// orderConfig mirrors examples/action_prompts.yaml's `order` action: a
// list, an action whose `fruit` input is bound from the selection while
// `amount` and `priority` are left for the generated form, and a confirm
// message that previews all three.
func orderConfig() *cfg.Config {
	return &cfg.Config{
		Actions: map[string]*cfg.Action{
			"order": {
				Run: []string{"echo", "${inputs.amount}", "${inputs.fruit}", "${inputs.priority}"},
				Inputs: map[string]*cfg.Parameter{
					"fruit":    {Required: true},
					"amount":   {Label: "Quantity", Default: "1", Order: 1},
					"priority": {Label: "Priority", Options: []string{"low", "normal", "high"}, Order: 2},
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"fruits": {Type: "list", Items: []string{"Apple", "Banana"}},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{Component: "fruits"},
				Actions: []cfg.ActionBinding{{
					Key:     "b",
					Action:  "order",
					Label:   "order",
					From:    "fruits",
					Confirm: "Order ${inputs.amount} ${selection}(s) as ${inputs.priority} priority?",
					Bind:    map[string]string{"fruit": "${selection}"},
				}},
			},
		},
	}
}

// TestConfirmSubstitutesCollectedInputs pins a bug found in the shipped
// example: the confirm modal rendered the literal text
// "${inputs.amount}" instead of the value the form had just collected.
//
// The cause was a namespace mismatch — build.SubstituteAll still
// resolved the old ${prompt.*} family after the schema had collapsed
// prompts into inputs, so every ${inputs.*} token in a confirm message
// (or notice) passed through untouched.
func TestConfirmSubstitutesCollectedInputs(t *testing.T) {
	m := deleteScreen(t, orderConfig())

	// Fire the action. `amount` has a default and `priority` doesn't, so
	// the form opens for the inputs the binding left unfilled.
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.formModal == nil {
		t.Fatal("expected the input form to open for the unbound inputs")
	}

	// Submit the form directly rather than driving keystrokes — this test
	// is about substitution, not about the widget's editing behaviour.
	runToQuiescence(t, m, form.SubmittedMsg{Values: map[string]any{
		"amount":   "3",
		"priority": "high",
	}})
	if m.confirmModal == nil {
		t.Fatal("expected the confirm modal after submitting the form")
	}

	plain := xansi.Strip(m.Layout().Render(geom.New(0, 0, 100, 40)))
	if strings.Contains(plain, "${") {
		t.Errorf("confirm message has unsubstituted tokens:\n%s", plain)
	}
	for _, want := range []string{"Order 3", "high priority", "Apple"} {
		if !strings.Contains(plain, want) {
			t.Errorf("confirm message missing %q:\n%s", want, plain)
		}
	}
}

// An input with a Default is not prompted for — the author already
// answered. Here every input is either bound or defaulted, so the action
// goes straight to the confirm with no form in between.
func TestNoFormWhenEveryInputIsBoundOrDefaulted(t *testing.T) {
	c := orderConfig()
	c.Actions["order"].Inputs["priority"].Default = "normal"
	m := deleteScreen(t, c)

	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.formModal != nil {
		t.Error("no input should be prompted for when all are bound or defaulted")
	}
	if m.confirmModal == nil {
		t.Fatal("expected the confirm modal")
	}
	plain := xansi.Strip(m.Layout().Render(geom.New(0, 0, 100, 40)))
	if !strings.Contains(plain, "Order 1") || !strings.Contains(plain, "normal priority") {
		t.Errorf("defaults not substituted into the confirm:\n%s", plain)
	}
}
