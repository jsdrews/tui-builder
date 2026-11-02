package scaffold

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// The quick start includes the golden file, so it's also what the docs
// show.
func TestStarterGolden(t *testing.T) {
	got := Starter("my-project")
	golden := filepath.Join("testdata", "starter.golden.yaml")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("starter differs from %s (go test ./internal/scaffold -update to accept):\n%s", golden, got)
	}
}

func TestStarterLoads(t *testing.T) {
	for _, title := range []string{"my-project", "My: app", "123", "yes", `say "hi"`, "#tag"} {
		path := filepath.Join(t.TempDir(), "tui-builder.yaml")
		if err := os.WriteFile(path, Starter(title), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(path)
		if err != nil {
			t.Fatalf("title %q: %v", title, err)
		}
		if c.App.Title != title {
			t.Errorf("title %q came back as %q", title, c.App.Title)
		}
	}
}

// The commented-out next steps are real sources once uncommented.
func TestStarterNextStepsParse(t *testing.T) {
	var in bool
	var block bytes.Buffer
	for _, line := range bytes.Split([]byte(starter), []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("    # users:")) || bytes.HasPrefix(line, []byte("    # pods:")):
			in = true
		case in && !bytes.HasPrefix(line, []byte("    #   ")):
			in = false
		}
		if in {
			block.Write(bytes.Replace(line, []byte("# "), nil, 1))
			block.WriteByte('\n')
		}
	}
	var sources map[string]map[string]any
	if err := yaml.Unmarshal(block.Bytes(), &sources); err != nil {
		t.Fatalf("%v\n%s", err, block.String())
	}
	if sources["users"]["type"] != "http" || sources["pods"]["type"] != "exec" {
		t.Errorf("next steps parsed as %v", sources)
	}
}
