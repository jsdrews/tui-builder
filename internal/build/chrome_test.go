package build

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/jsdrews/tuilib/pkg/glyph"
	"github.com/jsdrews/tuilib/pkg/pane"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// The config validator accepts a name only if this package can map it.
// Two lists in two packages is the drift risk the whole design has; this
// is what keeps them honest.
func TestBorderNamesAllMap(t *testing.T) {
	for _, name := range cfg.BorderShapeNames {
		if _, ok := borderShape(name); !ok {
			t.Errorf("cfg.BorderShapeNames has %q but borderShape does not map it", name)
		}
	}
	for _, name := range cfg.SlotBracketNames {
		if _, ok := slotBrackets(name); !ok {
			t.Errorf("cfg.SlotBracketNames has %q but slotBrackets does not map it", name)
		}
	}
}

func TestBorderShapeUnknownNameNotOK(t *testing.T) {
	if _, ok := borderShape("wobbly"); ok {
		t.Error("borderShape accepted a name the validator rejects")
	}
	if _, ok := borderShape(""); ok {
		t.Error(`borderShape treated "" as a shape; empty must mean "leave the palette alone"`)
	}
	if _, ok := slotBrackets(""); ok {
		t.Error(`slotBrackets treated "" as a style; empty must mean "leave the palette alone"`)
	}
}

// An empty app block must leave every shape zero, because zero is what
// tuilib reads as "use my default". Filling them in here would freeze
// today's defaults into every config.
func TestApplyChromeNoOpLeavesShapesZero(t *testing.T) {
	got := ApplyChrome(theme.All(), &cfg.App{})
	for _, tm := range got {
		if (tm.BorderShapeActive != lipgloss.Border{}) {
			t.Errorf("%s: active shape should stay zero", tm.Name)
		}
		if (tm.BorderShapeInactive != lipgloss.Border{}) {
			t.Errorf("%s: inactive shape should stay zero", tm.Name)
		}
		if (tm.BorderShapeOverlay != lipgloss.Border{}) {
			t.Errorf("%s: overlay shape should stay zero", tm.Name)
		}
		if (tm.Glyphs != glyph.Set{}) {
			t.Errorf("%s: glyphs should stay zero", tm.Name)
		}
		if tm.SlotBrackets != pane.SlotBracketsNone {
			t.Errorf("%s: slot brackets should stay at the zero value", tm.Name)
		}
	}
}

// A palette is a choice of color; glyphs are a choice of vocabulary.
// Pressing `t` walks the whole slice, so the overrides have to be on
// all of it.
func TestApplyChromeAppliesToEveryTheme(t *testing.T) {
	a := &cfg.App{
		Glyphs:  cfg.Glyphs{Cursor: "→"},
		Borders: cfg.Borders{Overlay: "double", SlotBrackets: "tees"},
	}
	got := ApplyChrome(theme.All(), a)
	if len(got) != len(theme.All()) {
		t.Fatalf("got %d themes, want %d", len(got), len(theme.All()))
	}
	for _, tm := range got {
		if tm.Glyphs.Cursor != "→" {
			t.Errorf("%s: cursor = %q, want →", tm.Name, tm.Glyphs.Cursor)
		}
		if tm.BorderShapeOverlay != lipgloss.DoubleBorder() {
			t.Errorf("%s: overlay shape was not applied", tm.Name)
		}
		if tm.SlotBrackets != pane.SlotBracketsTees {
			t.Errorf("%s: slot brackets = %v, want tees", tm.Name, tm.SlotBrackets)
		}
	}
}

// The plan's headline requirement: overriding one arrow must not blank
// the other twelve. Resolve() is what a component sees.
func TestPartialGlyphsResolveFromDefaults(t *testing.T) {
	a := &cfg.App{Glyphs: cfg.Glyphs{ExpandOpen: "v", ExpandClosed: ">"}}
	got := ApplyChrome([]theme.Theme{theme.Nord()}, a)[0].Glyphs.Resolve()

	if got.ExpandOpen != "v" || got.ExpandClosed != ">" {
		t.Errorf("overrides lost: open=%q closed=%q", got.ExpandOpen, got.ExpandClosed)
	}
	def := glyph.Default()
	if got.Cursor != def.Cursor {
		t.Errorf("cursor = %q, want the default %q", got.Cursor, def.Cursor)
	}
	if got.ColumnSep != def.ColumnSep {
		t.Errorf("column_sep = %q, want the default %q", got.ColumnSep, def.ColumnSep)
	}
	if got.Placeholder != def.Placeholder {
		t.Errorf("placeholder = %q, want the default %q", got.Placeholder, def.Placeholder)
	}
}

// Every field has to be wired, and a 13-entry copy loop is exactly where
// one gets forgotten.
func TestEveryGlyphFieldIsCopied(t *testing.T) {
	a := &cfg.App{Glyphs: cfg.Glyphs{
		Cursor: "1", Mark: "2", ExpandOpen: "3", ExpandClosed: "4",
		Rule: "5", ScrollThumb: "6", ScrollTrack: "7",
		HScrollThumb: "8", HScrollTrack: "9", SortAsc: "a",
		SortDesc: "b", ColumnSep: "c", Placeholder: "d",
	}}
	want := glyph.Set{
		Cursor: "1", Mark: "2", ExpandOpen: "3", ExpandClosed: "4",
		Rule: "5", ScrollThumb: "6", ScrollTrack: "7",
		HScrollThumb: "8", HScrollTrack: "9", SortAsc: "a",
		SortDesc: "b", ColumnSep: "c", Placeholder: "d",
	}
	if got := ApplyChrome([]theme.Theme{theme.Nord()}, a)[0].Glyphs; got != want {
		t.Errorf("glyphs not copied field-for-field:\n got %+v\nwant %+v", got, want)
	}
}

// ApplyChrome must not scribble on the slice it was handed — theme.All()
// returns fresh values, but the contract shouldn't depend on that.
func TestApplyChromeDoesNotMutateInput(t *testing.T) {
	in := []theme.Theme{theme.Nord()}
	ApplyChrome(in, &cfg.App{Glyphs: cfg.Glyphs{Cursor: "→"}})
	if in[0].Glyphs.Cursor != "" {
		t.Errorf("input theme was mutated: cursor = %q", in[0].Glyphs.Cursor)
	}
}

// The overrides only matter if they reach the components. theme.Table()
// is the seam every builder in this package goes through.
func TestChromeReachesComponentOptions(t *testing.T) {
	a := &cfg.App{
		Glyphs:  cfg.Glyphs{SortAsc: "^", ColumnSep: ":"},
		Borders: cfg.Borders{Active: "rounded", SlotBrackets: "corners"},
	}
	th := ApplyChrome([]theme.Theme{theme.Nord()}, a)[0]

	tbl := th.Table()
	if tbl.Glyphs.SortAsc != "^" {
		t.Errorf("table sort_asc = %q, want ^", tbl.Glyphs.SortAsc)
	}
	if tbl.Glyphs.ColumnSep != ":" {
		t.Errorf("table column_sep = %q, want :", tbl.Glyphs.ColumnSep)
	}
	// Untouched fields still arrive resolved, not empty.
	if tbl.Glyphs.Cursor != glyph.Default().Cursor {
		t.Errorf("table cursor = %q, want the default", tbl.Glyphs.Cursor)
	}
	if tbl.SlotBrackets != pane.SlotBracketsCorners {
		t.Errorf("table slot brackets = %v, want corners", tbl.SlotBrackets)
	}
	if th.List().Glyphs.SortAsc != "^" {
		t.Error("list did not receive the glyph override")
	}
}

// `overlay:` is a different shape from `active:`, and it reaches a
// different set of builders — the things drawn *above* a screen. The
// output console looks like it belongs here and doesn't: it is a pushed
// screen, so it takes the pane shape. Pin the split, because the docs
// and examples/chrome.yaml both make the claim.
//
// The help overlay joined this list in tuilib v0.25, which is exactly
// how the list goes stale: `?` was a footer panel when chrome.yaml was
// written and is a modal now.
func TestOverlayShapeReachesOverlaysOnly(t *testing.T) {
	a := &cfg.App{Borders: cfg.Borders{Active: "rounded", Overlay: "double"}}
	th := ApplyChrome([]theme.Theme{theme.Nord()}, a)[0]

	overlay, active := lipgloss.DoubleBorder(), lipgloss.RoundedBorder()
	for _, c := range []struct {
		what   string
		border lipgloss.Border
	}{
		{"confirm dialog", th.Confirm().ActiveBorder},
		{"alert dialog", th.Alert().ActiveBorder},
		{"action menu", th.Actions().ActiveBorder},
		{"help overlay", th.HelpOverlay().ActiveBorder},
	} {
		if c.border != overlay {
			t.Errorf("%s did not get the overlay shape", c.what)
		}
	}
	// Ordinary components stay on the pane shape.
	if got := th.Table().ActiveBorder; got != active {
		t.Error("table should take the active shape, not the overlay one")
	}
	if got := th.Logview().ActiveBorder; got != active {
		t.Error("logview should take the active shape, not the overlay one")
	}
}

// The help overlay is the screen a user reaches for when they are
// already lost, so it is the worst place for the chrome to revert to
// defaults. v0.25 wires Glyphs and SlotBrackets through theme.HelpOverlay;
// this pins that they arrive.
func TestChromeReachesHelpOverlay(t *testing.T) {
	a := &cfg.App{
		Glyphs:  cfg.Glyphs{Cursor: ">", Rule: "-"},
		Borders: cfg.Borders{SlotBrackets: "corners"},
	}
	th := ApplyChrome([]theme.Theme{theme.Nord()}, a)[0]

	ov := th.HelpOverlay()
	if ov.Glyphs.Cursor != ">" {
		t.Errorf("help overlay cursor = %q, want >", ov.Glyphs.Cursor)
	}
	if ov.Glyphs.Rule != "-" {
		t.Errorf("help overlay rule = %q, want -", ov.Glyphs.Rule)
	}
	if ov.Glyphs.ColumnSep != glyph.Default().ColumnSep {
		t.Errorf("help overlay column_sep = %q, want the default", ov.Glyphs.ColumnSep)
	}
	if ov.SlotBrackets != pane.SlotBracketsCorners {
		t.Errorf("help overlay slot brackets = %v, want corners", ov.SlotBrackets)
	}
}
