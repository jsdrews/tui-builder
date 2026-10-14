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

const presetConfig = `
data:
  presets:
    org:
      type: http
      url: https://api.example.test/v1
    api:
      extends: org
      refresh: 30s
  sources:
    issues:
      extends: api
      url: /issues
tui:
  components:
    t:
      type: table
      source: issues
      columns:
        - {title: V, width: 20, value: v}
  screen:
    layout:
      component: t
`

// A source that extends a preset describes as its merged result, with
// the chain and where each inherited field came from.
func TestRunDescribeShowsPresetOrigins(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "presets.yaml")
	write(t, cfgPath, presetConfig)

	out := captureStdout(t, func() {
		if err := runDescribe(cfgPath, "issues"); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"Extends:    api → org",
		"Refresh:    30s (from api)",
		"URL:        https://api.example.test/v1/issues\n",
		"source / http (from org)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	out = captureStdout(t, func() {
		if err := runDescribe(cfgPath, "org"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "preset / http") || !strings.Contains(out, "Extended by: issues") {
		t.Errorf("preset describe:\n%s", out)
	}
}

func TestPresetsListedAndNotRunnable(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "presets.yaml")
	write(t, cfgPath, presetConfig)

	out := captureStdout(t, func() {
		if err := runDump([]string{cfgPath}, true, false, false, false, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "PRESET") || !strings.Contains(out, "issues") {
		t.Errorf("--list:\n%s", out)
	}
	err := runDump([]string{cfgPath, "org"}, false, false, false, false, 0, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "org is a preset; run a source that extends it") {
		t.Errorf("running a preset: err = %v", err)
	}
}

// A literal secret in auth: or a secret-named header never prints;
// a template does.
func TestRunDescribeMasksLiteralSecrets(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "auth.yaml")
	write(t, cfgPath, `
data:
  sources:
    api:
      type: http
      url: https://example.test/api
      headers: {X-Api-Token: literal-hdr-secret, Accept: application/json}
      auth: {bearer: literal-bearer-secret}
    templated:
      type: http
      url: https://example.test/api
      auth: {api_key: {in: query, name: key, value: "${env.API_KEY}"}}
tui:
  components:
    t: {type: table, source: api, columns: [{title: V, width: 20, value: v}]}
  screen:
    layout: {component: t}
`)
	out := captureStdout(t, func() {
		if err := runDescribe(cfgPath, "api"); err != nil {
			t.Fatal(err)
		}
		if err := runDescribe(cfgPath, "templated"); err != nil {
			t.Fatal(err)
		}
	})
	for _, leak := range []string{"literal-hdr-secret", "literal-bearer-secret"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q printed:\n%s", leak, out)
		}
	}
	for _, want := range []string{"Auth:       bearer •••", "X-Api-Token: •••", "Accept: application/json", "api_key query key = ${env.API_KEY}"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
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
