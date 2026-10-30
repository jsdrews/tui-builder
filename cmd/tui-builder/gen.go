package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jsdrews/tui-builder/internal/gen"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

// commandLine is the arguments execute was given, for the header gen
// writes.
var commandLine []string

func newGenCmd() *cobra.Command {
	var (
		output  string
		initOv  string
		force   bool
		headers []string
		quiet   bool
		opts    gen.Options
	)
	cmd := &cobra.Command{
		Use:   "gen <spec>",
		Short: "Generate a config from an OpenAPI 3 document.",
		Long: `gen reads an OpenAPI 3.0 or 3.1 document (a file, an http(s) URL, or -
for stdin; JSON or YAML) and writes a tui-builder config for the API: a
preset with its base URL and auth, and a screen for each operation it can
show.`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if l := opts.Layout; l != "" && l != "tabs" && l != "menu" {
				return usagef("--layout %s: want tabs or menu", l)
			}
			in := opts.Input
			in.Spec, in.Stdin, in.Headers = args[0], cmd.InOrStdin(), http.Header{}
			for _, h := range headers {
				k, v, ok := strings.Cut(h, ":")
				if !ok || strings.TrimSpace(k) == "" {
					return usagef("--header %q: want 'Name: value'", h)
				}
				in.Headers.Add(strings.TrimSpace(k), strings.TrimSpace(v))
			}
			if initOv != "" && output != "" {
				return usagef("--init-overrides writes a starter overrides file, not a config: drop -o")
			}
			for _, path := range []string{output, initOv} {
				if path == "" || force {
					continue
				}
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("%s already exists: pass --force to overwrite it, or name another file", path)
				} else if !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			opts.InitOverrides = initOv != ""
			// The config being replaced lists the spec's operations
			// then, so this run can say which are new.
			if output != "" {
				if b, err := os.ReadFile(output); err == nil {
					opts.Previous = b
				}
			}
			opts.Input = in
			opts.Command = "tui-builder " + scaffold.ShellJoin(commandLine)
			res, err := gen.Generate(cmdContext(cmd), opts)
			stderr := cmd.ErrOrStderr()
			if !quiet {
				for _, line := range res.Skipped {
					fmt.Fprintln(stderr, line)
				}
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(stderr, "warning:", w)
			}
			if err != nil {
				return err
			}
			if initOv != "" {
				if err := os.WriteFile(initOv, res.Starter, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(stderr, "wrote %s: uncomment what you need, then\n  tui-builder gen %s --overrides %s -o <config>\n", initOv, args[0], initOv)
				return nil
			}
			if output == "" {
				_, err = cmd.OutOrStdout().Write(res.Config)
			} else {
				err = os.WriteFile(output, res.Config, 0o644)
			}
			if err != nil {
				return err
			}
			if !quiet {
				for _, o := range res.New {
					fmt.Fprintln(stderr, "new in the spec:", o)
				}
				for _, o := range res.Gone {
					fmt.Fprintln(stderr, "gone from the spec:", o)
				}
				fmt.Fprintln(stderr, "generated", res.Summary)
				for _, n := range res.Notes {
					fmt.Fprintln(stderr, "note:", n)
				}
				for _, h := range res.Hints {
					fmt.Fprintln(stderr, "hint:", h)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&output, "output", "o", "", "write the config to this file instead of stdout")
	f.BoolVar(&force, "force", false, "overwrite an existing -o or --init-overrides file")
	f.StringVar(&initOv, "init-overrides", "", "write a starter overrides file listing every generated operation, instead of the config")
	f.StringArrayVar(&headers, "header", nil, "'Name: value' sent when fetching a spec from a URL (repeatable)")
	f.BoolVar(&quiet, "quiet", false, "print no summary or skipped operations")
	opts.Register(f)
	return cmd
}
