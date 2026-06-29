package screen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestStreamWebsocketIntoTable confirms streaming → table works end
// to end: each text frame from the WebSocket is JSON-parsed,
// projected into row cells via column.Value paths, prepended to the
// ring buffer, and rendered with newest-first. Non-JSON diagnostic
// frames (the source's synthetic "(connecting…)" lines) are skipped
// — they don't pollute the trade data.
func TestStreamWebsocketIntoTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		// Eat the subscribe frame the client sends, then emit some
		// trade-style JSON frames the table should pick up.
		_, _, _ = conn.Read(ctx)
		// Confirmation frame with no useful columns — should be
		// skipped as "all-empty cells".
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"event":"subscription_succeeded","data":{}}`))
		for _, frame := range []string{
			`{"data":{"id":1,"price":"100.00","amount":"0.5","type":0}}`,
			`{"data":{"id":2,"price":"101.50","amount":"0.3","type":1}}`,
			`{"data":{"id":3,"price":"102.25","amount":"1.2","type":0}}`,
		} {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"trades": cfg.NewEntry(&cfg.Source{Type: "websocket", URL: wsURL,
		InitialMessages: []string{`{"subscribe":"trades"}`}},
	)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"trades_table": {
			Type:    "table",
			Title:   "Trades",
			Source:  "trades",
			MaxRows: 50,
			Columns: []cfg.Column{
				{Title: "ID", Width: 6, Value: cfg.Path{"data.id"}},
				{Title: "Price", Width: 12, Value: cfg.Path{"data.price"}, Align: "right"},
				{Title: "Amount", Width: 12, Value: cfg.Path{"data.amount"}, Align: "right"},
				{Title: "Side", Width: 6, Value: cfg.Path{"data.type"}},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "Live trades",
			Layout: cfg.Node{Component: "trades_table"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})

	queue := []tea.Cmd{m.Init()}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(500 * time.Millisecond):
			continue
		}
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	// All three trade rows should appear (project to non-empty cells).
	for _, want := range []string{"100.00", "101.50", "102.25"} {
		if !strings.Contains(view, want) {
			t.Errorf("rendered view missing %q\n--- view ---\n%s", want, view)
			return
		}
	}
	// The subscription-confirmation frame has all-empty projected
	// cells; it should NOT appear in the table.
	if strings.Contains(view, "subscription_succeeded") {
		t.Errorf("subscription-confirmation frame should be skipped (all-empty cells)\n--- view ---\n%s", view)
	}
	// Newest-first: row 3 should be visible above row 1.
	idx1 := strings.Index(view, "100.00")
	idx3 := strings.Index(view, "102.25")
	if idx3 < 0 || idx1 < 0 || idx3 >= idx1 {
		t.Errorf("expected newest-first ordering (price 102.25 above 100.00)\n--- view ---\n%s", view)
	}
}

// TestStreamMergeKeyedUpsert is the multi-source L1 case: two
// streaming sources carry DIFFERENT fields for the same symbol;
// merge composes them into one stream; row_key + deep-merge keep both
// sources' fields alive on the same row. This is the
// bookTicker (bid/ask) + aggTrade (price/qty) pattern.
func TestStreamMergeKeyedUpsert(t *testing.T) {
	// Source A: emits {"s":SYM, "b":bid, "a":ask}
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := websocket.Accept(w, r, nil)
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for _, frame := range []string{
			`{"s":"BTC","b":"43100","a":"43105"}`,
			`{"s":"ETH","b":"2300","a":"2301"}`,
			`{"s":"BTC","b":"43200","a":"43210"}`,
		} {
			_ = conn.Write(ctx, websocket.MessageText, []byte(frame))
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer srvA.Close()
	// Source B: emits {"s":SYM, "p":price, "q":qty}
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := websocket.Accept(w, r, nil)
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for _, frame := range []string{
			`{"s":"BTC","p":"43150","q":"0.001"}`,
			`{"s":"ETH","p":"2305","q":"0.5"}`,
		} {
			_ = conn.Write(ctx, websocket.MessageText, []byte(frame))
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer srvB.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"book": cfg.NewEntry(&cfg.Source{Type: "websocket", URL: "ws://" + strings.TrimPrefix(srvA.URL, "http://")}),
		"trades": cfg.NewEntry(&cfg.Source{Type: "websocket", URL: "ws://" + strings.TrimPrefix(srvB.URL, "http://")}),
		"l1":     cfg.NewEntry(&cfg.Source{Type: "merge", Sources: []string{"book", "trades"}})}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"table": {
			Type:   "table",
			Title:  "L1",
			Source: "l1",
			RowKey: cfg.Path{"s"},
			Columns: []cfg.Column{
				{Title: "Symbol", Width: 8, Value: cfg.Path{"s"}},
				{Title: "Bid", Width: 10, Value: cfg.Path{"b"}},
				{Title: "Ask", Width: 10, Value: cfg.Path{"a"}},
				{Title: "Price", Width: 10, Value: cfg.Path{"p"}},
				{Title: "Qty", Width: 10, Value: cfg.Path{"q"}},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "L1",
			Layout: cfg.Node{Component: "table"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	queue := []tea.Cmd{m.Init()}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(500 * time.Millisecond):
			continue
		}
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	// After all 5 frames have been merged, BTC's row should have
	// bid/ask from book (the LATER one: 43200/43210) AND price/qty
	// from trades (43150/0.001). Without deep-merge, the bid/ask
	// frame would have wiped out price/qty (or vice versa).
	wanted := []string{"43200", "43210", "43150", "0.001", "2300", "2301", "2305", "0.5"}
	for _, w := range wanted {
		if !strings.Contains(view, w) {
			t.Errorf("merged L1 view missing %q (deep-merge lost a source's fields?)\n--- view ---\n%s", w, view)
			return
		}
	}
}

// TestStreamWebsocketL1Table confirms keyed-upsert mode: each event
// updates the row whose row_key matches, instead of appending. The
// L1 order-book / status-grid pattern. Simulates a stream of two
// symbols with multiple updates each; the final table should have
// exactly two rows showing the LATEST values for each.
func TestStreamWebsocketL1Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, _, _ = conn.Read(ctx)
		// Interleave updates for two symbols. The final BTC update
		// should win (43900), and the final ETH update should win
		// (2350). All earlier values get overwritten in place.
		for _, frame := range []string{
			`{"data":{"s":"BTCUSDT","b":"43100","a":"43105","B":"1.0","A":"1.0"}}`,
			`{"data":{"s":"ETHUSDT","b":"2300","a":"2301","B":"5.0","A":"5.0"}}`,
			`{"data":{"s":"BTCUSDT","b":"43500","a":"43510","B":"2.0","A":"2.0"}}`,
			`{"data":{"s":"ETHUSDT","b":"2340","a":"2342","B":"4.5","A":"4.5"}}`,
			`{"data":{"s":"BTCUSDT","b":"43900","a":"43910","B":"3.0","A":"3.0"}}`,
			`{"data":{"s":"ETHUSDT","b":"2350","a":"2352","B":"6.0","A":"6.0"}}`,
		} {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"book": cfg.NewEntry(&cfg.Source{Type: "websocket", URL: wsURL,
		InitialMessages: []string{`{"subscribe":"book"}`}},
	)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"l1": {
			Type:   "table",
			Title:  "L1",
			Source: "book",
			RowKey: cfg.Path{"data.s"},
			Columns: []cfg.Column{
				{Title: "Symbol", Width: 10, Value: cfg.Path{"data.s"}},
				{Title: "Bid", Width: 10, Value: cfg.Path{"data.b"}},
				{Title: "Ask", Width: 10, Value: cfg.Path{"data.a"}},
				{Title: "BidQty", Width: 10, Value: cfg.Path{"data.B"}},
				{Title: "AskQty", Width: 10, Value: cfg.Path{"data.A"}},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "L1",
			Layout: cfg.Node{Component: "l1"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	queue := []tea.Cmd{m.Init()}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(500 * time.Millisecond):
			continue
		}
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	// Latest values for both symbols should appear.
	if !strings.Contains(view, "43900") {
		t.Errorf("latest BTC bid 43900 missing\n--- view ---\n%s", view)
	}
	if !strings.Contains(view, "2350") {
		t.Errorf("latest ETH bid 2350 missing\n--- view ---\n%s", view)
	}
	// Stale values from earlier in the stream MUST be gone — that's
	// the whole point of upsert-by-key vs prepend.
	for _, stale := range []string{"43100", "43500", "2300", "2340"} {
		if strings.Contains(view, stale) {
			t.Errorf("stale value %q should have been overwritten by latest update\n--- view ---\n%s", stale, view)
		}
	}
}

// TestStreamWebsocketEndToEnd spins up a local WebSocket server that
// immediately emits a few text frames, builds a streaming-websocket
// source pointed at it, and verifies the logview View() includes the
// emitted lines. Catches plumbing bugs anywhere along
// Subscribe → channel → screen pump → logview.Append → render.
func TestStreamWebsocketEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Read whatever the client sends (the initial_messages
		// subscribe frame) and echo it back as the first frame, then
		// emit a few canned trade-style frames.
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, payload, err := conn.Read(ctx)
		if err == nil {
			_ = conn.Write(ctx, websocket.MessageText, []byte(`echo: `+string(payload)))
		}
		for _, frame := range []string{
			`{"event":"trade","price":100,"side":"buy"}`,
			`{"event":"trade","price":101,"side":"sell"}`,
			`{"event":"trade","price":102,"side":"buy"}`,
		} {
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
		// Hold the connection open briefly so the client can read
		// everything before we drop.
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	// httptest URL is http://; flip to ws:// for websocket.Dial.
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"feed": cfg.NewEntry(&cfg.Source{Type: "websocket", URL: wsURL,
		InitialMessages: []string{`{"subscribe":"trades"}`}},
	)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"feed_view": {
			Type:       "logview",
			Title:      "Feed",
			Source:     "feed",
			Searchable: true,
		},
	},
		Screen: cfg.Screen{
			Title:  "Feed",
			Layout: cfg.Node{Component: "feed_view"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	// Pump init + cascading cmds. Unlike the fetch-based tests, this
	// includes streaming Cmds that block until the channel has data —
	// so we use a deadline rather than a step counter to know when to
	// stop, and only drain Cmds that complete within a short timeout.
	queue := []tea.Cmd{m.Init()}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		// Run the Cmd in a goroutine with a per-Cmd timeout. Streaming
		// reads from a channel block indefinitely; we want to keep
		// pulling messages but give each one a bounded wait.
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(500 * time.Millisecond):
			continue
		}
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	wanted := []string{
		`echo: {"subscribe":"trades"}`,
		`"price":100`,
		`"price":101`,
		`"price":102`,
	}
	for _, want := range wanted {
		if !strings.Contains(view, want) {
			t.Errorf("rendered view missing %q\n--- view ---\n%s", want, view)
			return
		}
	}
}
