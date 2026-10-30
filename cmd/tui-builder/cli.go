package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jsdrews/tui-builder/internal/version"
)

// Exit codes. A usage error is the command line's fault (an unknown
// flag, a missing argument); anything else that fails is exitError.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// usageError marks an error as the command line's fault, so execute
// exits 2 and points at --help.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usagef(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// usageArgs makes a positional-argument check's failure a usage error.
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

// execute runs the command line and returns the exit code. Cobra prints
// nothing on failure; the error goes to stderr here, once.
func execute(args []string, stdout, stderr io.Writer) int {
	commandLine = args
	root := newRoot()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(stderr, err)
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(stderr, "Run 'tui-builder --help' for usage.")
		return exitUsage
	}
	return exitError
}

// runForm is what `tui-builder <config.yaml>` does; tests swap it so
// they never start the TUI.
var runForm = runConfig

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "tui-builder <config.yaml>",
		Short: "Render a YAML config as an interactive terminal UI.",
		Long: `tui-builder renders a YAML config as an interactive terminal UI.

  tui-builder <config.yaml>

A subcommand's name wins over a config file of the same name: open a
file called "init" as ./init.`,
		Version:       version.String(),
		Args:          usageArgs(cobra.MaximumNArgs(1)),
		SilenceErrors: true,
		SilenceUsage:  true,
		// Shell completion isn't offered; without this Cobra adds a
		// `completion` command that would shadow a file of that name.
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usagef("missing config file: tui-builder <config.yaml>")
			}
			err := runForm(args[0])
			if errors.Is(err, fs.ErrNotExist) {
				if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
					return fmt.Errorf("%w\nDid you mean the %q command?", err, s[0])
				}
			}
			return err
		},
	}
	// Cobra only defaults this on its own unknown-command path, which a
	// root that takes a file never reaches.
	root.SuggestionsMinimumDistance = 2
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	root.AddCommand(newInitCmd(), newGenCmd(), newVersionCmd(), newAddCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version.",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "tui-builder version", version.String())
			return nil
		},
	}
}

// newAddCmd is the parent of the commands that write a source for you
// (`add exec`). It's hidden while it has none.
func newAddCmd() *cobra.Command {
	add := &cobra.Command{
		Use:   "add <kind>",
		Short: "Add a source, and a component that shows it, to a config.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var kinds []string
			for _, c := range cmd.Commands() {
				kinds = append(kinds, c.Name())
			}
			known := "none yet"
			if len(kinds) > 0 {
				known = strings.Join(kinds, ", ")
			}
			if len(args) == 0 {
				return usagef("tui-builder add needs a kind of source (%s)", known)
			}
			return usagef("unknown kind %q for tui-builder add (%s)", args[0], known)
		},
	}
	add.AddCommand(newAddExecCmd())
	add.Hidden = !add.HasSubCommands()
	return add
}
