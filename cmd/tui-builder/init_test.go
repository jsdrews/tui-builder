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
	for _, args := range [][]string{{"init", "extra"}, {"init", "--bogus"}} {
		if code, _, _ := run(t, args...); code != exitUsage {
			t.Errorf("%q → %d, want %d", args, code, exitUsage)
		}
	}
	if code, _, _ := run(t, "init", "-o", "missing/dir/app.yaml"); code != exitError {
		t.Errorf("init into a missing directory → %d, want %d", code, exitError)
	}
}
