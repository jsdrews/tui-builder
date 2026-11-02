package main

import (
	"fmt"
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
		initOv  bool
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
		Args: func(cmd *cobra.Command, args []string) error {
			// --init-overrides took a file; now that file is a second
			// argument.
			if initOv && len(args) == 2 {
				return usagef("--init-overrides takes no file now: it prints the overrides file, and -o FILE writes it")
			}
			return usageArgs(cobra.ExactArgs(1))(cmd, args)
		},
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
			if err := refuseExisting(output, force); err != nil {
				return err
			}
			opts.InitOverrides = initOv
			// The config being replaced lists the spec's operations
			// then, so this run can say which are new.
			if output != "" && !initOv {
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
			if initOv {
				if err := writeOutput(cmd, output, res.Starter, force); err != nil {
					return err
				}
				file := output
				if file == "" {
					file = "<overrides>"
				}
				fmt.Fprintf(stderr, "uncomment what you need in the overrides file, then\n  tui-builder gen %s --overrides %s -o <config>\n", args[0], file)
				return nil
			}
			if err := writeOutput(cmd, output, res.Config, force); err != nil {
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
	f.BoolVar(&force, "force", false, "overwrite an existing -o file")
	f.BoolVar(&initOv, "init-overrides", false, "print a starter overrides file listing every generated operation, instead of the config")
	f.StringArrayVar(&headers, "header", nil, "'Name: value' sent when fetching a spec from a URL (repeatable)")
	f.BoolVar(&quiet, "quiet", false, "print no summary or skipped operations")
	opts.Register(f)
	return cmd
}
