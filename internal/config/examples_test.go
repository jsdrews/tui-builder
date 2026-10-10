package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The docs site embeds every example verbatim, so an example that stops
// loading is a broken page as well as a broken example.
func TestEveryExampleLoads(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			// Required env vars are the user's to supply; stand in for
			// them so the schema is what's under test.
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var head struct {
				App struct {
					Env []EnvSpec `yaml:"env"`
				} `yaml:"app"`
			}
			if err := yaml.Unmarshal(b, &head); err != nil {
				t.Fatal(err)
			}
			for _, e := range head.App.Env {
				if e.Required && os.Getenv(e.Name) == "" {
					t.Setenv(e.Name, "example")
				}
			}
			if _, err := Load(f); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// docs/examples.md is the site's gallery. A new example should show up
// there, and the snippet include is what keeps its content current.
func TestExamplesGalleryListsEveryExample(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	for _, f := range files {
		include := `--8<-- "examples/` + filepath.Base(f) + `"`
		if !strings.Contains(page, include) {
			t.Errorf("docs/examples.md doesn't include %s", filepath.Base(f))
		}
	}
}
