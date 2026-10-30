package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jsdrews/tui-builder/internal/columns"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

func newAddExecCmd() *cobra.Command {
	var (
		noRun      bool
		timeout    time.Duration
		root       string
		maxColumns int
		noStyle    bool
		params     []string
		noInputs   bool
		cols       string
		component  string
		name       string
		title      string
		refresh    time.Duration
		follow     bool
		noFollow   bool
		windows    []string
		totalPath  string
		cursorPath string
		growing    bool
		every      time.Duration
		into       string
		place      string
		dryRun     bool
	)
	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command> [args…]",
		Short: "Run a command, sample its JSON, and print a config that shows it.",
		Long: `exec runs the command once, reads the JSON it prints, and prints a
complete config: an exec source for the command and a table (or, for a
single object, an inspector) that shows it.

The command goes after --, exactly as it should run: no shell, so pipes
and redirects need an explicit sh -c "…".`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || cmd.ArgsLenAtDash() != 0 {
				return usagef("put the command after --: tui-builder add exec -- kubectl get pods -o json")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, argv []string) error {
			if into == "" && (place != "" || dryRun) {
				return usagef("--place and --dry-run go with --into")
			}
			if place != "" && !slices.Contains(scaffold.Places, place) {
				return usagef("--place %s: want one of %s", place, strings.Join(scaffold.Places, ", "))
			}
			var old []byte
			if into != "" {
				b, err := os.ReadFile(into)
				if err != nil {
					return err
				}
				old = b
			}
			if maxColumns < 1 {
				return usagef("--max-columns %d: keep at least one column", maxColumns)
			}
			opts := scaffold.ExecOptions{
				Argv: argv, Timeout: timeout, Root: root, MaxColumns: maxColumns, NoStyle: noStyle,
				NoInputs: noInputs, Component: component, Name: name, Title: title, Refresh: refresh,
			}
			for _, s := range params {
				p, err := scaffold.ParseParam(s)
				if err != nil {
					return usageError{err}
				}
				opts.Params = append(opts.Params, p)
			}
			if noInputs && len(params) == 0 {
				return usagef("--no-inputs: there's no --param to leave without an input field")
			}
			if cols != "" {
				specs, err := scaffold.ParseColumns(cols)
				if err != nil {
					return usageError{err}
				}
				opts.Columns = specs
			}
			if component != "" && !slices.Contains(scaffold.Components, component) {
				return usagef("--component %s: want one of %s", component, strings.Join(scaffold.Components, ", "))
			}
			opts.Window = scaffold.WindowOptions{TotalPath: totalPath, CursorPath: cursorPath, Growing: growing, FollowEvery: every}
			for _, s := range windows {
				m, err := scaffold.ParseWindow(s)
				if err != nil {
					return usageError{err}
				}
				opts.Window.Marks = append(opts.Window.Marks, m)
			}
			if follow && (len(windows) > 0 || growing) {
				return usagef("--follow and a window: a stream isn't paged")
			}
			if follow && noFollow {
				return usagef("--follow and --no-follow: pick one")
			}
			switch {
			case follow:
				opts.Follow = scaffold.FollowYes
			case noFollow:
				opts.Follow = scaffold.FollowNo
			}
			if follow && refresh > 0 {
				return usagef("--refresh polls a command that finishes, and --follow says this one streams")
			}
			if refresh < 0 {
				return usagef("--refresh %s: want a positive interval, like 30s", refresh)
			}
			var res scaffold.Result
			var err error
			if noRun {
				res, err = scaffold.ExecConfigNoRun(opts)
			} else {
				sample := scaffold.SampleArgv(argv)
				fmt.Fprintln(cmd.ErrOrStderr(), "running:", scaffold.ShellJoin(sample))
				ctx, stop := signal.NotifyContext(cmdContext(cmd), os.Interrupt)
				defer stop()
				var out scaffold.Output
				if out, err = scaffold.Run(ctx, sample, timeout, opts.Follow); err != nil {
					return err
				}
				res, err = scaffold.ExecConfig(opts, out)
			}
			if err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
			}
			for _, line := range res.Summary {
				fmt.Fprintln(cmd.ErrOrStderr(), "  "+line)
			}
			if into == "" {
				_, err := cmd.OutOrStdout().Write(res.Config)
				return err
			}
			sp, err := scaffold.SpliceInto(old, res, place, filepath.Dir(into))
			if err != nil {
				return fmt.Errorf("%s: %w", into, err)
			}
			for _, n := range sp.Notes {
				fmt.Fprintln(cmd.ErrOrStderr(), "note:", n)
			}
			if dryRun {
				fmt.Fprint(cmd.OutOrStdout(), scaffold.UnifiedDiff(into, sp.Old, sp.New))
				return nil
			}
			if err := scaffold.WriteAtomic(into, sp.New); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s in %s\n", sp.Summary, into)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noRun, "no-run", false, "don't run the command; write the source with an auto inspector")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "how long the command may run (default 10s); also written to the source")
	cmd.Flags().StringVar(&root, "root", "", "dot-path to the list of rows, when the output has several")
	cmd.Flags().IntVar(&maxColumns, "max-columns", columns.DefaultMax, "how many columns to keep; the rest are written commented out")
	cmd.Flags().BoolVar(&noStyle, "no-style", false, "write no bold, colour rules or widths")
	cmd.Flags().StringArrayVar(&params, "param", nil, "NAME=VALUE[:TYPE]: make the argument equal to VALUE a parameter (repeatable)")
	cmd.Flags().BoolVar(&noInputs, "no-inputs", false, "write --param parameters without input fields")
	cmd.Flags().StringVar(&cols, "columns", "", "path[=Title],…: the columns, in order, instead of ranking")
	cmd.Flags().StringVar(&component, "component", "", "table, list, inspector, eventlog or logview (default inferred)")
	cmd.Flags().StringVar(&name, "name", "", "the source's and component's name (default from the command)")
	cmd.Flags().StringVar(&title, "title", "", "the component's title (default the name, title-cased)")
	cmd.Flags().DurationVar(&refresh, "refresh", 0, "poll the command on this interval (default fetch once)")
	cmd.Flags().BoolVar(&follow, "follow", false, "the command streams: sample it at once and write follow: true")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "the command finishes, however long it runs (up to --timeout)")
	cmd.Flags().StringArrayVar(&windows, "window", nil, "TOKEN=VALUE: make the argument equal to VALUE a window token (offset, limit, search, sort, sort_dir, cursor, dir, filter.NAME; repeatable)")
	cmd.Flags().StringVar(&totalPath, "total-path", "", "where a seekable window's total is (default inferred)")
	cmd.Flags().StringVar(&cursorPath, "cursor-path", "", "each item's cursor, for an anchored window (default inferred)")
	cmd.Flags().BoolVar(&growing, "growing", false, "the eventlog's set is still gaining items: follow the newest")
	cmd.Flags().DurationVar(&every, "follow-every", 0, "how often a growing window is polled (default 2s)")
	cmd.Flags().StringVar(&into, "into", "", "add the source and component to this config instead of printing a new one")
	cmd.Flags().StringVar(&place, "place", "", "with --into: stack, tab or screen (default by the config's shape)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --into: print the change as a diff and write nothing")
	return cmd
}

// cmdContext is the context a command runs under, or Background when
// Cobra was started without one.
func cmdContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
