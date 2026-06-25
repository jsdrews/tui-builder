package output

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	ds "github.com/jsdrews/tui-builder/internal/datasource"
)

// Options controls how a pipeline / source is dumped to a writer.
// Zero-value defaults: pretty JSON for one-shot, NDJSON for streams,
// no limit, no time cap.
type Options struct {
	// Pretty enables indented JSON output for one-shot mode. Streams
	// always emit one frame per line regardless (NDJSON is the contract
	// for line-oriented stream consumers like `jq -c`).
	Pretty bool
	// Raw bypasses JSON encoding for text values: string Fetch results
	// are written as-is (with a trailing newline), and streaming
	// Event.Line is written verbatim (no quote / escape). Non-string
	// Fetch results (parsed JSON) still go through json.Encode — there's
	// no raw text representation for those. Use --raw to make
	// `format: text` sources behave like `kubectl logs` or `tail -f`
	// for human reading.
	Raw bool
	// Limit caps the number of items emitted from a stream. 0 = no cap
	// (run until ctx is cancelled or the stream ends).
	Limit int
	// MaxDuration caps how long to consume from a stream. 0 = no cap.
	MaxDuration time.Duration
}

// Run consumes from src and writes results to w. Decides between
// one-shot and streaming based on whether src implements
// StreamingSource AND its Subscribe doesn't return ErrNotStreaming. For
// one-shot, calls Fetch once and emits the result. For streaming, emits
// one frame per line until limits / cancellation.
//
// Returns an error if Fetch/Subscribe failed. Honored ctx cancellation
// is NOT returned as an error — that's the clean shutdown path.
func Run(ctx context.Context, src ds.Source, w io.Writer, opts Options) error {
	if streamer, ok := src.(ds.StreamingSource); ok {
		ch, err := streamer.Subscribe(ctx)
		if err == nil {
			return runStream(ctx, ch, w, opts)
		}
		if !errors.Is(err, ds.ErrNotStreaming) {
			return fmt.Errorf("subscribe: %w", err)
		}
		// Fall through to one-shot Fetch.
	}
	data, err := src.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	// Raw mode skips JSON encoding when the value is a string —
	// `format: text` sources hand back raw bodies, and the user almost
	// certainly wants those as plain text rather than JSON-quoted.
	// Non-string values fall through to JSON encoding because there's
	// no other reasonable text form for them.
	if opts.Raw {
		if s, ok := data.(string); ok {
			if _, err := io.WriteString(w, s); err != nil {
				return err
			}
			if !strings.HasSuffix(s, "\n") {
				_, err = io.WriteString(w, "\n")
			}
			return err
		}
	}
	enc := json.NewEncoder(w)
	if opts.Pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(data)
}

// runStream emits one frame per line until the stream closes, ctx is
// cancelled, or limits (Limit/MaxDuration) are hit. Each frame is one
// JSON value followed by a newline. Errors from the stream are written
// as `{"error": "..."}` lines so consumers can grep them out without
// interrupting the line-protocol contract.
func runStream(ctx context.Context, ch <-chan ds.Event, w io.Writer, opts Options) error {
	deadline := time.Time{}
	if opts.MaxDuration > 0 {
		deadline = time.Now().Add(opts.MaxDuration)
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	emitted := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if ev.Err != nil {
				// Emit the error as a structured line and keep going —
				// the stream may still recover, and a one-off error
				// shouldn't terminate the pipe for downstream consumers.
				// In raw mode we still emit errors structurally so the
				// user notices them; raw is about data lines, not
				// diagnostics.
				obj := map[string]string{"error": ev.Err.Error()}
				if err := writeJSONLine(w, obj); err != nil {
					return err
				}
				continue
			}
			// Raw mode writes event.Line verbatim — no JSON quoting, no
			// escape. The right shape for `kubectl logs`-style streams
			// where each line is already plain text.
			if opts.Raw {
				if _, err := io.WriteString(w, ev.Line); err != nil {
					return err
				}
				if _, err := io.WriteString(w, "\n"); err != nil {
					return err
				}
				emitted++
				if opts.Limit > 0 && emitted >= opts.Limit {
					return nil
				}
				continue
			}
			// Default: try to parse the line as JSON so we re-emit
			// canonical JSON (and downstream `jq` can consume it). If it
			// isn't JSON, emit as a JSON string instead — keeps the
			// line-protocol promise even for non-JSON streams (e.g. kube
			// logs via format:text).
			if err := writeEventLine(w, ev.Line); err != nil {
				return err
			}
			emitted++
			if opts.Limit > 0 && emitted >= opts.Limit {
				return nil
			}
		}
	}
}

// writeEventLine emits one event payload. If the line parses as JSON,
// it's re-emitted canonicalized; otherwise it's emitted as a JSON
// string. Either way: one line, valid JSON.
func writeEventLine(w io.Writer, line string) error {
	var parsed any
	if err := json.Unmarshal([]byte(line), &parsed); err == nil {
		return writeJSONLine(w, parsed)
	}
	return writeJSONLine(w, line)
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
