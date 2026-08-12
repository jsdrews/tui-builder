package config

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// swapStderr replaces the package's stderr sink with a buffer so
// warnings emitted by checkEnv can be captured and asserted without
// polluting the test process's actual stderr. Restore on cleanup.
func swapStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := stderr
	stderr = buf
	t.Cleanup(func() { stderr = prev })
	return buf
}

// clearEnv wipes the given var for the duration of the test and
// restores it on cleanup. Env-var tests need deterministic starting
// state; a leaked GITHUB_TOKEN in the developer's shell would make
// a required-var test spuriously pass.
func clearEnv(t *testing.T, name string) {
	t.Helper()
	prev, had := os.LookupEnv(name)
	os.Unsetenv(name)
	t.Cleanup(func() {
		if had {
			os.Setenv(name, prev)
		} else {
			os.Unsetenv(name)
		}
	})
}

func TestCheckEnvErrorsOnMissingRequired(t *testing.T) {
	clearEnv(t, "TEST_ENV_TOKEN")
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_ENV_TOKEN", Required: true, Description: "test token"},
	}}}
	err := checkEnv(c)
	if err == nil {
		t.Fatalf("want missing-required error, got nil")
	}
	if !strings.Contains(err.Error(), "TEST_ENV_TOKEN") ||
		!strings.Contains(err.Error(), "test token") {
		t.Errorf("error should name the var and its description, got: %v", err)
	}
	if !strings.Contains(err.Error(), "export TEST_ENV_TOKEN") {
		t.Errorf("error should include an export hint, got: %v", err)
	}
}

func TestCheckEnvAppliesDefaultsWhenUnset(t *testing.T) {
	clearEnv(t, "TEST_ENV_DEFAULT")
	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_ENV_DEFAULT", Default: "fallback-value"},
	}}}
	if err := checkEnv(c); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TEST_ENV_DEFAULT"); got != "fallback-value" {
		t.Errorf("default should have been applied via os.Setenv; got %q", got)
	}
}

func TestCheckEnvDoesNotOverrideExistingValue(t *testing.T) {
	os.Setenv("TEST_ENV_PRESET", "existing")
	t.Cleanup(func() { os.Unsetenv("TEST_ENV_PRESET") })

	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_ENV_PRESET", Default: "would-be-overwritten"},
	}}}
	if err := checkEnv(c); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("TEST_ENV_PRESET"); got != "existing" {
		t.Errorf("existing env value should have won over default; got %q", got)
	}
}

func TestCheckEnvWarnsOnUndeclaredReference(t *testing.T) {
	buf := swapStderr(t)
	clearEnv(t, "TEST_ENV_UNDECLARED")

	c := &Config{
		App: App{Title: "test"},
		Data: DataBlock{Sources: map[string]*Source{
			"x": {Type: "http", URL: "https://x/${env.TEST_ENV_UNDECLARED}/foo"},
		}},
	}
	if err := checkEnv(c); err != nil {
		t.Fatalf("undeclared refs should warn, not error; got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "warning:") ||
		!strings.Contains(out, "TEST_ENV_UNDECLARED") {
		t.Errorf("expected stderr warning naming the undeclared var; got: %s", out)
	}
}

func TestCheckEnvSuppressesWarningForDeclaredVars(t *testing.T) {
	buf := swapStderr(t)
	clearEnv(t, "TEST_ENV_DECLARED")

	c := &Config{
		App: App{
			Title: "test",
			Env: []EnvSpec{
				{Name: "TEST_ENV_DECLARED", Default: "x"},
			},
		},
		Data: DataBlock{Sources: map[string]*Source{
			"x": {Type: "http", URL: "https://x/${env.TEST_ENV_DECLARED}/foo"},
		}},
	}
	if err := checkEnv(c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "warning:") {
		t.Errorf("declared var should not warn; got: %s", buf.String())
	}
}

func TestCheckEnvTreatsAppPromptsAsDeclared(t *testing.T) {
	buf := swapStderr(t)
	clearEnv(t, "TEST_ENV_PROMPTED")

	c := &Config{
		App: App{
			Title:   "test",
			Prompts: []Prompt{{Key: "TEST_ENV_PROMPTED", Label: "value"}},
		},
		Data: DataBlock{Sources: map[string]*Source{
			"x": {Type: "http", URL: "https://x/${env.TEST_ENV_PROMPTED}/foo"},
		}},
	}
	if err := checkEnv(c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "warning:") {
		t.Errorf("app.prompts-supplied var should not warn (tui fills at boot); got: %s", buf.String())
	}
}

func TestCheckEnvSuppressesWarningWhenEnvVarIsActuallySet(t *testing.T) {
	buf := swapStderr(t)
	os.Setenv("TEST_ENV_ACTUALLY_SET", "yep")
	t.Cleanup(func() { os.Unsetenv("TEST_ENV_ACTUALLY_SET") })

	c := &Config{
		App: App{Title: "test"},
		Data: DataBlock{Sources: map[string]*Source{
			"x": {Type: "http", URL: "https://x/${env.TEST_ENV_ACTUALLY_SET}/foo"},
		}},
	}
	if err := checkEnv(c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "warning:") {
		t.Errorf("undeclared-but-set var should not warn (empty-string isn't the outcome); got: %s", buf.String())
	}
}

func TestCheckEnvErrorsAggregateAllMissingVars(t *testing.T) {
	clearEnv(t, "TEST_ENV_A")
	clearEnv(t, "TEST_ENV_B")
	clearEnv(t, "TEST_ENV_C")

	c := &Config{App: App{Env: []EnvSpec{
		{Name: "TEST_ENV_A", Required: true, Description: "the first"},
		{Name: "TEST_ENV_B", Required: true, Description: "the second"},
		{Name: "TEST_ENV_C", Required: true, Description: "the third"},
	}}}
	err := checkEnv(c)
	if err == nil {
		t.Fatal("expected aggregate missing-required error")
	}
	msg := err.Error()
	// All three should be present in one message so the user fixes
	// the whole batch in one edit.
	for _, want := range []string{"TEST_ENV_A", "TEST_ENV_B", "TEST_ENV_C"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected %q in aggregate error; got: %s", want, msg)
		}
	}
}

func TestValidateEnvSpecsRejectsRequiredPlusDefault(t *testing.T) {
	err := validateEnvSpecs([]EnvSpec{
		{Name: "X", Required: true, Default: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want required+default mutex error, got %v", err)
	}
}

func TestValidateEnvSpecsRejectsDuplicates(t *testing.T) {
	err := validateEnvSpecs([]EnvSpec{
		{Name: "X"},
		{Name: "X"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want duplicate error, got %v", err)
	}
}

func TestValidateEnvSpecsRejectsEmptyName(t *testing.T) {
	err := validateEnvSpecs([]EnvSpec{{}})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("want empty-name error, got %v", err)
	}
}

func TestCollectEnvRefsWalksAllTemplatedFields(t *testing.T) {
	c := &Config{
		Data: DataBlock{Sources: map[string]*Source{
			"x": {
				Type:            "http",
				URL:             "https://${env.HOST}/${env.PATH_A}",
				Body:            "${env.BODY_TOKEN}",
				Headers:         map[string]string{"Auth": "Bearer ${env.TOKEN}"},
				Command:         []string{"curl", "${env.CMD_ARG}"},
				Env:             map[string]string{"FOO": "${env.INNER_ENV}"},
				InitialMessages: []string{"${env.WS_INIT}"},
			},
		}},
		TUI: TUIBlock{
			Screen: Screen{
				Title: "${env.SCREEN_TITLE}",
				Actions: []Action{
					{Run: []string{"${env.ACTION_ARG}"}, Confirm: "${env.CONFIRM_MSG}", Notice: "${env.NOTICE_MSG}"},
				},
			},
			Components: map[string]*Component{
				"c": {Title: "${env.COMP_TITLE}", Items: []string{"${env.COMP_ITEM}"}, Lines: []string{"${env.COMP_LINE}"}},
			},
		},
	}
	refs := collectEnvRefs(c)
	wanted := []string{
		"HOST", "PATH_A", "BODY_TOKEN", "TOKEN", "CMD_ARG",
		"INNER_ENV", "WS_INIT",
		"SCREEN_TITLE", "ACTION_ARG", "CONFIRM_MSG", "NOTICE_MSG",
		"COMP_TITLE", "COMP_ITEM", "COMP_LINE",
	}
	for _, w := range wanted {
		if _, ok := refs[w]; !ok {
			t.Errorf("expected %q in refs; got %v", w, refs)
		}
	}
}
