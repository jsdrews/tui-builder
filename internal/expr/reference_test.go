package expr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReferenceDocumentsEveryFunction fails when a built-in this package
// adds to every expression isn't named on the templating reference page.
// expr-lang's own built-ins are documented by a link, not one by one.
func TestReferenceDocumentsEveryFunction(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "templating.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name := range wrapEnv(nil) {
		if !strings.Contains(string(b), "`"+name+"`") {
			t.Errorf("built-in `%s` is not named on docs/reference/templating.md", name)
		}
	}
}
