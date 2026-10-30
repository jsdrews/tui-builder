package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

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
		Use:   "init",
		Short: "Write a starter config that runs offline.",
		Long: `init writes a small commented config that runs straight away, offline:
a static source, a filterable table bound to it, and one screen, with
an http and an exec source commented out as next steps.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if title == "" {
				t, err := dirTitle(output)
				if err != nil {
					return err
				}
				title = t
			}
			b := scaffold.Starter(title)
			if dryRun {
				_, err := cmd.OutOrStdout().Write(b)
				return err
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(output, flags, 0o644)
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%s already exists: pass --force to overwrite it, or -o to write somewhere else", output)
			}
			if err != nil {
				return err
			}
			if _, err := f.Write(b); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
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
