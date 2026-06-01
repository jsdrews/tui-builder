package datasource

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// websocketSource opens a long-lived WebSocket connection and emits
// each text frame as an Event to a bound logview. Binary frames are
// rendered as a hex marker line and otherwise ignored — most real
// streams (Slack RTM, custom event buses, chat backends) use text.
//
// One Source instance => one connection per Subscribe. Multiple
// components sharing this source share the same connection (screen.go
// fans events out to each bound consumer).
type websocketSource struct {
	url             string
	headers         map[string]string
	initialMessages []string
	timeout         time.Duration // connect timeout
}

func newWebsocket(d *cfg.DataSource) (Source, error) {
	timeout := 10 * time.Second
	if d.Timeout != "" {
		t, err := time.ParseDuration(d.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
		timeout = t
	}
	return &websocketSource{
		url:             d.URL,
		headers:         d.Headers,
		initialMessages: append([]string(nil), d.InitialMessages...),
		timeout:         timeout,
	}, nil
}

// Refresh is always 0 — the connection IS the refresh mechanism.
func (s *websocketSource) Refresh() time.Duration { return 0 }

// Fetch returns an empty snapshot so a bound logview renders empty
// before Subscribe takes over. The real data flows via Subscribe.
func (s *websocketSource) Fetch(_ context.Context) (any, error) {
	return "", nil
}

// Subscribe returns the events channel immediately. The dial + initial
// handshake happen inside the goroutine, NOT in the calling thread —
// otherwise OnEnter would block waiting for TLS, freezing the TUI for
// 1-3 seconds (and potentially much longer on slow networks) before
// the screen first renders. Connection state flows through the
// channel:
//
//   - "(connecting to wss://…)"           — emitted before the dial
//   - "(connected; sent N initial msgs)"  — emitted after the handshake
//   - any text frame from the server      — as-is
//   - Event{Err: …}                       — terminal; closes the stream
//
// The synthetic events give the user a clear signal that the
// connection is being made and that the subscribe handshake completed,
// so a slow / silent server is distinguishable from a hang.
func (s *websocketSource) Subscribe(ctx context.Context) (<-chan Event, error) {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)

		// Up-front "we're trying" event so the logview is never silent
		// during the dial. Use a non-blocking-style select so we don't
		// hang if the consumer goes away before reading.
		select {
		case ch <- Event{Line: fmt.Sprintf("(connecting to %s)", s.url)}:
		case <-ctx.Done():
			return
		}

		connectCtx, cancelConnect := context.WithTimeout(ctx, s.timeout)
		opts := &websocket.DialOptions{}
		if len(s.headers) > 0 {
			opts.HTTPHeader = make(http.Header, len(s.headers))
			for k, v := range s.headers {
				opts.HTTPHeader.Set(k, v)
			}
		}
		conn, _, err := websocket.Dial(connectCtx, s.url, opts)
		cancelConnect()
		if err != nil {
			select {
			case ch <- Event{Err: fmt.Errorf("dial %s: %w", s.url, err)}:
			case <-ctx.Done():
			}
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")

		// initial_messages — send each in order. Failure to write
		// surfaces as a terminal error.
		for i, msg := range s.initialMessages {
			if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
				select {
				case ch <- Event{Err: fmt.Errorf("initial_messages[%d] write: %w", i, err)}:
				case <-ctx.Done():
				}
				return
			}
		}

		// Confirm event so the user knows the dial + subscribe wrote
		// successfully. If only this line appears with no subsequent
		// data, the server is silent (connectivity / subscription
		// content issue) — distinguishable from a hung TUI.
		confirm := fmt.Sprintf("(connected to %s", s.url)
		if n := len(s.initialMessages); n > 0 {
			confirm += fmt.Sprintf("; sent %d initial message(s)", n)
		}
		confirm += ")"
		select {
		case ch <- Event{Line: confirm}:
		case <-ctx.Done():
			return
		}

		for {
			kind, payload, err := conn.Read(ctx)
			if err != nil {
				// ctx.Done() arrives here as the err. Don't surface
				// cancellation as a stream error — that's the normal
				// shutdown path.
				if ctx.Err() != nil {
					return
				}
				select {
				case ch <- Event{Err: err}:
				case <-ctx.Done():
				}
				return
			}
			var line string
			switch kind {
			case websocket.MessageText:
				line = string(payload)
			case websocket.MessageBinary:
				line = fmt.Sprintf("<binary frame: %d bytes>", len(payload))
			default:
				continue
			}
			select {
			case ch <- Event{Line: line}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
