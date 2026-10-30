package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jsdrews/tui-builder/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = execute(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		err  string // a substring of stderr; "" means stderr is empty
	}{
		{nil, exitUsage, "missing config file"},
		{[]string{"--bogus"}, exitUsage, "unknown flag: --bogus"},
		{[]string{"x.yaml", "--bogus"}, exitUsage, "unknown flag"},
		{[]string{"a.yaml", "b.yaml"}, exitUsage, "at most 1 arg"},
		{[]string{"version", "extra"}, exitUsage, `unknown command "extra"`},
		{[]string{"add"}, exitUsage, "needs a kind of source"},
		{[]string{"add", "http"}, exitUsage, `unknown kind "http" for tui-builder add (exec)`},
		{[]string{"testdata-missing.yaml"}, exitError, "no such file"},
		{[]string{"version"}, exitOK, ""},
		{[]string{"--version"}, exitOK, ""},
		{[]string{"--help"}, exitOK, ""},
	} {
		code, _, stderr := run(t, tc.args...)
		if code != tc.code {
			t.Errorf("%q: exit %d, want %d (stderr %q)", tc.args, code, tc.code, stderr)
		}
		if tc.err == "" && stderr != "" || !strings.Contains(stderr, tc.err) {
			t.Errorf("%q: stderr %q, want it to contain %q", tc.args, stderr, tc.err)
		}
		if usage := strings.Contains(stderr, "--help' for usage"); usage != (tc.code == exitUsage) {
			t.Errorf("%q: usage hint shown = %v, want %v", tc.args, usage, tc.code == exitUsage)
		}
	}
}

// The run form is unchanged: one positional, the config, with flags
// accepted after it as before.
func TestRunFormGetsThePath(t *testing.T) {
	var got []string
	runForm = func(path string) error { got = append(got, path); return nil }
	t.Cleanup(func() { runForm = runConfig })
	for _, args := range [][]string{{"app.yaml"}, {"./version"}, {"version.yaml"}} {
		if code, _, stderr := run(t, args...); code != exitOK {
			t.Fatalf("%q: exit %d: %s", args, code, stderr)
		}
	}
	if want := []string{"app.yaml", "./version", "version.yaml"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("run form got %q, want %q", got, want)
	}
	runForm = func(string) error { return errors.New("boom") }
	if code, _, stderr := run(t, "app.yaml"); code != exitError || !strings.Contains(stderr, "boom") {
		t.Errorf("a failing run → exit %d %q, want 1 with the error", code, stderr)
	}
}

// A subcommand's name wins over a file of the same name.
func TestSubcommandWinsOverFile(t *testing.T) {
	called := false
	runForm = func(string) error { called = true; return nil }
	t.Cleanup(func() { runForm = runConfig })
	code, stdout, _ := run(t, "version")
	if called || code != exitOK || stdout != "tui-builder version "+version.String()+"\n" {
		t.Errorf("version → run form %v, exit %d, %q", called, code, stdout)
	}
}

func TestVersionFlagMatchesCommand(t *testing.T) {
	_, cmd, _ := run(t, "version")
	for _, f := range []string{"--version", "-v"} {
		if _, out, _ := run(t, f); out != cmd {
			t.Errorf("%s printed %q, version printed %q", f, out, cmd)
		}
	}
}

// A mistyped command fails as a missing file, with a hint.
func TestTypoSuggestsCommand(t *testing.T) {
	_, _, stderr := run(t, "versoin")
	if !strings.Contains(stderr, `Did you mean the "version" command?`) {
		t.Errorf("stderr %q has no suggestion", stderr)
	}
	if _, _, stderr := run(t, "examples/nope.yaml"); strings.Contains(stderr, "Did you mean") {
		t.Errorf("a file path got a command suggestion: %q", stderr)
	}
}

// Help shows the run form first, then the subcommands.
func TestHelpShowsRunFormFirst(t *testing.T) {
	_, out, _ := run(t, "--help")
	runAt := strings.Index(out, "tui-builder <config.yaml>")
	cmdsAt := strings.Index(out, "Available Commands:")
	if runAt < 0 || cmdsAt < 0 || runAt > cmdsAt {
		t.Errorf("help doesn't lead with the run form:\n%s", out)
	}
	if strings.Contains(out, "completion") {
		t.Error("help lists a completion command")
	}
}
