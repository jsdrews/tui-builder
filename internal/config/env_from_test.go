package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFromValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec EnvSpec
		want string
	}{
		{"neither", EnvSpec{Name: "X", From: &EnvFrom{}}, "exactly one of `file:` or `command:`"},
		{"both", EnvSpec{Name: "X", From: &EnvFrom{File: "f", Command: []string{"true"}}}, "exactly one of"},
		{"default", EnvSpec{Name: "X", Default: "d", From: &EnvFrom{File: "f"}}, "not with `default:`"},
		{"empty program", EnvSpec{Name: "X", From: &EnvFrom{Command: []string{""}}}, "first element"},
		{"bad timeout", EnvSpec{Name: "X", From: &EnvFrom{Command: []string{"true"}, Timeout: "soon"}}, "not a positive duration"},
		{"timeout on file", EnvSpec{Name: "X", From: &EnvFrom{File: "f", Timeout: "5s"}}, "only applies to `command:`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEnvSpecs([]EnvSpec{tc.spec})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	if err := validateEnvSpecs([]EnvSpec{{Name: "X", Required: true, From: &EnvFrom{File: "f"}}}); err != nil {
		t.Errorf("required: with from: is allowed: %v", err)
	}
}

// Load must not run anything: a required from: var counts as provided,
// and its file isn't read until ResolveEnv.
func TestCheckEnvTreatsFromAsProvided(t *testing.T) {
	clearEnv(t, "TEST_FROM_REQ")
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_FROM_REQ", Required: true, From: &EnvFrom{File: "/does/not/exist"}},
	}}}
	if err := checkEnv(c); err != nil {
		t.Fatalf("checkEnv: %v", err)
	}
	if os.Getenv("TEST_FROM_REQ") != "" {
		t.Error("checkEnv set a from: var")
	}
}

func TestResolveEnvFromFile(t *testing.T) {
	clearEnv(t, "TEST_FROM_DIR")
	clearEnv(t, "TEST_FROM_REL")
	clearEnv(t, "TEST_FROM_ENVPATH")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tok"), []byte("  secret-1\n"), 0o600)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "tok"), []byte("secret-2"), 0o600)
	c := &Config{dir: dir, App: App{Env: []EnvSpec{
		{Name: "TEST_FROM_REL", From: &EnvFrom{File: "tok"}},
		{Name: "TEST_FROM_ENVPATH", From: &EnvFrom{File: "${env.TEST_FROM_DIR}/tok"}},
	}}}
	os.Setenv("TEST_FROM_DIR", filepath.Join(dir, "sub"))
	if err := c.ResolveEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TEST_FROM_REL"); got != "secret-1" {
		t.Errorf("relative file: got %q, want trimmed secret-1", got)
	}
	if got := os.Getenv("TEST_FROM_ENVPATH"); got != "secret-2" {
		t.Errorf("${env.X} in path: got %q", got)
	}
}

func TestResolveEnvFromCommand(t *testing.T) {
	clearEnv(t, "TEST_FROM_CMD")
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_FROM_CMD", From: &EnvFrom{Command: []string{"sh", "-c", "printf 'tok\\n'"}}},
	}}}
	if err := c.ResolveEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TEST_FROM_CMD"); got != "tok" {
		t.Errorf("got %q, want tok", got)
	}
}

// A value already in the environment wins; the command never runs.
func TestResolveEnvEnvironmentWins(t *testing.T) {
	t.Setenv("TEST_FROM_SET", "already")
	marker := filepath.Join(t.TempDir(), "ran")
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_FROM_SET", From: &EnvFrom{Command: []string{"sh", "-c", "touch " + marker + "; echo new"}}},
	}}}
	if err := c.ResolveEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TEST_FROM_SET"); got != "already" {
		t.Errorf("got %q, want the environment's value", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("from: command ran although the var was set")
	}
}

func TestResolveEnvFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		from *EnvFrom
		want string
	}{
		{"missing file", &EnvFrom{File: "/does/not/exist"}, "file /does/not/exist"},
		{"exit", &EnvFrom{Command: []string{"sh", "-c", "echo nope >&2; exit 3"}}, "exit status 3: nope"},
		{"empty", &EnvFrom{Command: []string{"true"}}, "empty value"},
		{"timeout", &EnvFrom{Command: []string{"sleep", "5"}, Timeout: "50ms"}, "timed out after 50ms"},
	} {
		t.Run(tc.name+"/required", func(t *testing.T) {
			clearEnv(t, "TEST_FROM_FAIL")
			swapStderr(t)
			c := &Config{App: App{Env: []EnvSpec{{Name: "TEST_FROM_FAIL", Required: true, From: tc.from}}}}
			err := c.ResolveEnv()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "TEST_FROM_FAIL") {
				t.Fatalf("want an error naming the var and %q, got %v", tc.want, err)
			}
		})
		t.Run(tc.name+"/optional", func(t *testing.T) {
			clearEnv(t, "TEST_FROM_FAIL")
			buf := swapStderr(t)
			c := &Config{App: App{Env: []EnvSpec{{Name: "TEST_FROM_FAIL", From: tc.from}}}}
			if err := c.ResolveEnv(); err != nil {
				t.Fatalf("optional failure should warn, got %v", err)
			}
			if !strings.Contains(buf.String(), "warning:") || !strings.Contains(buf.String(), tc.want) {
				t.Errorf("want a warning with %q, got %q", tc.want, buf.String())
			}
			if os.Getenv("TEST_FROM_FAIL") != "" {
				t.Error("a failed var should stay unset")
			}
		})
	}
}

// Errors and warnings name where a value came from, never the value.
func TestResolveEnvNeverPrintsValue(t *testing.T) {
	clearEnv(t, "TEST_FROM_QUIET")
	buf := swapStderr(t)
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_FROM_QUIET", From: &EnvFrom{Command: []string{"sh", "-c", "echo hunter2"}}},
	}}}
	if err := c.ResolveEnv(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Errorf("value leaked to stderr: %q", buf.String())
	}
}
