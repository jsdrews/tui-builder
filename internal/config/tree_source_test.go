package config

import (
	"strings"
	"testing"
)

// Tests for the source-bound tree schema (feature A from the tui-builder
// integration batch).
//
//   1. Source-bound tree without `label:` is rejected — no way to render
//      leaves.
//   2. Static-root tree (no source) doesn't require `label:`.
//   3. Source-bound tree with `label:` accepted.
//   4. Source-bound tree with `label:` + `group_by:` accepted.

func TestValidateTreeSourceRequiresLabel(t *testing.T) {
	c := &Component{Type: "tree", Source: "src"}
	err := c.validate("tui.components.t")
	if err == nil || !strings.Contains(err.Error(), "`label:`") {
		t.Errorf("want label-required error, got %v", err)
	}
}

func TestValidateTreeStaticRootUnaffected(t *testing.T) {
	c := &Component{
		Type: "tree",
		Root: &TreeNode{Label: "root"},
	}
	if err := c.validate("tui.components.t"); err != nil {
		t.Errorf("static-root tree should not require label, got %v", err)
	}
}

func TestValidateTreeSourceHappyPath(t *testing.T) {
	c := &Component{
		Type:   "tree",
		Source: "src",
		Label:  Path{"name"},
	}
	if err := c.validate("tui.components.t"); err != nil {
		t.Errorf("valid source-bound tree should not error, got %v", err)
	}
}

func TestValidateTreeSourceWithGroupByHappyPath(t *testing.T) {
	c := &Component{
		Type:    "tree",
		Source:  "src",
		Label:   Path{"name"},
		GroupBy: Path{"kind"},
	}
	if err := c.validate("tui.components.t"); err != nil {
		t.Errorf("valid source-bound tree with group_by should not error, got %v", err)
	}
}
