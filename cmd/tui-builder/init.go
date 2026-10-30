package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jsdrews/tui-builder/internal/config"
	"github.com/jsdrews/tui-builder/internal/scaffold"
)

// defaultInitPath is where `tui-builder init` writes without -o.
const defaultInitPath = "tui-builder.yaml"

func newInitCmd() *cobra.Command {
	var (
		output string
		title  string
		dryRun bool
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "init [kind]",
		Short: "Write a starter config, or an example of any kind.",
		Long: `init writes a small commented config that runs straight away, offline:
a static source, a filterable table bound to it, and one screen, with
an http and an exec source commented out as next steps.

init <kind> writes a small config built around one component kind or
source type, which runs as written, with every field that kind takes
below it, commented out: each line shows the field's default and what
it does. init file also writes the JSON file it reads; init http and
init websocket read the things fixture API.

Components: ` + strings.Join(scaffold.ComponentKinds, ", ") + `.
Sources: ` + strings.Join(scaffold.SourceKinds, ", ") + `.`,
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
			if dryRun {
				_, err := cmd.OutOrStdout().Write(ex.Config)
				return err
			}
			// Refuse before writing anything, so a refusal leaves no half.
			var side []string
			for name := range ex.Files {
				side = append(side, filepath.Join(filepath.Dir(output), name))
			}
			slices.Sort(side)
			if !force {
				for _, p := range append([]string{output}, side...) {
					if _, err := os.Stat(p); err == nil {
						return fmt.Errorf("%s already exists: pass --force to overwrite it, or -o to write somewhere else", p)
					}
				}
			}
			if err := writeFile(output, ex.Config, force); err != nil {
				return err
			}
			for _, p := range side {
				if err := writeFile(p, ex.Files[filepath.Base(p)], force); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "wrote", p)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "run: tui-builder", output)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", defaultInitPath, "the file to write")
	cmd.Flags().StringVar(&title, "title", "", "app.title (default the directory's name)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the config instead of writing it")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

// writeFile writes b to a new file at path, or over one with force.
func writeFile(path string, b []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: pass --force to overwrite it, or -o to write somewhere else", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
