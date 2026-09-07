package main

// The action side of wrangl is deliberately read-only: --list-actions
// and --describe-action tell you what a config can do and, with
// --dry-run, exactly what a given invocation would run. There is no
// --run.
//
// That's a decision, not an omission. Every safety gate an action has —
// the confirm modal above all — is TUI state. A headless caller bypasses
// all of it, so `wrangl prod.yaml --run delete_cluster` would be one
// shell-history up-arrow away from an outage with nothing in between.
// The describe half carries most of the value anyway: it makes actions
// testable without a TTY, which is the thing you actually can't do today.

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jsdrews/tui-builder/internal/action"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// runListActions prints every action the config defines. Inline
// declarations appear here too — they're hoisted into the same registry
// at load time — so this is an honest inventory regardless of how the
// config was written.
func runListActions(configPath string) error {
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	if len(c.Actions) == 0 {
		fmt.Fprintln(os.Stdout, "no actions defined")
		return nil
	}
	names := sortedActionNames(c.Actions)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tINPUTS\tDESCRIPTION")
	for _, name := range names {
		a := c.Actions[name]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, a.Kind(), inputSummary(a), a.Description)
	}
	return w.Flush()
}

// runDescribeAction prints one action's full schema. With params bound
// it also dry-runs: the fully-substituted argv or request line, which is
// the thing that's otherwise only observable by pressing a key in a live
// TUI and watching what happens.
func runDescribeAction(configPath, target string, params map[string]string) error {
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	a, ok := c.Actions[target]
	if !ok {
		return fmt.Errorf("action %q not defined in %s (try --list-actions)", target, configPath)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\t%s\n", target)
	fmt.Fprintf(w, "KIND\t%s\n", a.Kind())
	if a.Description != "" {
		fmt.Fprintf(w, "DESCRIPTION\t%s\n", a.Description)
	}
	switch a.Kind() {
	case "exec":
		fmt.Fprintf(w, "RUN\t%s\n", strings.Join(a.Run, " "))
	case "http":
		method := a.Method
		if method == "" {
			method = "POST"
		}
		fmt.Fprintf(w, "REQUEST\t%s %s\n", method, a.URL)
	}
	if a.Success != "" {
		fmt.Fprintf(w, "SUCCESS\t%s\n", a.Success)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if len(a.Inputs) > 0 {
		fmt.Fprintln(os.Stdout, "\nINPUTS")
		iw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(iw, "  NAME\tTYPE\tMODE\tDESCRIPTION")
		for _, n := range sortedInputNames(a.Inputs) {
			p := a.Inputs[n]
			fmt.Fprintf(iw, "  %s\t%s\t%s\t%s\n", n, inputType(p), inputMode(p), p.Description)
		}
		if err := iw.Flush(); err != nil {
			return err
		}
	}

	// Dry run. Always shown — with no --param it reveals which inputs
	// resolve to empty, which is usually the bug you were looking for.
	resolved, err := action.Resolve(a, action.Inputs(params))
	if err != nil {
		return fmt.Errorf("dry run: %w", err)
	}
	fmt.Fprintln(os.Stdout, "\nDRY RUN")
	switch resolved.Kind {
	case "exec":
		fmt.Fprintf(os.Stdout, "  %s\n", strings.Join(resolved.Argv, " "))
	case "http":
		fmt.Fprintf(os.Stdout, "  %s %s\n", resolved.Method, resolved.URL)
		for _, k := range sortedKeys(resolved.Headers) {
			fmt.Fprintf(os.Stdout, "  %s: %s\n", k, resolved.Headers[k])
		}
		if resolved.Body != "" {
			fmt.Fprintf(os.Stdout, "  \n  %s\n", resolved.Body)
		}
	}
	return nil
}

// inputSummary renders an action's inputs as a one-line signature:
// required inputs get a *, defaulted ones show their default.
func inputSummary(a *cfg.Action) string {
	if len(a.Inputs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(a.Inputs))
	for _, n := range sortedInputNames(a.Inputs) {
		p := a.Inputs[n]
		switch {
		case p.Required:
			parts = append(parts, n+"*")
		case p.Default != "":
			parts = append(parts, n+"="+p.Default)
		default:
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}

func inputType(p *cfg.Parameter) string {
	if p.Type == "" {
		return "string"
	}
	return p.Type
}

func inputMode(p *cfg.Parameter) string {
	switch {
	case p.Required:
		return "required"
	case p.Default != "":
		return "default=" + p.Default
	default:
		return "optional"
	}
}

func sortedActionNames(m map[string]*cfg.Action) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedInputNames(m map[string]*cfg.Parameter) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
