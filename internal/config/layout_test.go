package config

import (
	"strings"
	"testing"
)

// layoutConfig wraps promptConfig's single list in a vstack whose item
// sizing the test sets.
func layoutConfig(it Item) *Config {
	c := promptConfig()
	it.Node = Node{Component: "items"}
	c.TUI.Screen.Layout = Node{VStack: []Item{it}}
	return c
}

func TestLayoutItemSizing(t *testing.T) {
	cases := []struct {
		name    string
		item    Item
		wantErr bool
	}{
		{"neither", Item{}, false},
		{"flex only", Item{Flex: 2}, false},
		{"fixed only", Item{Fixed: 10}, false},
		// fixed used to win silently, so the author never saw their flex.
		{"both", Item{Flex: 2, Fixed: 10}, true},
	}
	for _, tc := range cases {
		err := layoutConfig(tc.item).Validate()
		if tc.wantErr {
			if err == nil || !strings.Contains(err.Error(), "not both") {
				t.Errorf("%s: want a flex/fixed error, got %v", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Validate: %v", tc.name, err)
		}
	}
}

func TestLayoutItemSizingCheckedInNestedStacks(t *testing.T) {
	c := promptConfig()
	c.TUI.Screen.Layout = Node{HStack: []Item{{Node: Node{VStack: []Item{
		{Flex: 1, Fixed: 3, Node: Node{Component: "items"}},
	}}}}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "hstack[0].vstack[0]") {
		t.Errorf("want the error to name the nested item, got %v", err)
	}
}
