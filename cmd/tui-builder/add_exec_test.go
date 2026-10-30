package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

// The printed config is complete: saved, it loads and names the command.
func TestAddExecPrintsCompleteConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	code, stdout, stderr := run(t, "add", "exec", "--", "sh", "-c", `echo '[{"name":"a","size":"1Ki"},{"name":"b","size":"2Mi"}]'`)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.HasPrefix(stderr, "running: sh -c ") {
		t.Errorf("stderr %q doesn't start with the running line", stderr)
	}
	path := filepath.Join(t.TempDir(), "sh.yaml")
	os.WriteFile(path, []byte(stdout), 0o644)
	c, err := cfg.Load(path)
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	src := c.Data.Sources["sh"]
	if src == nil || src.Type != "exec" || len(src.Command) != 3 || src.Timeout != "" {
		t.Errorf("source: %+v", src)
	}
	if comp := c.TUI.Components["sh"]; comp == nil || comp.Type != "table" || len(comp.Columns) != 2 || comp.Columns[1].Sort != "si" {
		t.Errorf("component: %+v", comp)
	}
}

func TestAddExecFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	for _, tc := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"add", "exec"}, exitUsage, "put the command after --"},
		{[]string{"add", "exec", "kubectl"}, exitUsage, "put the command after --"},
		{[]string{"add", "exec", "--bogus", "--", "true"}, exitUsage, "unknown flag"},
		{[]string{"add", "exec", "--", "sh", "-c", "exit 4"}, exitError, "exited with status 4"},
		{[]string{"add", "exec", "--", "sh", "-c", "echo NAME READY"}, exitError, "isn't JSON"},
		{[]string{"add", "exec", "--timeout", "100ms", "--", "sleep", "3"}, exitError, "didn't finish within 100ms"},
		{[]string{"add", "exec", "--root", "nope", "--", "sh", "-c", `echo '{"items":[{"a":1}]}'`}, exitError, "--root nope"},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != tc.code || !strings.Contains(stderr, tc.err) {
			t.Errorf("%q → %d %q, want %d with %q", tc.args, code, stderr, tc.code, tc.err)
		}
		if stdout != "" {
			t.Errorf("%q printed a config on failure", tc.args)
		}
	}
}

func TestAddExecNoRunDoesntRun(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	code, stdout, stderr := run(t, "add", "exec", "--no-run", "--timeout", "1m", "--", "touch", marker)
	if code != exitOK || strings.Contains(stderr, "running:") {
		t.Fatalf("--no-run → %d %q", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("--no-run ran the command")
	}
	if !strings.Contains(stdout, "auto: true") || !strings.Contains(stdout, "timeout: 1m0s") {
		t.Errorf("--no-run config:\n%s", stdout)
	}
}

func TestAddIsListedNow(t *testing.T) {
	if _, out, _ := run(t, "--help"); !strings.Contains(out, "add ") {
		t.Errorf("help doesn't list add:\n%s", out)
	}
	if code, _, stderr := run(t, "add"); code != exitUsage || !strings.Contains(stderr, "(exec)") {
		t.Errorf("add → %d %q", code, stderr)
	}
}

func TestAddExecShapingFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	script := `echo '[{"name":"a","status":"Running","n":1},{"name":"b","status":"Failed","n":2}]'`
	_, stdout, _ := run(t, "add", "exec", "--max-columns", "1", "--", "sh", "-c", script)
	if !strings.Contains(stdout, "# - {title: Status") || !strings.Contains(stdout, "bold: true") {
		t.Errorf("--max-columns 1:\n%s", stdout)
	}
	_, stdout, _ = run(t, "add", "exec", "--no-style", "--", "sh", "-c", script)
	if strings.Contains(stdout, "color_rules") || strings.Contains(stdout, "bold") {
		t.Errorf("--no-style:\n%s", stdout)
	}
	if code, _, stderr := run(t, "add", "exec", "--max-columns", "0", "--", "true"); code != exitUsage || !strings.Contains(stderr, "at least one") {
		t.Errorf("--max-columns 0 → %d %q", code, stderr)
	}
}

func TestAddExecShapingFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"add", "exec", "--param", "ns", "--", "true"}, exitUsage, "NAME=VALUE"},
		{[]string{"add", "exec", "--no-inputs", "--", "true"}, exitUsage, "no --param"},
		{[]string{"add", "exec", "--columns", "a,,b", "--", "true"}, exitUsage, "no path"},
		{[]string{"add", "exec", "--component", "tree", "--", "true"}, exitUsage, "want one of"},
		{[]string{"add", "exec", "--refresh", "-5s", "--", "true"}, exitUsage, "positive"},
		{[]string{"add", "exec", "--no-run", "--param", "ns=prod", "--", "kubectl", "-n", "default"}, exitError, `has no "prod"`},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != tc.code || !strings.Contains(stderr, tc.err) || stdout != "" {
			t.Errorf("%q → %d %q, want %d with %q", tc.args, code, stderr, tc.code, tc.err)
		}
	}
}

func TestAddExecSummaryOnStderr(t *testing.T) {
	code, stdout, stderr := run(t, "add", "exec", "--no-run", "--name", "pods", "--param", "ns=default", "--", "kubectl", "get", "pods", "-n", "default")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"name: pods (--name)", "component: inspector (inferred)", "input fields: 1 (--param)"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "(inferred)") {
		t.Error("the summary leaked into the config")
	}
}

func TestAddExecStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	w := scaffold.StreamWindow
	scaffold.StreamWindow = 300 * time.Millisecond
	t.Cleanup(func() { scaffold.StreamWindow = w })

	code, stdout, stderr := run(t, "add", "exec", "--", "sh", "-c", `echo '{"id":"a","level":"info"}'; echo '{"id":"b","level":"warn"}'; sleep 30`)
	if code != exitOK || !strings.Contains(stdout, "follow: true") || !strings.Contains(stdout, "row_key: id") {
		t.Fatalf("a JSON stream → %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "stream: follow: true (inferred: still running after 300ms)") {
		t.Errorf("stderr lacks the stream note:\n%s", stderr)
	}
	_, stdout, _ = run(t, "add", "exec", "--follow", "--", "sh", "-c", `echo started; sleep 30`)
	if !strings.Contains(stdout, "type: logview") || !strings.Contains(stdout, "format: text") {
		t.Errorf("a text stream:\n%s", stdout)
	}
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"add", "exec", "--follow", "--no-follow", "--", "true"}, "pick one"},
		{[]string{"add", "exec", "--follow", "--refresh", "5s", "--", "true"}, "streams"},
	} {
		if code, _, stderr := run(t, tc.args...); code != exitUsage || !strings.Contains(stderr, tc.err) {
			t.Errorf("%q → %d %q", tc.args, code, stderr)
		}
	}
}

func TestAddExecWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires sh")
	}
	// The script checks it was run with the real values, then prints a page.
	page := `[ "$1" = 0 ] && [ "$2" = 25 ] && echo '{"total": 2, "items": [{"id": 1, "name": "a"}, {"id": 2, "name": "b"}]}'`
	code, stdout, stderr := run(t, "add", "exec", "--name", "things", "--window", "offset=0", "--window", "limit=25", "--", "sh", "-c", page, "page", "0", "25")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"${window.offset}", "${window.limit}", "total_path: total", "type: table"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("config lacks %q:\n%s", want, stdout)
		}
	}
	if code, _, stderr := run(t, "add", "exec", "--follow", "--window", "offset=0", "--", "true"); code != exitUsage || !strings.Contains(stderr, "isn't paged") {
		t.Errorf("--follow --window → %d %q", code, stderr)
	}
	if code, _, stderr := run(t, "add", "exec", "--window", "page=1", "--", "true"); code != exitUsage || !strings.Contains(stderr, "window token") {
		t.Errorf("--window page=1 → %d %q", code, stderr)
	}
}

func TestAddExecInto(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	cfgText := "# mine\ntui:\n  components:\n    a: {type: list, items: [x]}\n  screen:\n    layout:\n      component: a\n"
	if err := os.WriteFile(path, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"add", "exec", "--no-run", "--name", "pods", "--columns", "metadata.name", "--into", path, "--", "kubectl", "get", "pods"}

	code, stdout, stderr := run(t, append([]string{"add", "exec", "--dry-run"}, args[2:]...)...)
	if code != exitOK || !strings.HasPrefix(stdout, "--- "+path) || !strings.Contains(stdout, "+    pods:") || !strings.Contains(stdout, "-      component: a") {
		t.Fatalf("--dry-run → %d\n%s\n%s", code, stdout, stderr)
	}
	if b, _ := os.ReadFile(path); string(b) != cfgText {
		t.Fatal("--dry-run wrote the file")
	}

	code, stdout, stderr = run(t, args...)
	if code != exitOK || stdout != "added source `pods`, table `pods` to the screen in "+path+"\n" {
		t.Fatalf("--into → %d %q %q", code, stdout, stderr)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "data:\n  sources:\n    # Added by `tui-builder add exec -- kubectl get pods`.\n    pods:") || !strings.Contains(string(b), "# mine\ntui:") {
		t.Errorf("written:\n%s", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600 kept", info.Mode().Perm())
	}
	if _, err := cfg.Load(path); err != nil {
		t.Error(err)
	}
	// Again: the name is taken, and the file is left alone.
	before, _ := os.ReadFile(path)
	if code, _, stderr := run(t, args...); code != exitError || !strings.Contains(stderr, "pass --name pods2") {
		t.Errorf("second --into → %d %q", code, stderr)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("a refused --into changed the file")
	}
	if code, _, stderr := run(t, "add", "exec", "--dry-run", "--", "true"); code != exitUsage || !strings.Contains(stderr, "go with --into") {
		t.Errorf("--dry-run without --into → %d %q", code, stderr)
	}
	if code, _, _ := run(t, "add", "exec", "--into", filepath.Join(dir, "nope.yaml"), "--no-run", "--", "true"); code != exitError {
		t.Errorf("--into a missing file → %d", code)
	}
}
