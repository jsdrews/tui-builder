package screen

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/tree"
)

// TestTagCursorFocusedUnwrapsBatchMsg pins the fix for the on-hover
// bug: tuilib's Update returns tea.Batch(cmd, m.flushMsgs()). Calling
// the batched Cmd produces a tea.BatchMsg (a []tea.Cmd), not the
// focus emit itself. Our tagger has to unwrap the batch and re-tag
// each sub-cmd, otherwise every focus emit that co-flushes with any
// other cmd (which is virtually all of them) is silently dropped.
func TestTagCursorFocusedUnwrapsBatchMsg(t *testing.T) {
	// Simulate what a tuilib component's Update returns: a Batch with
	// the focus emit alongside a viewport / spinner cmd.
	batchCmd := tea.Batch(
		func() tea.Msg { return struct{ noop int }{1} }, // some other cmd
		func() tea.Msg { return table.RowFocusedMsg{Row: 3, Cells: []string{"a", "b"}} },
	)
	wrapped := tagCursorFocused(batchCmd, "my_table")
	msg := wrapped()

	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("want BatchMsg, got %T", msg)
	}
	var found *taggedCursorMsg
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if t, ok := sub().(taggedCursorMsg); ok {
			found = &t
			break
		}
	}
	if found == nil {
		t.Fatal("no taggedCursorMsg found inside batch — the sub-cmds weren't re-wrapped")
	}
	if found.driver != "my_table" || found.sel.String != "a" {
		t.Errorf("tagged msg = %+v, want driver=my_table sel.String=a", found)
	}
}

// TestTagCursorFocusedTreeFromBatch pins the tree path — same batch
// unwrap but the emit is SelectedChangedMsg with a Path array.
func TestTagCursorFocusedTreeFromBatch(t *testing.T) {
	batchCmd := tea.Batch(
		func() tea.Msg { return struct{ noop int }{1} },
		func() tea.Msg {
			return tree.SelectedChangedMsg{
				Path:  []string{"root", "team", "alice"},
				Label: "alice",
				Depth: 2,
			}
		},
	)
	wrapped := tagCursorFocused(batchCmd, "org_tree")
	batch, ok := wrapped().(tea.BatchMsg)
	if !ok {
		t.Fatalf("want BatchMsg, got %T", wrapped())
	}
	var found *taggedCursorMsg
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if t, ok := sub().(taggedCursorMsg); ok {
			found = &t
			break
		}
	}
	if found == nil {
		t.Fatal("no taggedCursorMsg for tree emit")
	}
	if found.driver != "org_tree" {
		t.Errorf("driver = %q, want org_tree", found.driver)
	}
	if found.sel.String != "alice" {
		t.Errorf("sel.String = %q, want alice", found.sel.String)
	}
	if len(found.sel.Cells) != 3 || found.sel.Cells[2] != "alice" {
		t.Errorf("sel.Cells = %v, want [root team alice]", found.sel.Cells)
	}
}

// TestTagCursorFocusedListDirect confirms the non-batch path still
// works — a bare Cmd returning a SelectedChangedMsg gets tagged
// without extra wrapping.
func TestTagCursorFocusedListDirect(t *testing.T) {
	directCmd := func() tea.Msg { return list.SelectedChangedMsg{Index: 1, Item: "banana"} }
	wrapped := tagCursorFocused(directCmd, "my_list")
	got, ok := wrapped().(taggedCursorMsg)
	if !ok {
		t.Fatalf("want taggedCursorMsg, got %T", wrapped())
	}
	if got.driver != "my_list" || got.sel.String != "banana" {
		t.Errorf("tagged = %+v, want driver=my_list sel.String=banana", got)
	}
}

// TestTagCursorFocusedEmptyTransitions confirms the empty flag
// propagates through both direct and batched paths.
func TestTagCursorFocusedEmptyTransitions(t *testing.T) {
	direct := tagCursorFocused(func() tea.Msg { return list.SelectedChangedMsg{Empty: true} }, "x")
	got, _ := direct().(taggedCursorMsg)
	if !got.empty {
		t.Errorf("direct: want empty=true, got %+v", got)
	}
}
