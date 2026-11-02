package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

func newInitCmd() *cobra.Command {
	var (
		output string
		title  string
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "init [kind]",
		Short: "Print a starter config, or an example of any kind.",
		Long: `init prints a small commented config that runs straight away, offline:
a static source, a filterable table bound to it, and one screen, with
an http and an exec source commented out as next steps. -o writes it
to a file instead.

init <kind> prints a small config built around one component kind or
source type, which runs as written, with every field that kind takes
below it, commented out: each line shows the field's default and what
it does. init file reads a JSON file, which -o writes beside the
config; init http and init websocket read the things fixture API.

Components: ` + strings.Join(scaffold.ComponentKinds, ", ") + `.
Sources: ` + strings.Join(scaffold.SourceKinds, ", ") + `.`,
		Example: `  tui-builder init table | tui-builder -   # run one without saving it
  tui-builder init table -o table.yaml     # save it`,
		Args:      usageArgs(cobra.MaximumNArgs(1)),
		ValidArgs: scaffold.Kinds(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && !slices.Contains(scaffold.Kinds(), args[0]) {
				hint := ""
				if s := config.Suggest(args[0], scaffold.Kinds()); s != "" {
					hint = fmt.Sprintf(" (did you mean %s?)", s)
				}
				return usagef("no kind %q%s; the components are %s, and the sources %s", args[0], hint,
					strings.Join(scaffold.ComponentKinds, ", "), strings.Join(scaffold.SourceKinds, ", "))
			}
			if title == "" {
				t, err := dirTitle(output)
				if err != nil {
					return err
				}
				title = t
			}
			ex := scaffold.Example{Config: scaffold.Starter(title)}
			if len(args) == 1 {
				var err error
				if ex, err = scaffold.Kind(args[0], title, filepath.Dir(output)); err != nil {
					return err
				}
			}
			stderr := cmd.ErrOrStderr()
			var side []string
			for name := range ex.Files {
				side = append(side, filepath.Join(filepath.Dir(output), name))
			}
			slices.Sort(side)
			if output == "" {
				for _, p := range side {
					fmt.Fprintf(stderr, "note: this config reads %s: pass -o to write it beside the config\n", p)
				}
				return writeOutput(cmd, "", ex.Config, force)
			}
			// Refuse before writing anything, so a refusal leaves no half.
			for _, p := range append([]string{output}, side...) {
				if err := refuseExisting(p, force); err != nil {
					return err
				}
			}
			if err := writeFile(output, ex.Config, force); err != nil {
				return err
			}
			for _, p := range side {
				if err := writeFile(p, ex.Files[filepath.Base(p)], force); err != nil {
					return err
				}
				fmt.Fprintln(stderr, "wrote", p)
			}
			fmt.Fprintln(stderr, "run: tui-builder", output)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the config to this file instead of stdout")
	cmd.Flags().StringVar(&title, "title", "", "app.title (default the directory's name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing -o file")
	goneFlags(cmd, map[string]string{"dry-run": "init prints the config unless -o names a file; drop --dry-run"})
	return cmd
}

// dirTitle is the name of the directory the config is written into.
func dirTitle(output string) (string, error) {
	dir, err := filepath.Abs(filepath.Dir(output))
	if err != nil {
		return "", err
	}
	name := filepath.Base(dir)
	if name == string(filepath.Separator) || name == "." {
		return "tui-builder", nil
	}
	return name, nil
}
