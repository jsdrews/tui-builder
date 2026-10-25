package config

// app.env values read from a file or a command (`from:`). Resolved by
// ResolveEnv, which the entry points call — never Load, so `--check`,
// `--list` and tests don't run commands. tui-builder calls it before
// its startup prompts (a prompt with the same key then starts with the
// resolved value); wrangl calls it only when it fetches.
//
// A value is a secret as often as not, so nothing here prints one:
// errors and warnings name the var and where it was read from.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// envFromTimeout bounds a `from: {command:}` with no timeout of its own.
const envFromTimeout = 60 * time.Second

// envStdin is what a `from:` command reads, so a password prompt works.
// Overridable in tests.
var envStdin io.Reader = os.Stdin

// ResolveEnv sets every `app.env` var that has a `from:` and is still
// unset, in list order, so a later entry's file path can use an earlier
// one through ${env.X}. A value already in the environment wins and its
// `from:` doesn't run.
//
// A failure on a `required: true` var is returned; on any other var it
// prints a one-line warning and leaves the var unset.
//
// Runs once: a second call returns immediately.
func (c *Config) ResolveEnv() error {
	if c == nil || c.envResolved {
		return nil
	}
	c.envResolved = true
	for _, spec := range c.App.Env {
		if spec.From == nil || os.Getenv(spec.Name) != "" {
			continue
		}
		v, err := spec.From.read(c.dir)
		if err != nil {
			err = fmt.Errorf("app.env %s: from %s: %w", spec.Name, spec.From.where(), err)
			if spec.Required {
				return err
			}
			fmt.Fprintf(stderr, "warning: %v; %s stays unset\n", err, spec.Name)
			continue
		}
		os.Setenv(spec.Name, v)
	}
	return nil
}

// where names the source of the value for messages: never the value.
func (f *EnvFrom) where() string {
	if f.File != "" {
		return "file " + f.File
	}
	return fmt.Sprintf("command %v", f.Command)
}

// read gets the trimmed value. Empty counts as a failure: an empty
// token is never what a config that asked for one meant.
func (f *EnvFrom) read(dir string) (string, error) {
	var raw []byte
	var err error
	if f.File != "" {
		raw, err = os.ReadFile(f.path(dir))
	} else {
		raw, err = f.run()
	}
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return "", errors.New("empty value")
	}
	return v, nil
}

// path expands ${env.X} and a leading ~, then anchors a relative path
// at the config file's directory.
func (f *EnvFrom) path(dir string) string {
	p := envTokenRe.ReplaceAllStringFunc(f.File, func(tok string) string {
		return os.Getenv(envTokenRe.FindStringSubmatch(tok)[1])
	})
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	return p
}

// run executes the command with the terminal's stdin and stderr. The
// first line of stderr is kept for the error.
func (f *EnvFrom) run() ([]byte, error) {
	timeout := envFromTimeout
	if f.Timeout != "" {
		if d, err := time.ParseDuration(f.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.Command[0], f.Command[1:]...)
	var out, errOut bytes.Buffer
	cmd.Stdin = envStdin
	cmd.Stdout = &out
	cmd.Stderr = io.MultiWriter(stderr, &errOut)
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timed out after %s", timeout)
	}
	if err != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(errOut.String()), "\n"); line != "" {
			return nil, fmt.Errorf("%w: %s", err, line)
		}
		return nil, err
	}
	return out.Bytes(), nil
}
