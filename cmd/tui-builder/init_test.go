package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

// chdir runs the test in a fresh directory named name.
func chdir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestInitWritesStarter(t *testing.T) {
	chdir(t, "inventory")
	code, stdout, stderr := run(t, "init")
	if code != exitOK || stdout != "run: tui-builder tui-builder.yaml\n" || stderr != "" {
		t.Fatalf("init → %d %q %q", code, stdout, stderr)
	}
	c, err := cfg.Load("tui-builder.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Title != "inventory" {
		t.Errorf("title %q, want the directory's name", c.App.Title)
	}
}

func TestInitRefusesExistingFile(t *testing.T) {
	chdir(t, "x")
	os.WriteFile("tui-builder.yaml", []byte("mine"), 0o644)
	code, _, stderr := run(t, "init")
	if code != exitError || !strings.Contains(stderr, "tui-builder.yaml already exists") ||
		!strings.Contains(stderr, "--force") || !strings.Contains(stderr, "-o") {
		t.Errorf("init over a file → %d %q", code, stderr)
	}
	if b, _ := os.ReadFile("tui-builder.yaml"); string(b) != "mine" {
		t.Error("the existing file was changed")
	}
	if code, _, stderr := run(t, "init", "--force"); code != exitOK {
		t.Fatalf("--force → %d %q", code, stderr)
	}
	if b, _ := os.ReadFile("tui-builder.yaml"); string(b) != string(scaffold.Starter("x")) {
		t.Error("--force didn't overwrite with the starter")
	}
}

func TestInitOutputAndTitle(t *testing.T) {
	dir := chdir(t, "x")
	os.Mkdir("sub", 0o755)
	code, stdout, stderr := run(t, "init", "-o", "sub/app.yaml", "--title", "My: app")
	if code != exitOK || stdout != "run: tui-builder sub/app.yaml\n" {
		t.Fatalf("init -o → %d %q %q", code, stdout, stderr)
	}
	c, err := cfg.Load(filepath.Join(dir, "sub", "app.yaml"))
	if err != nil || c.App.Title != "My: app" {
		t.Fatalf("load → %v, title %q", err, c.App.Title)
	}
	// Without --title, the title is the directory the file goes into.
	run(t, "init", "--output", "sub/b.yaml")
	if c, _ := cfg.Load("sub/b.yaml"); c == nil || c.App.Title != "sub" {
		t.Errorf("title from -o's directory: %+v", c)
	}
}

func TestInitDryRun(t *testing.T) {
	chdir(t, "demo")
	code, stdout, _ := run(t, "init", "--dry-run")
	if code != exitOK || stdout != string(scaffold.Starter("demo")) {
		t.Errorf("--dry-run → %d, stdout isn't the starter", code)
	}
	if _, err := os.Stat("tui-builder.yaml"); err == nil {
		t.Error("--dry-run wrote a file")
	}
}

func TestInitUsageErrors(t *testing.T) {
	chdir(t, "x")
	for _, args := range [][]string{{"init", "extra"}, {"init", "table", "extra"}, {"init", "--bogus"}} {
		if code, _, _ := run(t, args...); code != exitUsage {
			t.Errorf("%q → %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, _ := run(t, "init", "-o", "missing/dir/app.yaml"); code != exitError {
		t.Errorf("init into a missing directory → %d, want %d", code, exitError)
	}
}

func TestInitKind(t *testing.T) {
	chdir(t, "inventory")
	code, stdout, stderr := run(t, "init", "table", "-o", "table.yaml")
	if code != exitOK || stdout != "run: tui-builder table.yaml\n" || stderr != "" {
		t.Fatalf("init table → %d %q %q", code, stdout, stderr)
	}
	c, err := cfg.Load("table.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Title != "inventory" || c.TUI.Components["services"] == nil {
		t.Errorf("init table: title %q", c.App.Title)
	}
	want, _ := scaffold.Kind("menu", "My app", ".")
	if code, stdout, _ := run(t, "init", "menu", "--dry-run", "--title", "My app"); code != exitOK || stdout != string(want.Config) {
		t.Errorf("init menu --dry-run → %d, stdout isn't the menu example", code)
	}
}

func TestInitUnknownKind(t *testing.T) {
	chdir(t, "x")
	code, _, stderr := run(t, "init", "tabel")
	if code != exitUsage || !strings.Contains(stderr, `no kind "tabel" (did you mean table?)`) || !strings.Contains(stderr, "list, table, logview") || !strings.Contains(stderr, "the sources http, exec") {
		t.Errorf("init tabel → %d %q", code, stderr)
	}
	if _, err := os.Stat("tui-builder.yaml"); err == nil {
		t.Error("an unknown kind wrote a file")
	}
}

// init file writes the JSON beside the config, refusing first when
// either exists.
func TestInitFileWritesItsData(t *testing.T) {
	chdir(t, "x")
	os.Mkdir("sub", 0o755)
	code, stdout, stderr := run(t, "init", "file", "-o", "sub/app.yaml")
	if code != exitOK || stdout != "wrote sub/services.json\nrun: tui-builder sub/app.yaml\n" {
		t.Fatalf("init file → %d %q %q", code, stdout, stderr)
	}
	if _, err := cfg.Load("sub/app.yaml"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile("sub/services.json"); !strings.Contains(string(b), `"cron"`) {
		t.Errorf("services.json: %q", b)
	}
	os.Remove("sub/app.yaml")
	code, _, stderr = run(t, "init", "file", "-o", "sub/app.yaml")
	if code != exitError || !strings.Contains(stderr, "sub/services.json already exists") {
		t.Errorf("init file over services.json → %d %q", code, stderr)
	}
	if _, err := os.Stat("sub/app.yaml"); err == nil {
		t.Error("the refusal wrote the config")
	}
	if code, _, stderr := run(t, "init", "file", "-o", "sub/app.yaml", "--force"); code != exitOK {
		t.Errorf("--force → %d %q", code, stderr)
	}
}
