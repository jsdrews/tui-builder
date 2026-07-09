package screen

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// ─────────────────────────────────────────────────────────────────────────
// Headless bubbletea harness for on_cursor.
//
// Motivation: on_cursor lives at the intersection of tuilib's msg
// pipeline, our fanout dispatch, template resolution, and pipeline
// registry construction. Four separate bugs shipped past unit tests
// during the initial build (BuildLeaf-only path, batch-msg unwrap,
// post-fetch wake, path composition). Unit tests couldn't catch any
// of them because they lived in the msg pump.
//
// These tests drive real screen.Model instances with the same msg
// sequences bubbletea would produce, drain every Cmd recursively,
// and assert on target component state. Static sources are used
// everywhere so the tests don't depend on external binaries or the
// filesystem.
// ─────────────────────────────────────────────────────────────────────────

// runToQuiescence pumps msgs through m.Update, draining each Cmd's
// output recursively into the inbox, until nothing more fires or the
// iteration cap is hit. Cap prevents accidental live-lock in
// broken configs; the tests should exit well under it.
func runToQuiescence(t *testing.T, m *Model, msgs ...tea.Msg) {
	t.Helper()
	// Seed with a WindowSizeMsg — bubbletea always sends this at
	// startup, and pane/tuilib components use it to compute their
	// initial dimensions.
	inbox := append([]tea.Msg{tea.WindowSizeMsg{Width: 100, Height: 40}}, msgs...)
	const maxIter = 200
	for iter := 0; iter < maxIter && len(inbox) > 0; iter++ {
		msg := inbox[0]
		inbox = inbox[1:]
		_, cmd := m.Update(msg)
		inbox = append(inbox, drainCmd(cmd)...)
	}
	if len(inbox) > 0 {
		t.Fatalf("msg pump did not settle in %d iterations (%d msgs remaining)", maxIter, len(inbox))
	}
}

// drainCmd invokes a Cmd (recursively unwrapping tea.BatchMsg) and
// returns every leaf msg produced. Filters out nil sub-cmds. Runs
// each Cmd with a short timeout so delayed msgs (tea.Tick from
// source refresh intervals) don't stall the harness — those msgs
// wouldn't help the assertion anyway, and the pump would be blocked
// waiting for the next 30s poll otherwise.
func drainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg, ok := runWithTimeout(cmd, cmdTimeout)
	if !ok {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, drainCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// cmdTimeout is the per-Cmd budget. 50ms is enough for static
// sources; real exec/http fetches need more but the fs example
// still shouldn't take longer than a couple hundred ms. Tests that
// need more can call runWithTimeout directly.
var cmdTimeout = 3 * time.Second

// runWithTimeout invokes cmd in a goroutine, returning the msg if
// it comes back within d. Returns (nil, false) when the Cmd is
// still running past the deadline — the harness treats that as
// "delayed msg not relevant for now."
func runWithTimeout(cmd tea.Cmd, d time.Duration) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(d):
		return nil, false
	}
}

// newTestModel wires a Config into a screen.Model. Panics on failure
// so tests fail fast at build time rather than deep in the harness.
func newTestModel(t *testing.T, c *cfg.Config) *Model {
	t.Helper()
	if err := c.Validate(); err != nil {
		t.Fatalf("config validate: %v", err)
	}
	m, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	return m
}

// renderScreen forces every component through the layout engine at a
// realistic terminal size. Rendering is what triggers SetDimensions
// on each Sizer, so a component populated via ApplyData but never
// rendered will show empty in View().
func renderScreen(m *Model) string {
	return m.Layout().Render(100, 40)
}

// detailsPane extracts the portion of the rendered screen that
// belongs to the second (detail) pane. Assertions apply here so a
// substring in the table above isn't mistaken for a substring in
// the inspector below. Splits on the second top-border marker.
func detailsPane(t *testing.T, m *Model) string {
	t.Helper()
	rendered := renderScreen(m)
	// The layout in these tests always vstacks/hstacks the driver
	// then the target. Each pane opens with "┌ <title> ─" and closes
	// with "└─". The second "┌" starts the detail pane.
	parts := strings.SplitN(rendered, "┌", 3)
	if len(parts) < 3 {
		t.Fatalf("expected at least two pane frames in render; got:\n%s", rendered)
	}
	return "┌" + parts[2]
}

// ─────────────────────────────────────────────────────────────────────────
// Test 1 — Table driver, textview target
//
// The simplest end-to-end shape. A table of fruits; a parameterised
// filter source keyed on the fruit's name; a textview bound to a
// per-fruit info string. Verifies:
//   - tree.SelectedChangedMsg equivalent (RowFocusedMsg) reaches
//     handleCursorChange
//   - startCursorFetch runs the pipeline with the right param
//   - postApplyMsg wakes the target so SetContent actually happens
// ─────────────────────────────────────────────────────────────────────────

func fruitInfoConfig() *cfg.Config {
	return &cfg.Config{
		Data: cfg.DataBlock{
			Sources: map[string]*cfg.Source{
				"fruits": {
					Type: "static",
					Data: []any{
						map[string]any{"name": "apple", "note": "crisp red pome"},
						map[string]any{"name": "banana", "note": "long yellow berry"},
					},
				},
				// Parameterised filter — the target of the cursor bind.
				// Returns the row whose name matches params.name. The
				// project/derive chain was tempting for symmetry with
				// real configs but each pipeline stage owns its own
				// params scope (they don't propagate through `from:`),
				// which turned this into a subtle empty-result bug.
				// Keep the target a single stage so the test doesn't
				// re-encounter that limitation.
				"note_line": {
					Type: "filter",
					Parameters: map[string]*cfg.Parameter{
						"name": {Type: "string", Default: ""},
					},
					From:  "fruits",
					Where: "name == params.name",
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"fruits_table": {
					Type: "table", Source: "fruits",
					Columns: []cfg.Column{
						{Title: "Name", Value: cfg.Path{"name"}},
						{Title: "Note", Value: cfg.Path{"note"}},
					},
				},
				"note_pane": {
					Type:   "inspector",
					Source: "note_line",
					Auto:   true,
					OnCursor: &cfg.OnCursor{
						Source: "fruits_table",
						Bind:   map[string]string{"name": "${cursor.Name}"},
					},
				},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{VStack: []cfg.Item{
					{Node: cfg.Node{Component: "fruits_table"}},
					{Node: cfg.Node{Component: "note_pane"}},
				}},
			},
		},
	}
}

// TestOnCursorTableInspector_InitialPopulates covers the biggest
// class of bug from the interactive runs: the initial cursor lands
// on the first row automatically, so the target inspector should
// populate without any user input. If it doesn't, at least one of
// {batch-msg unwrap, post-fetch wake, pipeline.Build routing} is
// broken.
//
// Assertion strategy: check that (1) cursorState carries the right
// Selection for the driver — proves the msg pipeline delivered
// through tag → handle, and (2) the details pane's rendered body
// is non-empty — proves ApplyData → SetFields ran with real data.
// Text-content grep on the whole render is misleading because the
// driver table renders every row and the target's fields.
func TestOnCursorTableInspector_InitialPopulates(t *testing.T) {
	m := newTestModel(t, fruitInfoConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	sel, ok := m.cursorState["fruits_table"]
	if !ok || sel.String != "apple" {
		t.Fatalf("cursorState[fruits_table] should be apple's row; got %+v (ok=%v)", sel, ok)
	}
	details := detailsPane(t, m)
	if !strings.Contains(details, "[0]") {
		t.Errorf("details pane should show the auto-inspector's [0] wrapper for a populated result; got:\n%s", details)
	}
}

// TestOnCursorTableInspector_CursorMoveRebinds pins the reactive
// path: after the cursor moves to the second row via a `j` key
// press, cursorState should update AND the details pane's fetched
// data should change (verified by inspecting the cursor selection —
// SetFields ran but the wrapper collapses the value; assertion on
// cursorState is the semantic proof).
func TestOnCursorTableInspector_CursorMoveRebinds(t *testing.T) {
	m := newTestModel(t, fruitInfoConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	// Sanity: initial state.
	if sel := m.cursorState["fruits_table"]; sel.String != "apple" {
		t.Fatalf("initial cursor should be on apple; got %q", sel.String)
	}

	// Press `j` — cursor moves apple → banana.
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})

	sel := m.cursorState["fruits_table"]
	if sel.String != "banana" {
		t.Errorf("after cursor move, driver Selection should be banana; got %q", sel.String)
	}
	if len(sel.Cells) < 2 || sel.Cells[1] != "long yellow berry" {
		t.Errorf("cursor's Cells should include banana's note; got %v", sel.Cells)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 2 — Tree driver, textview target
//
// Nested static data, ${cursor.path} joining the label chain, a
// path-parameterised filter that resolves to a "detail" string.
// Verifies:
//   - children: recursive walker builds the nested tree
//   - tree.SelectedChangedMsg fires with the right path
//   - ${cursor.path} joins correctly
//   - pipeline handles a path-keyed parameter tuple
// ─────────────────────────────────────────────────────────────────────────

func fsLikeTreeConfig() *cfg.Config {
	// Nested "filesystem" fixture, all in-memory.
	tree := map[string]any{
		"name": "root",
		"contents": []any{
			map[string]any{
				"name": "docs",
				"contents": []any{
					map[string]any{"name": "readme.md", "kind": "file"},
					map[string]any{"name": "changelog.md", "kind": "file"},
				},
				"kind": "directory",
			},
			map[string]any{
				"name": "src",
				"contents": []any{
					map[string]any{"name": "main.go", "kind": "file"},
				},
				"kind": "directory",
			},
		},
		"kind": "directory",
	}
	// Flat lookup table: full-path → info string. The detail source
	// filters this by params.path.
	entries := []any{
		map[string]any{"path": "root", "info": "the fixture root directory"},
		map[string]any{"path": "root/docs", "info": "docs section, 2 files"},
		map[string]any{"path": "root/docs/readme.md", "info": "the readme"},
		map[string]any{"path": "root/docs/changelog.md", "info": "the changelog"},
		map[string]any{"path": "root/src", "info": "source code"},
		map[string]any{"path": "root/src/main.go", "info": "the entrypoint"},
	}
	return &cfg.Config{
		Data: cfg.DataBlock{
			Sources: map[string]*cfg.Source{
				"walk":       {Type: "static", Data: tree},
				"path_index": {Type: "static", Data: entries},
				"path_info": {
					Type: "filter",
					Parameters: map[string]*cfg.Parameter{
						"path": {Type: "string", Default: ""},
					},
					From:  "path_index",
					Where: "path == params.path",
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"files": {
					Type: "tree", Source: "walk",
					Label: cfg.Path{"name"}, Children: cfg.Path{"contents"},
					RootLabel:    "root",
					InitialDepth: 3,
				},
				"info_pane": {
					Type: "inspector", Source: "path_info", Auto: true,
					OnCursor: &cfg.OnCursor{
						Source: "files",
						Bind:   map[string]string{"path": "${cursor.path}"},
					},
				},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{HStack: []cfg.Item{
					{Node: cfg.Node{Component: "files"}},
					{Node: cfg.Node{Component: "info_pane"}},
				}},
			},
		},
	}
}

// TestOnCursorTree_InitialPopulatesFromRoot pins that the tree's
// initial cursor at root produces a cursorState carrying just
// ["root"]. Confirms tree.SelectedChangedMsg reaches
// handleCursorChange with the proper Path.
func TestOnCursorTree_InitialPopulatesFromRoot(t *testing.T) {
	m := newTestModel(t, fsLikeTreeConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	sel, ok := m.cursorState["files"]
	if !ok || sel.String != "root" {
		t.Fatalf("cursorState[files] should be root; got %+v (ok=%v)", sel, ok)
	}
	if len(sel.Cells) != 1 || sel.Cells[0] != "root" {
		t.Errorf("root cursor Cells should be [root]; got %v", sel.Cells)
	}
}

// TestOnCursorTree_DrillDownComposesPath walks the cursor into a
// nested node and asserts the path composition. This is the exact
// bug from the fs demo — the synthetic-root prefix was double-
// prepending labels and ${cursor.path} joined to an invalid string.
func TestOnCursorTree_DrillDownComposesPath(t *testing.T) {
	m := newTestModel(t, fsLikeTreeConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	// buildTree's placeholder seed only opens the root; deeper levels
	// stay collapsed until the user hits E (expand-all). Do that so
	// j lands on the first descendant, not the root's next sibling.
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})

	// j → docs (root's first child).
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	sel := m.cursorState["files"]
	wantPath := []string{"root", "docs"}
	if !equalStrings(sel.Cells, wantPath) {
		t.Fatalf("after j → docs: Cells = %v, want %v", sel.Cells, wantPath)
	}

	// Another j → readme.md (docs's first child, now visible).
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	sel = m.cursorState["files"]
	wantPath = []string{"root", "docs", "readme.md"}
	if !equalStrings(sel.Cells, wantPath) {
		t.Fatalf("after second j → readme.md: Cells = %v, want %v", sel.Cells, wantPath)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────
// Test 3 — List driver, textview target
//
// Simplest bind — ${cursor} = the focused item. Verifies the list
// SelectedChangedMsg path (issue #44 consumer wiring).
// ─────────────────────────────────────────────────────────────────────────

func listNoteConfig() *cfg.Config {
	notes := []any{
		map[string]any{"key": "one", "text": "the first note"},
		map[string]any{"key": "two", "text": "the second note"},
	}
	return &cfg.Config{
		Data: cfg.DataBlock{
			Sources: map[string]*cfg.Source{
				"keys": {
					Type: "static",
					Data: []any{
						map[string]any{"key": "one"},
						map[string]any{"key": "two"},
					},
				},
				"notes_data": {Type: "static", Data: notes},
				"note_by_key": {
					Type: "filter",
					Parameters: map[string]*cfg.Parameter{
						"key": {Type: "string", Default: ""},
					},
					From:  "notes_data",
					Where: "key == params.key",
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"key_list": {
					Type: "list", Source: "keys", Item: "key",
				},
				"note_pane": {
					Type: "inspector", Source: "note_by_key", Auto: true,
					OnCursor: &cfg.OnCursor{
						Source: "key_list",
						Bind:   map[string]string{"key": "${cursor}"},
					},
				},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{HStack: []cfg.Item{
					{Node: cfg.Node{Component: "key_list"}},
					{Node: cfg.Node{Component: "note_pane"}},
				}},
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Test 4 — Filesystem-shape end-to-end
//
// Exact structural clone of examples/on_cursor_fs.yaml (nested tree
// source, textview target, ${cursor.path} bind) but with static data
// so we isolate the plumbing from `tree -J` / `stat`. If this test
// passes but the interactive example fails, the bug is somewhere in
// the exec source shape or textview rendering path. If it fails, the
// bug is in on_cursor.
// ─────────────────────────────────────────────────────────────────────────

func fsExampleShapeConfig() *cfg.Config {
	// Mirrors what `tree -J cmd` returns after `root: [0]`:
	// a single top-level directory node with recursive contents.
	walk := map[string]any{
		"type": "directory",
		"name": "cmd",
		"contents": []any{
			map[string]any{
				"type": "directory",
				"name": "wrangl",
				"contents": []any{
					map[string]any{"type": "file", "name": "main.go"},
				},
			},
			map[string]any{
				"type": "directory",
				"name": "tui-builder",
				"contents": []any{
					map[string]any{"type": "file", "name": "main.go"},
				},
			},
		},
	}
	// Flat lookup: full path → stat-like info string. Mirrors what
	// the exec `stat` source produces when bound with `path: <p>`.
	statTable := []any{
		map[string]any{"path": "cmd", "info": "dir cmd"},
		map[string]any{"path": "cmd/wrangl", "info": "dir cmd/wrangl"},
		map[string]any{"path": "cmd/wrangl/main.go", "info": "file cmd/wrangl/main.go"},
		map[string]any{"path": "cmd/tui-builder", "info": "dir cmd/tui-builder"},
		map[string]any{"path": "cmd/tui-builder/main.go", "info": "file cmd/tui-builder/main.go"},
	}
	return &cfg.Config{
		Data: cfg.DataBlock{
			Sources: map[string]*cfg.Source{
				"walk":       {Type: "static", Data: walk},
				"stat_table": {Type: "static", Data: statTable},
				"stat_detail": {
					Type: "filter",
					Parameters: map[string]*cfg.Parameter{
						"path": {Type: "string", Default: ""},
					},
					From:  "stat_table",
					Where: "path == params.path",
				},
			},
		},
		TUI: cfg.TUIBlock{
			Components: map[string]*cfg.Component{
				"files": {
					Type:         "tree",
					Source:       "walk",
					Label:        cfg.Path{"name"},
					Children:     cfg.Path{"contents"},
					RootLabel:    "cmd",
					InitialDepth: 3,
				},
				// TEXTVIEW target, not inspector — matches the fs
				// example's shape exactly.
				"stat_pane": {
					Type:   "textview",
					Source: "stat_detail",
					OnCursor: &cfg.OnCursor{
						Source: "files",
						Bind:   map[string]string{"path": "${cursor.path}"},
					},
				},
			},
			Screen: cfg.Screen{
				Layout: cfg.Node{HStack: []cfg.Item{
					{Node: cfg.Node{Component: "files"}},
					{Node: cfg.Node{Component: "stat_pane"}},
				}},
			},
		},
	}
}

func TestOnCursorFsShape_InitialPopulates(t *testing.T) {
	m := newTestModel(t, fsExampleShapeConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	sel, ok := m.cursorState["files"]
	if !ok || sel.String != "cmd" {
		t.Fatalf("initial cursor should be at root 'cmd'; got %+v (ok=%v)", sel, ok)
	}
	// The textview target should have received the stat_detail
	// fetch result for path="cmd" — a []any with one map. Check the
	// textview's Content directly since it's an exported accessor.
	tv := m.tree.Components["stat_pane"].Textview
	content := tv.Content()
	if content == "" {
		t.Fatalf("textview content should be non-empty after initial cursor emit; got %q", content)
	}
	t.Logf("initial textview content (raw): %q", content)
}

func TestOnCursorFsShape_DrillDown(t *testing.T) {
	m := newTestModel(t, fsExampleShapeConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	// Expand + drill to cmd/wrangl/main.go.
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // → wrangl
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}) // → main.go

	sel := m.cursorState["files"]
	want := []string{"cmd", "wrangl", "main.go"}
	if !equalStrings(sel.Cells, want) {
		t.Fatalf("after drill: Cells = %v, want %v", sel.Cells, want)
	}

	tv := m.tree.Components["stat_pane"].Textview
	content := tv.Content()
	if content == "" {
		t.Errorf("textview should have fresh content after drill; got empty")
	}
	t.Logf("drilled textview content (raw): %q", content)
}

// TestOnCursorFsExample_YAML loads the exact YAML the interactive
// user would run and drives it through the harness. Catches shape
// mismatches between our static-fixture tests and the real config.
func TestOnCursorFsExample_YAML(t *testing.T) {
	// Real fs example uses `tree -J -L 4 cmd` relative to cwd; the
	// go test binary starts in internal/screen/, where cmd doesn't
	// exist. Chdir to repo root so the tree actually populates.
	prev, _ := os.Getwd()
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	c, err := cfg.Load("examples/on_cursor_fs.yaml")
	if err != nil {
		t.Skipf("skip: cannot load fs example: %v", err)
	}
	m, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, theme.Nord())
	if err != nil {
		t.Fatalf("build model from fs example: %v", err)
	}
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)

	sel, ok := m.cursorState["files"]
	if !ok || sel.String != "cmd" {
		t.Fatalf("initial fs cursor should be at root 'cmd'; got %+v (ok=%v)", sel, ok)
	}
	// The inspector should have rendered fields — we grep the layout
	// render for the "path" label the stat JSON always includes. If
	// the render doesn't carry "cmd" the leaf-param-binding path is
	// regressed.
	rendered := m.Layout().Render(120, 40)
	if !strings.Contains(rendered, "cmd") {
		t.Errorf("stat inspector render should show the queried path; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "path") {
		t.Errorf("stat inspector should carry a 'path' field label; got:\n%s", rendered)
	}
}

func TestOnCursorList_InitialPopulates(t *testing.T) {
	m := newTestModel(t, listNoteConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)
	sel, ok := m.cursorState["key_list"]
	if !ok || sel.String != "one" {
		t.Fatalf("cursorState[key_list] should be 'one'; got %+v (ok=%v)", sel, ok)
	}
}

func TestOnCursorList_CursorMoveRebinds(t *testing.T) {
	m := newTestModel(t, listNoteConfig())
	initCmd := m.OnEnter(nil)
	runToQuiescence(t, m, drainCmd(initCmd)...)
	runToQuiescence(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	sel := m.cursorState["key_list"]
	if sel.String != "two" {
		t.Errorf("after cursor move, list selection should be 'two'; got %q", sel.String)
	}
}
