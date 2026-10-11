package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestDocsListEveryFlag fails when a wrangl flag has no row on
// docs/cli/wrangl.md, so a new flag can't ship undocumented.
func TestDocsListEveryFlag(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli", "wrangl.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	newRoot().Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		if !strings.Contains(doc, "`--"+f.Name) {
			t.Errorf("--%s is not documented on docs/cli/wrangl.md", f.Name)
		}
		if f.Shorthand != "" && !strings.Contains(doc, "`-"+f.Shorthand+"`") {
			t.Errorf("-%s (--%s) is not documented on docs/cli/wrangl.md", f.Shorthand, f.Name)
		}
	})
}
