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
	ds "github.com/jsdrews/tui-builder/internal/datasource"
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
			return runDump(args, listOnly, pretty, raw, limit, maxDuration, params)
		},
	}
	// pflag long flags. The names match the previous std-flag surface so
	// `task wrangl -- --list foo.yaml` and existing muscle-memory still
	// work after the migration.
	cmd.Flags().BoolVar(&listOnly, "list", false, "list every defined source / pipeline and exit")
	cmd.Flags().BoolVar(&describe, "describe", false, "print the schema (kind, lifecycle, parameters) for the given target and exit")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "indent one-shot JSON output (streams remain NDJSON)")
	cmd.Flags().BoolVar(&raw, "raw", false, "emit string / log-line values as plain text instead of JSON (useful for `format: text` and streaming logs)")
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

func runDump(args []string, listOnly, pretty, raw bool, limit int, maxDuration time.Duration, params map[string]string) error {
	configPath := args[0]
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}

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
		if p, ok := c.Pipelines[target]; ok && len(p.Parameters) > 0 {
			pipelineParams[target] = params
		} else {
			if err := applyParams(c, target, params); err != nil {
				return err
			}
		}
	} else if len(params) > 0 {
		return fmt.Errorf("--param is only valid when dumping a specific target (got --list / no target)")
	}

	live, err := ds.Build(c.DataSources)
	if err != nil {
		return fmt.Errorf("build sources: %w", err)
	}
	reg, err := pipeline.Build(live, c.DataSources, c.Pipelines, pipelineParams)
	if err != nil {
		return fmt.Errorf("build pipelines: %w", err)
	}

	// Bare `wrangl config.yaml` and `--list` both print the listing.
	// Helpful when you don't remember what's defined; matches kubectl's
	// pattern of "no args = something useful, not an error."
	if listOnly || len(args) == 1 {
		printList(os.Stdout, c, reg)
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

// runDescribe prints the schema for a single source or pipeline:
// kind, inferred lifecycle, the templated request shape (URL / Path /
// Command), and the full parameter table. Intended for "I want to
// know what knobs this target takes" introspection — the wrangl
// counterpart to a TUI form preview.
func runDescribe(configPath, target string) error {
	c, err := cfg.Load(configPath)
	if err != nil {
		return err
	}
	if src, ok := c.DataSources[target]; ok {
		describeSource(os.Stdout, target, src)
		return nil
	}
	if p, ok := c.Pipelines[target]; ok {
		srcName, err := resolveTargetSource(c, target)
		if err != nil {
			return err
		}
		describePipeline(os.Stdout, target, p, srcName, c.DataSources[srcName])
		return nil
	}
	return fmt.Errorf("no source or pipeline named %q (run `wrangl --list %s` to see what's defined)", target, configPath)
}

func describeSource(w *os.File, name string, src *cfg.DataSource) {
	fmt.Fprintf(w, "%s\n", name)
	fmt.Fprintf(w, "  Kind:       source / %s\n", src.Type)
	fmt.Fprintf(w, "  Lifecycle:  %s\n", inferLifecycle(src))
	if src.Refresh != "" {
		fmt.Fprintf(w, "  Refresh:    %s\n", src.Refresh)
	}
	if src.URL != "" {
		fmt.Fprintf(w, "  URL:        %s\n", src.URL)
	}
	if src.Path != "" {
		fmt.Fprintf(w, "  Path:       %s\n", src.Path)
	}
	if len(src.Command) > 0 {
		fmt.Fprintf(w, "  Command:    %v\n", src.Command)
	}
	if len(src.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none)")
		return
	}
	fmt.Fprintln(w, "  Parameters:")
	writeParamTable(w, src.Parameters, "    ")
}

func describePipeline(w *os.File, name string, p *cfg.Pipeline, srcName string, src *cfg.DataSource) {
	fmt.Fprintf(w, "%s\n", name)
	fmt.Fprintln(w, "  Kind:       pipeline / passthrough")
	fmt.Fprintf(w, "  Upstream:   %s (source / %s)\n", srcName, src.Type)
	if len(p.Parameters) > 0 {
		fmt.Fprintln(w, "  Pipeline parameters:")
		writeParamTable(w, p.Parameters, "    ")
	}
	fmt.Fprintf(w, "  Lifecycle:  %s\n", inferLifecycle(src))
	if len(src.Parameters) == 0 {
		fmt.Fprintln(w, "  Parameters: (none — passes through from upstream)")
		return
	}
	fmt.Fprintln(w, "  Parameters (from upstream):")
	writeParamTable(w, src.Parameters, "    ")
	_ = p // reserved — operator pipelines will add their own params here
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
// A streaming source can also need params (kube `pod_logs` follow with
// required namespace/name); a polled source can also need params
// (`pods` list refreshed every 5s but parameterized by namespace).
// We surface both so --list / --describe tell the whole truth.
func inferLifecycle(src *cfg.DataSource) string {
	cadence := "one-shot"
	switch {
	case src.Type == "websocket":
		cadence = "streamed"
	case src.Type == "exec" && src.Follow:
		cadence = "streamed (follow)"
	case src.Type == "http" && src.Follow:
		cadence = "streamed (follow)"
	case src.Refresh != "":
		cadence = "polled (refresh: " + src.Refresh + ")"
	}
	if hasUnbindableRequired(src) {
		cadence += " · needs params"
	}
	return cadence
}

func hasUnbindableRequired(src *cfg.DataSource) bool {
	for _, p := range src.Parameters {
		if p != nil && p.Required && p.Default == "" {
			return true
		}
	}
	return false
}

// applyParams finds the data source that backs `target` (either a
// direct source name, or a pipeline whose `from:` chain ends at a
// source) and binds caller-supplied parameter values onto it. The
// source's templated string fields (URL, Body, Headers, …) are
// resolved in place against the bound + default values.
//
// We resolve at the cfg level rather than after Build because the
// data layer's Source interface deliberately doesn't know about
// parameters — once a source is constructed, its templates are
// expected to be fully resolved.
func applyParams(c *cfg.Config, target string, params map[string]string) error {
	srcName, err := resolveTargetSource(c, target)
	if err != nil {
		// No backing source — likely the target itself doesn't exist
		// yet. Defer the error until runDump prints its better message.
		if len(params) == 0 {
			return nil
		}
		return err
	}
	src := c.DataSources[srcName]
	if err := src.BindParams(params); err != nil {
		return fmt.Errorf("%s: %w", srcName, err)
	}
	return nil
}

// resolveTargetSource maps a target name (source or pipeline) onto the
// underlying source whose parameters define the schema. Pipelines
// today are passthrough — `from:` either resolves to a source or to
// another pipeline; we follow the chain until we land on a source.
func resolveTargetSource(c *cfg.Config, target string) (string, error) {
	if _, ok := c.DataSources[target]; ok {
		return target, nil
	}
	p, ok := c.Pipelines[target]
	if !ok {
		return "", fmt.Errorf("target %q is not a defined source or pipeline", target)
	}
	// Walk the pipeline chain. Cycles are rejected at config load time.
	for cur := p.From; ; {
		if _, ok := c.DataSources[cur]; ok {
			return cur, nil
		}
		next, ok := c.Pipelines[cur]
		if !ok {
			return "", fmt.Errorf("pipeline %q references undefined upstream %q", target, cur)
		}
		cur = next.From
	}
}

// operatorKind labels the pipeline by which operator block it uses.
// Surfaces in the `--list` KIND column so users can see what each
// pipeline does at a glance.
func operatorKind(p *cfg.Pipeline) string {
	switch {
	case p.Filter != nil:
		return "filter"
	case p.Project != nil:
		return "project"
	case p.Derive != nil:
		return "derive"
	case p.Sort != nil:
		return "sort"
	case p.Union != nil:
		return "union"
	case p.Compose != nil:
		return "compose"
	case p.Join != nil:
		return "join"
	case p.Cache != nil:
		return "cache"
	}
	return "passthrough"
}

// printList emits a flat, scannable inventory of every defined source
// and pipeline. Columns:
//
//	NAME             KIND            LIFECYCLE        UPSTREAM (pipelines only)
//
// We deliberately leave emit-type detection for a later phase — it
// requires actually fetching from each source to inspect the value,
// which would have side effects on a `--list` call.
func printList(w *os.File, c *cfg.Config, reg *pipeline.Registry) {
	type row struct{ name, kind, lifecycle, upstream string }
	var rows []row

	for name, src := range c.DataSources {
		rows = append(rows, row{
			name:      name,
			kind:      "source / " + src.Type,
			lifecycle: lifecycleOf(src),
		})
	}
	for name, p := range c.Pipelines {
		// Walk the operator's upstream(s) to derive lifecycle + a
		// representative upstream name for the column. Operator
		// pipelines (filter, project, …) have empty p.From; use
		// UpstreamsOf which knows every operator family.
		ups := cfg.UpstreamsOf(p)
		upstream := ""
		lifecycle := "unknown"
		if len(ups) > 0 {
			upstream = ups[0]
			lifecycle = lifecycleOfRef(c, upstream)
		}
		rows = append(rows, row{
			name:      name,
			kind:      "pipeline / " + operatorKind(p),
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

// lifecycleOf is the alias --list uses so the column matches --describe.
// We deliberately read lifecycle from the config and not by introspecting
// the live source, so --list works without making network calls.
func lifecycleOf(src *cfg.DataSource) string { return inferLifecycle(src) }

// lifecycleOfRef walks pipeline → upstream edges until it lands on
// a source, then classifies that source's cadence. Handles every
// operator family (filter / project / derive / sort / union /
// compose / join / cache) via cfg.UpstreamsOf — single-input
// operators return one upstream; multi-input operators (union /
// compose) return all of them and we use the first (lifecycle is
// "polled if any child is polled"; an arbitrary representative
// works for the high-level label).
func lifecycleOfRef(c *cfg.Config, ref string) string {
	if s, ok := c.DataSources[ref]; ok {
		return lifecycleOf(s)
	}
	p, ok := c.Pipelines[ref]
	if !ok {
		return "unknown"
	}
	ups := cfg.UpstreamsOf(p)
	if len(ups) == 0 {
		return "unknown"
	}
	return lifecycleOfRef(c, ups[0])
}
