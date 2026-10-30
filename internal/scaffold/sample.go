package scaffold

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jsdrews/tui-builder/internal/config"
)

// DefaultExecTimeout is the exec source's own default, used for the
// sample run when no --timeout is given.
const DefaultExecTimeout = 10 * time.Second

// Stream sampling: a command still running this long after it started,
// having printed something, is a stream; a stream is sampled for this
// long or this many lines, whichever comes first. Variables so tests can
// shorten them.
var (
	StreamWindow = 3 * time.Second
	StreamLines  = 20
)

// Follow says whether the command is a stream.
type Follow int

const (
	// FollowAuto decides from how the command behaves.
	FollowAuto Follow = iota
	// FollowYes is --follow: it streams, so sample it without waiting
	// to find out.
	FollowYes
	// FollowNo is --no-follow: it finishes, however long it takes (up to
	// the timeout).
	FollowNo
)

// Output is what a sample run of a command printed.
type Output struct {
	Stdout []byte
	// Truncated is set when stdout ran past config.DefaultMaxBody and
	// was cut there.
	Truncated bool
	// Stream is set when the command was still running when sampling
	// stopped: Stdout holds the complete lines it had printed.
	Stream bool
	// Detected is set on a stream found by watching it (not --follow).
	Detected bool
}

// Run runs argv as the exec source will: no shell, the inherited
// environment and working directory. A command that finishes must exit
// zero and print something within the timeout. One that keeps running
// is a stream (see Follow): it's stopped after StreamWindow or
// StreamLines, and what it printed is the sample.
func Run(ctx context.Context, argv []string, timeout time.Duration, follow Follow) (Output, error) {
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &capped{max: config.DefaultMaxBody}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	ownGroup(cmd)
	// A child that keeps stdout open mustn't keep Wait waiting once the
	// command is stopped.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return Output{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	start := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			out, err := finished(argv, err, stdout, &stderr, ctx)
			if err == nil && follow == FollowYes {
				// Asked to follow, it ended early: what it printed is
				// still a stream's sample.
				out = streamSample(out.Stdout, follow)
			}
			return out, err
		case <-ctx.Done():
			<-exited
			return Output{}, fmt.Errorf("%s: cancelled", argv[0])
		case <-deadline.C:
			if follow == FollowYes || (follow == FollowAuto && stdout.lines() > 0) {
				return stopStream(cancel, exited, stdout, follow, argv)
			}
			cancel()
			<-exited
			return Output{}, fmt.Errorf("%s didn't finish within %s; raise it with --timeout, pass --follow if it streams, or pass --no-run", argv[0], timeout)
		case <-tick.C:
			elapsed := time.Since(start)
			switch follow {
			case FollowYes:
				if stdout.lines() >= StreamLines || elapsed >= StreamWindow {
					return stopStream(cancel, exited, stdout, follow, argv)
				}
			case FollowAuto:
				// Still running past the window with output is a stream.
				// With no output yet it may be a slow command that prints
				// at the end, so it waits for one or the other.
				if elapsed >= StreamWindow && stdout.lines() > 0 {
					return stopStream(cancel, exited, stdout, follow, argv)
				}
			}
		}
	}
}

func finished(argv []string, err error, stdout *capped, stderr *bytes.Buffer, ctx context.Context) (Output, error) {
	if ctx.Err() != nil {
		return Output{}, fmt.Errorf("%s: cancelled", argv[0])
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			msg := fmt.Sprintf("%s exited with status %d", argv[0], exit.ExitCode())
			if tail := lastLines(stderr.String(), 5); tail != "" {
				msg += ":\n" + tail
			}
			return Output{}, errors.New(msg)
		}
		return Output{}, err
	}
	b := stdout.bytes()
	if len(bytes.TrimSpace(b)) == 0 {
		return Output{}, errors.New("the command printed nothing to sample")
	}
	return Output{Stdout: b, Truncated: stdout.wasCut()}, nil
}

// stopStream stops a streaming command and keeps the complete lines it
// printed, at most StreamLines of them.
func stopStream(cancel context.CancelFunc, exited <-chan error, stdout *capped, follow Follow, argv []string) (Output, error) {
	cancel()
	<-exited
	b := stdout.bytes()
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[:i+1] // drop a line cut off mid-way
	} else {
		b = nil
	}
	out := streamSample(b, follow)
	if strings.TrimSpace(string(out.Stdout)) == "" {
		return Output{}, fmt.Errorf("%s printed no complete line in %s to sample; pass --no-run --follow for a logview of it", argv[0], StreamWindow)
	}
	return out, nil
}

// streamSample is a stream's first StreamLines lines.
func streamSample(b []byte, follow Follow) Output {
	lines := strings.SplitAfter(string(b), "\n")
	if len(lines) > StreamLines {
		lines = lines[:StreamLines]
	}
	return Output{Stdout: []byte(strings.Join(lines, "")), Stream: true, Detected: follow == FollowAuto}
}

// capped keeps the first max bytes written and discards the rest, so a
// command printing more still runs to the end instead of blocking. It's
// read while the command writes.
type capped struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
	cut bool
	nl  int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := p
	if room := c.max - c.buf.Len(); room < len(p) {
		c.cut = true
		keep = p[:max(room, 0)]
	}
	c.nl += bytes.Count(keep, []byte("\n"))
	c.buf.Write(keep)
	return len(p), nil
}

func (c *capped) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Clone(c.buf.Bytes())
}

func (c *capped) lines() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nl
}

func (c *capped) wasCut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cut
}

// lastLines is the last n non-blank lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
