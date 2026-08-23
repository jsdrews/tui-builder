package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRunDumpSubstitutesEnv is the regression test for a bug that bit
// repeatedly: `${env.X}` resolved under `tui-builder` but came through
// literally under `wrangl`, so the same config behaved differently
// depending on which binary opened it.
//
// The cause was structural. The substitution helper lived in
// internal/build (the TUI layer), which cmd/wrangl is forbidden to
// import, so wrangl had no way to call it — while the load-time
// *warning* about unset env vars did fire, which made it look like the
// machinery was running. This test asserts at the level that broke: the
// bytes wrangl actually writes.
func TestRunDumpSubstitutesEnv(t *testing.T) {
	t.Setenv("WRANGL_TEST_VALUE", "resolved")

	cfgPath := filepath.Join(t.TempDir(), "env.yaml")
	// A static source would skip the templated fields entirely, so use
	// exec: the token has to survive into argv for this to pass.
	write(t, cfgPath, `
app:
  title: Env regression
data:
  sources:
    who:
      type: exec
      command: [sh, -c, "printf '[{\"v\":\"%s\"}]' '${env.WRANGL_TEST_VALUE}'"]
tui:
  components:
    t:
      type: table
      source: who
      columns:
        - {title: V, width: 20, value: v}
  screen:
    layout:
      component: t
`)

	out := captureStdout(t, func() {
		if err := runDump([]string{cfgPath, "who"}, false, false, false, false, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
	})

	if strings.Contains(out, "${env.") {
		t.Errorf("wrangl emitted an unresolved env token:\n%s", out)
	}
	if !strings.Contains(out, "resolved") {
		t.Errorf("env var did not reach the command; got:\n%s", out)
	}
}

// An `app.env` default must reach the output too — Load applies it with
// os.Setenv, and SubstituteEnv has to run after that to pick it up.
func TestRunDumpAppliesAppEnvDefault(t *testing.T) {
	t.Setenv("WRANGL_TEST_DEFAULTED", "")

	cfgPath := filepath.Join(t.TempDir(), "env.yaml")
	write(t, cfgPath, `
app:
  title: Env default
  env:
    - name: WRANGL_TEST_DEFAULTED
      default: "from-default"
data:
  sources:
    who:
      type: exec
      command: [sh, -c, "printf '[{\"v\":\"%s\"}]' '${env.WRANGL_TEST_DEFAULTED}'"]
tui:
  components:
    t:
      type: table
      source: who
      columns:
        - {title: V, width: 20, value: v}
  screen:
    layout:
      component: t
`)

	out := captureStdout(t, func() {
		if err := runDump([]string{cfgPath, "who"}, false, false, false, false, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "from-default") {
		t.Errorf("app.env default never reached the command; got:\n%s", out)
	}
}

// --describe is the deliberate exception: it reports the templated shape
// of a request, so the token stays, and whatever secret an env var holds
// never gets printed.
func TestRunDescribeKeepsTemplateShape(t *testing.T) {
	t.Setenv("WRANGL_TEST_SECRET", "hunter2")

	cfgPath := filepath.Join(t.TempDir(), "env.yaml")
	write(t, cfgPath, `
app:
  title: Describe
data:
  sources:
    api:
      type: http
      url: https://example.test/api?key=${env.WRANGL_TEST_SECRET}
tui:
  components:
    t:
      type: table
      source: api
      columns:
        - {title: V, width: 20, value: v}
  screen:
    layout:
      component: t
`)

	out := captureStdout(t, func() {
		if err := runDescribe(cfgPath, "api"); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "hunter2") {
		t.Errorf("--describe expanded an env var into its output:\n%s", out)
	}
	if !strings.Contains(out, "${env.WRANGL_TEST_SECRET}") {
		t.Errorf("--describe should show the template shape; got:\n%s", out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureStdout swaps os.Stdout for a pipe while fn runs. wrangl writes
// to os.Stdout directly rather than an injected io.Writer, so this is
// the seam available without reshaping the CLI.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	func() {
		defer func() {
			os.Stdout = orig
			w.Close()
		}()
		fn()
	}()

	select {
	case out := <-done:
		return out
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading captured stdout")
		return ""
	}
}
