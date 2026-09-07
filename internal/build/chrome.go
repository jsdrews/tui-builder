package build

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/jsdrews/tuilib/pkg/glyph"
	"github.com/jsdrews/tuilib/pkg/pane"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// ApplyChrome writes the config's `app.glyphs` / `app.borders`
// overrides onto every theme in themes and returns the result.
//
// Every theme, not just the one `app.theme` names: a palette is a
// choice of color, while glyphs and border shapes are a choice of
// vocabulary. Cycling palettes at runtime (`t`) walks the whole slice,
// and a cursor that turned back into "▸" partway through that cycle
// would read as a bug.
//
// Unset fields are left alone rather than filled in here. tuilib
// resolves a zero border shape to its own default and a zero glyph via
// glyph.Default, so "unset" keeps exactly one meaning and it lives
// upstream — which is also why a palette that ships its own glyphs one
// day will keep the ones this config doesn't mention.
func ApplyChrome(themes []theme.Theme, a *cfg.App) []theme.Theme {
	out := make([]theme.Theme, len(themes))
	for i, t := range themes {
		t.Glyphs = overrideGlyphs(t.Glyphs, &a.Glyphs)
		if b, ok := borderShape(a.Borders.Active); ok {
			t.BorderShapeActive = b
		}
		if b, ok := borderShape(a.Borders.Inactive); ok {
			t.BorderShapeInactive = b
		}
		if b, ok := borderShape(a.Borders.Overlay); ok {
			t.BorderShapeOverlay = b
		}
		if s, ok := slotBrackets(a.Borders.SlotBrackets); ok {
			t.SlotBrackets = s
		}
		out[i] = t
	}
	return out
}

// overrideGlyphs copies the non-empty fields of o over base. Empty
// means "leave it", never "blank it" — a config that sets one arrow
// keeps the twelve marks it didn't mention.
func overrideGlyphs(base glyph.Set, o *cfg.Glyphs) glyph.Set {
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&base.Cursor, o.Cursor},
		{&base.Mark, o.Mark},
		{&base.ExpandOpen, o.ExpandOpen},
		{&base.ExpandClosed, o.ExpandClosed},
		{&base.Rule, o.Rule},
		{&base.ScrollThumb, o.ScrollThumb},
		{&base.ScrollTrack, o.ScrollTrack},
		{&base.HScrollThumb, o.HScrollThumb},
		{&base.HScrollTrack, o.HScrollTrack},
		{&base.SortAsc, o.SortAsc},
		{&base.SortDesc, o.SortDesc},
		{&base.ColumnSep, o.ColumnSep},
		{&base.Placeholder, o.Placeholder},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	return base
}

// borderShape maps a config border name to its lipgloss constructor.
// Names are validated at load against cfg.BorderShapeNames, so a miss
// here means the two lists drifted; returning false keeps the palette's
// own shape rather than forcing a wrong one. TestBorderNamesAllMap
// walks the list so that can't happen quietly.
func borderShape(name string) (lipgloss.Border, bool) {
	switch name {
	case "normal":
		return lipgloss.NormalBorder(), true
	case "rounded":
		return lipgloss.RoundedBorder(), true
	case "thick":
		return lipgloss.ThickBorder(), true
	case "double":
		return lipgloss.DoubleBorder(), true
	case "hidden":
		return lipgloss.HiddenBorder(), true
	case "block":
		return lipgloss.BlockBorder(), true
	case "ascii":
		return lipgloss.ASCIIBorder(), true
	}
	return lipgloss.Border{}, false
}

// slotBrackets maps a config slot-bracket name onto tuilib's enum. The
// zero value is SlotBracketsNone, which is also what "none" means, so
// the bool is what distinguishes "the author asked for none" from "the
// author said nothing".
func slotBrackets(name string) (pane.SlotBracketStyle, bool) {
	switch name {
	case "none":
		return pane.SlotBracketsNone, true
	case "corners":
		return pane.SlotBracketsCorners, true
	case "tees":
		return pane.SlotBracketsTees, true
	}
	return pane.SlotBracketsNone, false
}
