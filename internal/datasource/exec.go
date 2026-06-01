package datasource

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// execSource runs a subprocess and consumes its stdout. Two modes:
//
//   - Format `json` (default) / `text`: one-shot mode. `cmd.Run()`
//     waits for completion and returns the parsed stdout. Use for CLIs
//     that print a snapshot (`kubectl get -o json`, `gh api`,
//     `terraform output -json`, custom scripts).
//
//   - `follow: true`: streaming mode. `cmd.Start()` then read stdout
//     line-by-line and emit each line as an Event to a logview binding.
//     Use for `kubectl logs -f`, `tail -f`, `journalctl -f`, anything
//     that keeps emitting.
//
// Streaming mode satisfies both Source (a synchronous Fetch returns an
// empty snapshot — the streaming Subscribe is the real path) and
// StreamingSource. The screen prefers Subscribe when the source
// implements it.
type execSource struct {
	argv    []string
	env     []string // overrides applied on top of os.Environ()
	format  string   // "json" (default) or "text"
	root    string   // dot-path applied to parsed JSON before return
	follow  bool
	timeout time.Duration
	refresh time.Duration
}

func newExec(d *cfg.DataSource) (Source, error) {
	timeout := 10 * time.Second
	if d.Timeout != "" {
		t, err := time.ParseDuration(d.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
		timeout = t
	}
	var refresh time.Duration
	if d.Refresh != "" {
		r, err := time.ParseDuration(d.Refresh)
		if err != nil {
			return nil, fmt.Errorf("refresh: %w", err)
		}
		refresh = r
	}
	env := make([]string, 0, len(d.Env))
	for k, v := range d.Env {
		env = append(env, k+"="+v)
	}
	return &execSource{
		argv:    append([]string(nil), d.Command...),
		env:     env,
		format:  d.Format,
		root:    d.Root,
		follow:  d.Follow,
		timeout: timeout,
		refresh: refresh,
	}, nil
}

func (s *execSource) Refresh() time.Duration {
	if s.follow {
		// Streaming mode owns its own clock — no tea.Tick polling.
		return 0
	}
	return s.refresh
}

// Fetch handles one-shot mode. In follow mode it returns an empty
// snapshot synchronously so the bound logview shows a clean empty
// state before Subscribe takes over.
func (s *execSource) Fetch(ctx context.Context) (any, error) {
	if s.follow {
		// Streaming sources hand back a sentinel empty string so the
		// logview's Clear+AppendLines path renders an empty buffer
		// without errors. The actual data arrives via Subscribe.
		return "", nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.argv[0], s.argv[1:]...)
	if len(s.env) > 0 {
		cmd.Env = append(os.Environ(), s.env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		serr := strings.TrimSpace(stderr.String())
		if serr != "" {
			return nil, fmt.Errorf("%s: %s", err, serr)
		}
		return nil, err
	}
	if s.format == "text" {
		return stdout.String(), nil
	}
	var out any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		preview := stdout.String()
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		return nil, fmt.Errorf("parse json: %w (preview: %q)", err, preview)
	}
	return applyRoot(out, s.root), nil
}

// Subscribe implements StreamingSource for follow mode. Starts the
// subprocess, returns a channel of stdout lines, and reaps the process
// when ctx is cancelled. Non-follow sources return an error so the
// screen can fall back to the polling path.
func (s *execSource) Subscribe(ctx context.Context) (<-chan Event, error) {
	if !s.follow {
		return nil, ErrNotStreaming
	}
	// No timeout on the context — follow streams are inherently
	// long-running. The caller cancels ctx when the screen pops.
	cmd := exec.CommandContext(ctx, s.argv[0], s.argv[1:]...)
	if len(s.env) > 0 {
		cmd.Env = append(os.Environ(), s.env...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ch := make(chan Event, 64)
	var (
		stderrBuf strings.Builder
		stderrMu  sync.Mutex
		wg        sync.WaitGroup
	)

	// Reader goroutines — stdout lines are events; stderr is buffered
	// and only surfaced if the subprocess exits with an error (the
	// kubectl / aws / gh pattern: complaints land on stderr).
	scan := func(r io.Reader, emit bool) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		// Allow long lines without growing the buffer infinitely.
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if emit {
				select {
				case ch <- Event{Line: sc.Text()}:
				case <-ctx.Done():
					return
				}
			} else {
				stderrMu.Lock()
				stderrBuf.WriteString(sc.Text())
				stderrBuf.WriteByte('\n')
				stderrMu.Unlock()
			}
		}
	}
	wg.Add(2)
	go scan(stdout, true)
	go scan(stderr, false)

	// Reaper: when stdout closes (subprocess exited), wait for the
	// process state and emit a terminal event with the exit error.
	// Closes the channel last so consumers see all events first.
	go func() {
		wg.Wait()
		err := cmd.Wait()
		if err != nil {
			stderrMu.Lock()
			serr := strings.TrimSpace(stderrBuf.String())
			stderrMu.Unlock()
			msg := err.Error()
			if serr != "" {
				msg = fmt.Sprintf("%s: %s", err, serr)
			}
			select {
			case ch <- Event{Err: fmt.Errorf("%s", msg)}:
			case <-ctx.Done():
			}
		}
		close(ch)
	}()

	return ch, nil
}
