package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
)

// writeOutput is how every command that makes a config ends: it prints
// b, or with -o writes it to path, refusing a file that exists unless
// force.
func writeOutput(cmd *cobra.Command, path string, b []byte, force bool) error {
	if path == "" {
		_, err := cmd.OutOrStdout().Write(b)
		return err
	}
	return writeFile(path, b, force)
}

// refuseExisting fails when path names a file that exists and force is
// off, so a command can refuse before doing slow work, or writing half.
func refuseExisting(path string, force bool) error {
	if path == "" || force {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return existsError(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func existsError(path string) error {
	return fmt.Errorf("%s already exists: pass --force to overwrite it, or -o to write somewhere else", path)
}

// writeFile writes b to a new file at path, or over one with force.
func writeFile(path string, b []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return existsError(path)
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

// goneFlags registers flags that were removed, hidden, so using one
// fails with what to do now instead of "unknown flag". Call it after
// cmd.Args is set: the check runs before the command's own.
func goneFlags(cmd *cobra.Command, hints map[string]string) {
	for name := range hints {
		cmd.Flags().Bool(name, false, "")
		_ = cmd.Flags().MarkHidden(name)
	}
	check := cmd.Args
	cmd.Args = func(c *cobra.Command, args []string) error {
		for name, hint := range hints {
			if c.Flags().Changed(name) {
				return usagef("--%s is gone: %s", name, hint)
			}
		}
		if check == nil {
			return nil
		}
		return check(c, args)
	}
}
