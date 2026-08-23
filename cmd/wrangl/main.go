// Command wrangl is the data-layer-only entry point for tui-builder.
//
// Usage:
//
//	wrangl <config.yaml> [pipeline-or-source-name] [flags]
//
// pflag (via cobra) accepts flags in any position — `wrangl
// examples/foo.yaml --list` and `wrangl --list examples/foo.yaml`
// both work.
//
// Examples:
//
//	wrangl --list examples/http_countries.yaml
//	wrangl examples/http_countries.yaml all_countries
//	wrangl --limit 5 examples/stream_l1.yaml l1
//	wrangl --pretty examples/http_countries.yaml all_countries
//
// With a name, wrangl runs that pipeline (or source) and emits its
// result to stdout: JSON for a one-shot polled source, NDJSON (one
// JSON value per line) for a stream. Designed to pipe into jq,
// duckdb, miller, or any line-oriented downstream.
//
// With no name, wrangl prints the same listing as --list: every
// defined source and pipeline, its kind, and its lifecycle. Useful
// when you've inherited a config and want to know what's available.
//
// wrangl deliberately does NOT import internal/screen, internal/build,
// or any tuilib package. The data layer is architecturally
// independent of the TUI — running wrangl never compiles, links, or
// drags in any terminal-UI code. A CI check enforces this boundary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	cfg "github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/output"
	"github.com/jsdrews/tui-builder/internal/pipeline"
)

func main() {
	// Cobra returns the error from RunE; it has already been printed
	// via SilenceErrors=false behavior. We just need the right exit code.
	if err := newRoot().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	var (
		listOnly    bool
		describe    bool
		pretty      bool
		raw         bool
		all         bool
		limit       int
		maxDuration time.Duration
		paramArgs   []string
	)
	cmd := &cobra.Command{
		Use:   "wrangl <config.yaml> [pipeline-or-source-name]",
		Short: "Run the data layer of a tui-builder config; dump JSON / NDJSON to stdout.",
		Long: `wrangl runs only the data layer of a tui-builder config — sources and
pipelines — and dumps the result to stdout. No TUI is loaded.

With no target name, wrangl prints the inventory of every defined
source and pipeline. With a name, it dumps that target: pretty JSON
for one-shot sources, NDJSON (one frame per line) for streaming
sources. Streams are designed to pipe straight into jq, duckdb,
miller, or any other line-oriented downstream.

The data layer is architecturally independent of the TUI — running
wrangl never compiles, links, or drags in any terminal-UI code.`,
		Example: `  # List every defined source and pipeline
  wrangl --list examples/http_countries.yaml

  # Dump a pipeline as pretty JSON
  wrangl --pretty examples/http_countries.yaml all_countries

  # Pipe into jq
  wrangl examples/http_countries.yaml all_countries | jq '.[0].name.common'

  # Cap a stream at N events or a duration
  wrangl --limit 5 examples/stream_l1.yaml l1
  wrangl --for 10s examples/stream_websocket.yaml trades

  # Bind parameters on a parameterized source (repeatable)
  wrangl examples/params_demo.yaml posts_by_user --param user_id=1
  wrangl examples/params_demo.yaml posts_by_user --param user_id=3 --param limit=10`,
		Args:          cobra.MaximumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			params, err := parseParams(paramArgs)
			if err != nil {
				return err
			}
			if describe {
				if len(args) < 2 {
					return fmt.Errorf("--describe requires a target name")
				}
				return runDescribe(args[0], args[1])
			}
			return runDump(args, listOnly, pretty, raw, all, limit, maxDuration, params)
		},
	}
	// pflag long flags. The names match the previous std-flag surface so
	// `task wrangl -- --list foo.yaml` and existing muscle-memory still
	// work after the migration.
	cmd.Flags().BoolVar(&listOnly, "list", false, "list every defined source / pipeline and exit")
	cmd.Flags().BoolVar(&describe, "describe", false, "print the schema (kind, lifecycle, parameters) for the given target and exit")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent one-shot JSON output (streams remain NDJSON)")
	cmd.Flags().BoolVar(&raw, "raw", false, "emit string / log-line values as plain text instead of JSON (useful for `format: text` and streaming logs)")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include hidden sources and pipelines (those whose name starts with `_`) in --list")
	cmd.Flags().IntVar(&limit, "limit", 0, "cap stream output at N events, then exit (0 = no cap)")
	cmd.Flags().DurationVar(&maxDuration, "for", 0, "cap stream consumption at this duration, e.g. 5s, 1m (0 = no cap)")
	cmd.Flags().StringSliceVar(&paramArgs, "param", nil, "bind a source parameter (repeatable): --param key=value")
	return cmd
}

// parseParams splits each `key=value` form into a map. Empty key,
// missing `=`, or duplicate keys are errors so silent typos can't
// produce misleading dumps.
func parseParams(args []string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for _, a := range args {
		i := strings.IndexByte(a, '=')
		if i <= 0 {
			return nil, fmt.Errorf("--param %q: expected key=value (got no `=` or empty key)", a)
		}
		k := a[:i]
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--param %q: %q given more than once", a, k)
		}
		out[k] = a[i+1:]
	}
	return out, nil
}

func runDump(args []string, listOnly, pretty, raw, all bool, limit int, maxDuration time.Duration, params map[string]string) error {
	configPath := args[0]
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	// Resolve ${env.*} before anything reads a URL or an argv. The TUI
	// does this after its prompts run; wrangl has no prompts, so the
	// environment is already final here. Without it, a config that works
	// under `tui-builder` would fetch a literal "${env.HOST}/api" here —
	// the same config behaving differently depending on which binary
	// opened it.
	c.SubstituteEnv()

	// Route --param: if the target is a PIPELINE with its own
	// `parameters:` block, bind to the pipeline (its operator
	// expressions see them as `params.X`). Otherwise fall through to
	// the original source-binding behavior — params get substituted
	// into URL/Body/Headers/etc. before Build.
	//
	// This means pipelines with params shadow upstream sources for
	// --param purposes — backwards-compatible with single-target
	// source configs while letting parameterized pipelines do their
	// thing. Wire-level forwarding (pipeline params → upstream source
	// params) is a future feature, not in this slice.
	pipelineParams := map[string]map[string]string{}
	if !listOnly && len(args) >= 2 {
		target := args[1]
		// If the target is an OPERATOR entry that declares its own
		// `parameters:`, bind there (its expressions see params.X).
		// Otherwise walk through to the underlying leaf and bind the
		// caller's params onto its template fields before Build.
		s, ok := c.Data.Sources[target]
		if ok && !s.IsLeaf() && len(s.Parameters) > 0 {
			pipelineParams[target] = params
		} else if err := applyParams(c, target, params); err != nil {
			return err
		}
	} else if len(params) > 0 {
		return fmt.Errorf("--param is only valid when dumping a specific target (got --list / no target)")
	}

	reg, err := pipeline.Build(nil, c.Data.Sources, pipelineParams)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}

	// Bare `wrangl config.yaml` and `--list` both print the listing.
	// Helpful when you don't remember what's defined; matches kubectl's
	// pattern of "no args = something useful, not an error."
	if listOnly || len(args) == 1 {
		printList(os.Stdout, c, reg, all)
		return nil
	}

	name := args[1]
	src := reg.Get(name)
	if src == nil {
		return fmt.Errorf("no source or pipeline named %q (run `wrangl --list %s` to see what's defined)", name, configPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return output.Run(ctx, src, os.Stdout, output.Options{
		Pretty:      pretty,
		Raw:         raw,
		Limit:       limit,
		MaxDuration: maxDuration,
	})
}

// runDescribe prints the schema for a single entry: kind, inferred
// lifecycle, the templated request shape (URL / Path / Command for
// leaves), and the full parameter table. Intended for "I want to
// know what knobs this target takes" introspection — the wrangl
// counterpart to a TUI form preview.
func runDescribe(configPath, target string) error {
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	// Deliberately NOT SubstituteEnv'd: --describe reports the templated
	// shape of a request, so `${env.AWX_HOST}/api/v2/jobs/` is the useful
	// answer, and resolving it would print whatever secret an env var
	// holds into the terminal.
	s, ok := c.Data.Sources[target]
	if !ok {
		return fmt.Errorf("no entry named %q (run `wrangl --list %s` to see what's defined)", target, configPath)
	}
	if s.IsLeaf() {
		describeLeaf(os.Stdout, target, s)
	} else {
		describeOperator(os.Stdout, target, s, c)
	}
	return nil
}

func describeLeaf(w *os.File, name string, s *cfg.Source) {
	fmt.Fprintf(w, "%s\n", name)
	fmt.Fprintf(w, "  Kind:       source / %s\n", s.Type)
	fmt.Fprintf(w, "  Lifecycle:  %s\n", inferLifecycle(s))
	if s.Refresh != "" {
		fmt.Fprintf(w, "  Refresh:    %s\n", s.Refresh)
	}
	if s.URL != "" {
		fmt.Fprintf(w, "  URL:        %s\n", s.URL)
	}
	if s.Path != "" {
		fmt.Fprintf(w, "  Path:       %s\n", s.Path)
	}
	if len(s.Command) > 0 {
		fmt.Fprintf(w, "  Command:    %v\n", s.Command)
	}
	if len(s.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none)")
		return
	}
	fmt.Fprintln(w, "  Parameters:")
	writeParamTable(w, s.Parameters, "    ")
}

func describeOperator(w *os.File, name string, s *cfg.Source, c *cfg.Config) {
	fmt.Fprintf(w, "%s\n", name)
	fmt.Fprintf(w, "  Kind:       pipeline / %s\n", s.Type)
	srcName := resolveTargetLeaf(c, name)
	if srcName != "" {
		if leaf, ok := c.Data.Sources[srcName]; ok {
			fmt.Fprintf(w, "  Upstream:   %s (source / %s)\n", srcName, leaf.Type)
		}
	}
	if len(s.Parameters) > 0 {
		fmt.Fprintln(w, "  Pipeline parameters:")
		writeParamTable(w, s.Parameters, "    ")
	}
	fmt.Fprintf(w, "  Lifecycle:  %s\n", lifecycleOfRef(c, name))
	if srcName == "" {
		return
	}
	leaf, ok := c.Data.Sources[srcName]
	if !ok {
		return
	}
	if len(leaf.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none — passes through from upstream)")
		return
	}
	fmt.Fprintln(w, "  Parameters (from upstream):")
	writeParamTable(w, leaf.Parameters, "    ")
}

// writeParamTable renders the parameter map as an aligned table. We
// sort by name so the same config produces the same output on every
// run (map iteration order would otherwise drift between platforms).
func writeParamTable(w *os.File, params map[string]*cfg.Parameter, indent string) {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)

	nw, tw, mw := 0, 0, 0
	for _, n := range names {
		if len(n) > nw {
			nw = len(n)
		}
		t := paramType(params[n])
		if len(t) > tw {
			tw = len(t)
		}
		m := paramMode(params[n])
		if len(m) > mw {
			mw = len(m)
		}
	}
	for _, n := range names {
		p := params[n]
		fmt.Fprintf(w, "%s%-*s   %-*s   %-*s   %s\n",
			indent, nw, n, tw, paramType(p), mw, paramMode(p), p.Description)
	}
}

func paramType(p *cfg.Parameter) string {
	if p.Type == "" {
		return "string"
	}
	return p.Type
}

// paramMode renders the required / default / optional column.
//   - REQUIRED       → caller must bind
//   - default: "5"   → optional with declared default
//   - optional       → optional, no default (substitutes to empty)
func paramMode(p *cfg.Parameter) string {
	if p.Required {
		return "REQUIRED"
	}
	if p.Default != "" {
		return "default: " + strconv.Quote(p.Default)
	}
	return "optional"
}

// inferLifecycle returns a one-line classification combining two
// orthogonal facts:
//
//   - Cadence: streamed / polled / one-shot — how the source emits
//     values over time (event-driven, clock-driven, or single fetch).
//   - Binding: self-contained / needs params — whether required
//     parameters must be bound by a caller before the source can run.
//
// A streaming source can also need params (kube `pod_logs` follow
// with required namespace/name); a polled source can also need params
// (`pods` list refreshed every 5s but parameterized by namespace).
// We surface both so --list / --describe tell the whole truth.
//
// Operator entries don't have an inherent cadence — they delegate to
// their upstream. Lifecycle for those is computed by lifecycleOfRef.
func inferLifecycle(s *cfg.Source) string {
	cadence := "one-shot"
	switch s.Type {
	case "websocket":
		cadence = "streamed"
	case "exec", "http":
		if s.Follow {
			cadence = "streamed (follow)"
		} else if s.Refresh != "" {
			cadence = "polled (refresh: " + s.Refresh + ")"
		}
	case "file", "merge":
		if s.Refresh != "" {
			cadence = "polled (refresh: " + s.Refresh + ")"
		}
	}
	if hasUnbindableRequired(s.Parameters) {
		cadence += " · needs params"
	}
	return cadence
}

func hasUnbindableRequired(params map[string]*cfg.Parameter) bool {
	for _, p := range params {
		if p != nil && p.Required && p.Default == "" {
			return true
		}
	}
	return false
}

// applyParams binds caller-supplied parameter values onto the leaf
// source that backs target. Pipelines walk to their underlying leaf
// via `from:` chains. Templated string fields (URL, Body, Headers,
// Command, …) are resolved in place on a cloned source stored back
// into c.Data.Sources so the data-layer builders see fully-resolved
// templates.
func applyParams(c *cfg.Config, target string, params map[string]string) error {
	if len(params) == 0 {
		// No caller binding requested. Leave the leaf alone — its
		// templated fields stay as-is so the per-row join lookup
		// can clone+bind them later, or so the fetch surfaces a
		// clear unresolved-template error.
		return nil
	}
	leafName := resolveTargetLeaf(c, target)
	if leafName == "" {
		return fmt.Errorf("target %q is not a defined entry", target)
	}
	leaf := c.Data.Sources[leafName]
	cloned := leaf.Clone()
	if err := cloned.BindParams(params); err != nil {
		return fmt.Errorf("%s: %w", leafName, err)
	}
	c.Data.Sources[leafName] = cloned
	return nil
}

// resolveTargetLeaf walks from target through `from:` references
// until it lands on a leaf entry; returns the leaf's name. Empty
// string when no leaf is reachable. Cycles are rejected at config
// load time so the recursion always terminates.
func resolveTargetLeaf(c *cfg.Config, target string) string {
	visited := map[string]bool{}
	for cur := target; cur != ""; {
		if visited[cur] {
			return ""
		}
		visited[cur] = true
		s, ok := c.Data.Sources[cur]
		if !ok {
			return ""
		}
		if s.IsLeaf() {
			return cur
		}
		// Operator — follow its primary upstream.
		ups := s.Upstreams()
		if len(ups) == 0 {
			return ""
		}
		cur = ups[0]
	}
	return ""
}

// printList emits a flat, scannable inventory of every defined source
// and pipeline. Columns:
//
//	NAME             KIND            LIFECYCLE        UPSTREAM (pipelines only)
//
// By convention, names prefixed with `_` are treated as "hidden" —
// they're still fully callable (`wrangl <config> _internal` works),
// but they don't clutter the listing. Pass `showAll` (--all / -a) to
// reveal them.
//
// We deliberately leave emit-type detection for a later phase — it
// requires actually fetching from each source to inspect the value,
// which would have side effects on a `--list` call.
func printList(w *os.File, c *cfg.Config, reg *pipeline.Registry, showAll bool) {
	type row struct{ name, kind, lifecycle, upstream string }
	var rows []row

	add := func(r row) {
		if !showAll && strings.HasPrefix(r.name, "_") {
			return
		}
		rows = append(rows, r)
	}

	for name, s := range c.Data.Sources {
		if s.IsLeaf() {
			add(row{
				name:      name,
				kind:      "source / " + s.Type,
				lifecycle: inferLifecycle(s),
			})
			continue
		}
		// Operator — pick a representative upstream name for the
		// column and follow it to find a lifecycle.
		ups := s.Upstreams()
		upstream := ""
		lifecycle := "unknown"
		if len(ups) > 0 {
			upstream = ups[0]
			lifecycle = lifecycleOfRef(c, upstream)
		}
		add(row{
			name:      name,
			kind:      "pipeline / " + s.Type,
			lifecycle: lifecycle,
			upstream:  upstream,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	// Column widths.
	nw, kw, lw := len("NAME"), len("KIND"), len("LIFECYCLE")
	for _, r := range rows {
		if len(r.name) > nw {
			nw = len(r.name)
		}
		if len(r.kind) > kw {
			kw = len(r.kind)
		}
		if len(r.lifecycle) > lw {
			lw = len(r.lifecycle)
		}
	}
	fmt.Fprintf(w, "%-*s   %-*s   %-*s   %s\n", nw, "NAME", kw, "KIND", lw, "LIFECYCLE", "UPSTREAM")
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s   %-*s   %-*s   %s\n", nw, r.name, kw, r.kind, lw, r.lifecycle, r.upstream)
	}
	_ = reg // reserved — future --list will use the Registry for richer info (emitted types after Phase 2 operators land).
}

// lifecycleOfRef walks operator → upstream edges until it lands on
// a leaf entry, then classifies that leaf's cadence. Multi-input
// operators (union / compose) return all their children; we use the
// first (lifecycle is "polled if any child is polled"; an arbitrary
// representative works for the high-level label).
func lifecycleOfRef(c *cfg.Config, ref string) string {
	s, ok := c.Data.Sources[ref]
	if !ok {
		return "unknown"
	}
	if s.IsLeaf() {
		return inferLifecycle(s)
	}
	ups := s.Upstreams()
	if len(ups) == 0 {
		return "unknown"
	}
	return lifecycleOfRef(c, ups[0])
}
