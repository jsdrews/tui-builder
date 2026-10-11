package config

import (
	"strings"
	"testing"
)

// Every key but enter is a shortcut inside the action menu, and tuilib's
// menu handles its own navigation keys first. A shortcut on one of them
// would load and then never fire.
func TestActionKeyCannotBeAMenuKey(t *testing.T) {
	for _, k := range []string{"up", "k", "down", "j", "g", "G", "ctrl+u", "ctrl+d"} {
		c := promptConfig()
		bindKey(c, k)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "action menu's own key") {
			t.Errorf("key %q: want a menu-key error, got %v", k, err)
		}
	}
}

func TestActionKeyMenuShortcutAccepted(t *testing.T) {
	for _, k := range []string{"d", "enter", "ctrl+x", "/"} {
		c := promptConfig()
		bindKey(c, k)
		if err := c.Validate(); err != nil {
			t.Errorf("key %q: Validate: %v", k, err)
		}
	}
}
