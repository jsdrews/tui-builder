package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func claimConfig(mutate func(*Config)) *Config {
	return activityConfig(func(c *Config) {
		c.Actions = map[string]*Action{"restart": {
			Run:    []string{"kubectl", "rollout", "restart", "${inputs.name}"},
			Inputs: map[string]*Parameter{"name": {Required: true}},
			Claim:  &Claim{Label: "Restarting"},
		}}
		c.TUI.Screen.Actions = []ActionBinding{{Key: "R", Action: "restart", From: "pods",
			Bind: map[string]string{"name": "${selection.Name}"}}}
		if mutate != nil {
			mutate(c)
		}
	})
}

func TestClaimValidation(t *testing.T) {
	claim := func(c *Config) *Claim { return c.Actions["restart"].Claim }
	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"held", nil, ""},
		{"observed with settle", func(c *Config) { claim(c).Until = "status"; claim(c).Settle = 2 }, ""},
		{"no label", func(c *Config) { claim(c).Label = "" }, "`label:` is required"},
		{"unknown until", func(c *Config) { claim(c).Until = "later" }, "unknown \"later\""},
		{"settle without status", func(c *Config) { claim(c).Settle = 2 }, "only applies with `until: status`"},
		{"no from", func(c *Config) {
			c.TUI.Screen.Actions[0].From = ""
			c.TUI.Screen.Actions[0].Bind = map[string]string{"name": "x"}
		}, "needs `from:`"},
		{"interactive", func(c *Config) { yes := true; c.TUI.Screen.Actions[0].Interactive = &yes }, "can't follow an interactive run"},
		{"from has no activity", func(c *Config) { c.TUI.Components["pods"].Activity = nil; c.TUI.Components["pods"].Markable = true }, "has no `activity:`"},
		{"push", func(c *Config) {
			c.Actions["open"] = &Action{Push: "detail", Claim: &Claim{Label: "x"}}
		}, "there are no rows for it to claim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := claimConfig(tc.mutate).Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestClaimShorthandAndSettle(t *testing.T) {
	var a struct {
		Short Claim `yaml:"short"`
		Long  Claim `yaml:"long"`
	}
	if err := yaml.Unmarshal([]byte("short: Restarting\nlong: {label: Running, until: status}\n"), &a); err != nil {
		t.Fatal(err)
	}
	if a.Short.Label != "Restarting" || a.Short.IsObserved() {
		t.Errorf("shorthand decoded as %+v, want a held claim labelled Restarting", a.Short)
	}
	if !a.Long.IsObserved() || a.Long.SettleReads() != 1 {
		t.Errorf("until: status should default to settle 1, got %+v (%d)", a.Long, a.Long.SettleReads())
	}

	if got := (&Claim{Label: "Running", Until: "status", Settle: 3}).SettleReads(); got != 3 {
		t.Errorf("explicit settle = %d, want 3", got)
	}
	if got := a.Short.SettleReads(); got != 0 {
		t.Errorf("an until: done claim has no settle, got %d", got)
	}
}
