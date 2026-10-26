package build

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/jsdrews/tuilib/pkg/glyph"
	"github.com/jsdrews/tuilib/pkg/surface"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// Surfaces are tuilib's built-in surfaces with the config's chrome on
// each, in cycle order, starting from the one named start (the first
// when start names none). Every surface gets the chrome, so cycling
// (`T`) keeps the brackets and divider, and Outlined keeps its shapes
// whenever it's reached — chosen, cycled to, or fallen back to.
func Surfaces(a *cfg.App, start string) []surface.Surface {
	all := make([]surface.Surface, 0, len(cfg.SurfaceNames))
	head := 0
	for _, name := range cfg.SurfaceNames {
		s, ok := builtinSurface(name)
		if !ok {
			continue
		}
		if name == start {
			head = len(all)
		}
		all = append(all, ChromeSurface(s, a))
	}
	return append(all[head:], all[:head]...)
}

// builtinSurface maps a config surface name to tuilib's built-in.
// TestSurfaceNamesAllMap keeps cfg.SurfaceNames and this in step.
func builtinSurface(name string) (surface.Surface, bool) {
	for _, s := range surface.Builtins() {
		if s.Name == name {
			return s, true
		}
	}
	return surface.Surface{}, false
}

// ChromeSurface is base with the config's `app.borders` and
// `app.glyphs.rule` applied: the line shape of every pane and of what
// floats over a screen, how a title meets the line, and the divider
// under a pinned header.
//
// Unset fields leave base alone, so an empty app block draws exactly
// what tuilib's own Outlined draws. The border shapes only mean
// something on a lined surface; on any other base they're skipped.
func ChromeSurface(base surface.Surface, a *cfg.App) surface.Surface {
	s := base
	if _, lined := s.Lined(); lined {
		if b, ok := borderShape(a.Borders.Active); ok {
			overlay := s.Overlay
			s = surface.OutlinedWith(b)
			s.Name = base.Name
			s.Overlay = overlay
		}
		if b, ok := borderShape(a.Borders.Overlay); ok {
			o := surface.OutlinedWith(b)
			o.Name = base.Name + "-overlay"
			s.Overlay = &o
		}
	}
	apply := func(s *surface.Surface) {
		if br, ok := slotBrackets(a.Borders.SlotBrackets); ok {
			s.Brackets = br
		}
		if a.Glyphs.Rule != "" {
			s.Divider.Glyph = a.Glyphs.Rule
		}
	}
	apply(&s)
	if s.Overlay != nil {
		o := *s.Overlay
		apply(&o)
		s.Overlay = &o
	}
	return s
}

// Glyphs is the config's `app.glyphs` as a glyph set, for the app shell,
// which puts it on every theme it composes — the screens' and its own
// overlays' alike. Unset fields stay empty and fall back to glyph.Default.
func Glyphs(a *cfg.App) glyph.Set { return overrideGlyphs(glyph.Set{}, &a.Glyphs) }

// overrideGlyphs copies the non-empty fields of o over base. Empty
// means "leave it", never "blank it" — a config that sets one arrow
// keeps the twelve marks it didn't mention. Rule isn't here: it's the
// surface's divider (ChromeSurface).
func overrideGlyphs(base glyph.Set, o *cfg.Glyphs) glyph.Set {
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&base.Cursor, o.Cursor},
		{&base.Mark, o.Mark},
		{&base.ExpandOpen, o.ExpandOpen},
		{&base.ExpandClosed, o.ExpandClosed},
		{&base.ScrollThumb, o.ScrollThumb},
		{&base.ScrollTrack, o.ScrollTrack},
		{&base.HScrollThumb, o.HScrollThumb},
		{&base.HScrollTrack, o.HScrollTrack},
		{&base.SortAsc, o.SortAsc},
		{&base.SortDesc, o.SortDesc},
		{&base.ColumnSep, o.ColumnSep},
		{&base.Placeholder, o.Placeholder},
		{&base.ActivityOK, o.ActivityOK},
		{&base.ActivityFail, o.ActivityFail},
		{&base.ActivityChanged, o.ActivityChanged},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	return base
}

// borderShape maps a config border name to its lipgloss constructor.
// Names are validated at load against cfg.BorderShapeNames, so a miss
// here means the two lists drifted; returning false keeps the surface's
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
// zero value is BracketsNone, which is also what "none" means, so the
// bool is what distinguishes "the author asked for none" from "the
// author said nothing".
func slotBrackets(name string) (surface.Brackets, bool) {
	switch name {
	case "none":
		return surface.BracketsNone, true
	case "corners":
		return surface.BracketsCorners, true
	case "tees":
		return surface.BracketsTees, true
	}
	return surface.BracketsNone, false
}
