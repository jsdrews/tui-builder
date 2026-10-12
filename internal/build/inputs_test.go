package build

import (
	"testing"

	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

func TestSubstituteInputValues(t *testing.T) {
	values := map[string]string{"search": "web", "flag": "true"}
	got := SubstituteInputValues("q=${input.search}&del=${input.flag}&x=${input.none}&n=${inputs.name}&s=${selection}", values)
	if want := "q=web&del=true&x=&n=${inputs.name}&s=${selection}"; got != want {
		t.Errorf("got %q, want %q (other tokens left for their own resolvers)", got, want)
	}
}

func TestInputAndToggleValues(t *testing.T) {
	th := theme.Nord()
	in, _ := NewComponent(&cfg.Component{Type: "input", Initial: "web"}, th)
	tg, _ := NewComponent(&cfg.Component{Type: "toggle", Initial: "true"}, th)
	if InputValue(in) != "web" || InputValue(tg) != "true" {
		t.Errorf("values = %q, %q", InputValue(in), InputValue(tg))
	}
	in.Rebuild(theme.Dark())
	if InputValue(in) != "web" {
		t.Errorf("a theme swap lost the input's value: %q", InputValue(in))
	}
}

// An input in a vstack is three rows unless sized otherwise.
func TestInputDefaultsToThreeRowsInAVStack(t *testing.T) {
	root := &cfg.Node{VStack: []cfg.Item{
		{Node: cfg.Node{Component: "search"}},
		{Node: cfg.Node{Component: "items"}},
	}}
	tree, err := Build(root, map[string]*cfg.Component{
		"search": {Type: "input"},
		"items":  {Type: "list", Items: []string{"a"}},
	}, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.fixedFor(root.VStack[0]); got != cfg.InputHeight {
		t.Errorf("input height = %d, want %d", got, cfg.InputHeight)
	}
	if got := tree.fixedFor(root.VStack[1]); got != 0 {
		t.Errorf("a list should stay flexible, got fixed %d", got)
	}
}
