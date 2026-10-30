package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestDocsListEveryFlag fails when a tui-builder subcommand has no
// section, or a flag no row, on docs/cli/tui-builder.md, so neither can
// ship undocumented.
func TestDocsListEveryFlag(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli", "tui-builder.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	var visit func(*cobra.Command)
	visit = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" {
			return
		}
		if c.HasParent() && !strings.Contains(doc, "## `"+c.CommandPath()+"`") {
			t.Errorf("%q has no `## `%s`` section on docs/cli/tui-builder.md", c.CommandPath(), c.CommandPath())
		}
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			if !strings.Contains(doc, "`--"+f.Name) {
				t.Errorf("%s --%s is not documented on docs/cli/tui-builder.md", c.CommandPath(), f.Name)
			}
			if f.Shorthand != "" && !strings.Contains(doc, "`-"+f.Shorthand+"`") {
				t.Errorf("%s -%s (--%s) is not documented on docs/cli/tui-builder.md", c.CommandPath(), f.Shorthand, f.Name)
			}
		})
		for _, sub := range c.Commands() {
			visit(sub)
		}
	}
	root := newRoot()
	// Cobra adds --version (and --help) when the command line is
	// parsed; do it now so the root's flags are all there to check.
	root.InitDefaultVersionFlag()
	root.InitDefaultHelpFlag()
	visit(root)
}
