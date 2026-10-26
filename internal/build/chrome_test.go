package build

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jsdrews/tuilib/pkg/glyph"
	"github.com/jsdrews/tuilib/pkg/palette"
	"github.com/jsdrews/tuilib/pkg/surface"
	"github.com/jsdrews/tuilib/pkg/theme"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// chromeTheme is what a screen is handed for a: a palette composed with
// the config's surface, with the config's glyphs put back.
func chromeTheme(t *testing.T, p palette.Palette, a *cfg.App) theme.Theme {
	t.Helper()
	withAppChrome(t, a)
	caps := surface.Caps{Profile: lipgloss.ColorProfile(), BlockGlyphs: true}
	th := theme.Compose(p, ChromeSurface(surface.Outlined, a), caps)
	th.Glyphs = Glyphs(a)
	return th
}

// sameLines compares the eight glyphs a lined surface keeps of a
// lipgloss.Border; the middle joins aren't part of a surface.
func sameLines(a, b lipgloss.Border) bool {
	return a.Top == b.Top && a.Bottom == b.Bottom && a.Left == b.Left && a.Right == b.Right &&
		a.TopLeft == b.TopLeft && a.TopRight == b.TopRight &&
		a.BottomLeft == b.BottomLeft && a.BottomRight == b.BottomRight
}

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
		t.Error(`borderShape treated "" as a shape; empty must mean "leave the surface alone"`)
	}
	if _, ok := slotBrackets(""); ok {
		t.Error(`slotBrackets treated "" as a style; empty must mean "leave the surface alone"`)
	}
}

// An empty app block must draw exactly tuilib's Outlined: filling
// anything in here would freeze today's defaults into every config.
func TestChromeSurfaceNoOpIsOutlined(t *testing.T) {
	got := ChromeSurface(surface.Outlined, &cfg.App{})
	want := surface.Outlined
	gb, _ := got.Lined()
	wb, _ := want.Lined()
	if gb != wb || got.Brackets != want.Brackets || got.Divider != want.Divider || got.Name != want.Name {
		t.Errorf("empty app block changed the surface: got %+v", got)
	}
	go_, _ := got.Overlay.Lined()
	wo, _ := want.Overlay.Lined()
	if go_ != wo || got.Overlay.Brackets != want.Overlay.Brackets || got.Overlay.Divider != want.Overlay.Divider {
		t.Error("empty app block changed the overlay form")
	}
}

// A palette is a choice of colour; glyphs and shapes are a choice of
// vocabulary. Pressing `t` composes the next palette with the same
// surface and the screen puts the glyphs back, so every palette gets
// them.
func TestChromeAppliesToEveryPalette(t *testing.T) {
	a := &cfg.App{
		Glyphs:  cfg.Glyphs{Cursor: "→"},
		Borders: cfg.Borders{Overlay: "double", SlotBrackets: "tees"},
	}
	for _, p := range palette.All() {
		tm := chromeTheme(t, p, a)
		if tm.Glyphs.Cursor != "→" {
			t.Errorf("%s: cursor = %q, want →", p.Name, tm.Glyphs.Cursor)
		}
		if !sameLines(tm.Confirm().ActiveBorder, lipgloss.DoubleBorder()) {
			t.Errorf("%s: overlay shape was not applied", p.Name)
		}
		if tm.Table().SlotBrackets != surface.BracketsTees {
			t.Errorf("%s: slot brackets = %v, want tees", p.Name, tm.Table().SlotBrackets)
		}
	}
}

// The plan's headline requirement: overriding one arrow must not blank
// the others. Resolve() is what a component sees.
func TestPartialGlyphsResolveFromDefaults(t *testing.T) {
	got := chromeTheme(t, palette.Nord(), &cfg.App{Glyphs: cfg.Glyphs{ExpandOpen: "v", ExpandClosed: ">"}}).Glyphs.Resolve()
	if got.ExpandOpen != "v" || got.ExpandClosed != ">" {
		t.Errorf("overrides lost: open=%q closed=%q", got.ExpandOpen, got.ExpandClosed)
	}
	def := glyph.Default()
	if got.Cursor != def.Cursor || got.ColumnSep != def.ColumnSep || got.Placeholder != def.Placeholder {
		t.Errorf("unset glyphs should resolve to the defaults, got %+v", got)
	}
}

// Every field has to be wired, and a long copy loop is exactly where one
// gets forgotten. Rule is the surface's divider now, not a glyph.
func TestEveryGlyphFieldIsCopied(t *testing.T) {
	a := &cfg.App{Glyphs: cfg.Glyphs{
		Cursor: "1", Mark: "2", ExpandOpen: "3", ExpandClosed: "4",
		Rule: "5", ScrollThumb: "6", ScrollTrack: "7",
		HScrollThumb: "8", HScrollTrack: "9", SortAsc: "a",
		SortDesc: "b", ColumnSep: "c", Placeholder: "d",
		ActivityOK: "e", ActivityFail: "f", ActivityChanged: "g",
	}}
	want := glyph.Set{
		Cursor: "1", Mark: "2", ExpandOpen: "3", ExpandClosed: "4",
		ScrollThumb: "6", ScrollTrack: "7",
		HScrollThumb: "8", HScrollTrack: "9", SortAsc: "a",
		SortDesc: "b", ColumnSep: "c", Placeholder: "d",
		ActivityOK: "e", ActivityFail: "f", ActivityChanged: "g",
	}
	if got := overrideGlyphs(glyph.Set{}, &a.Glyphs); got != want {
		t.Errorf("glyphs not copied field-for-field:\n got %+v\nwant %+v", got, want)
	}
	if got := ChromeSurface(surface.Outlined, a).Divider.Glyph; got != "5" {
		t.Errorf("rule should be the surface divider, got %q", got)
	}
}

// ChromeSurface must not scribble on the surface it was handed: the
// built-ins are package variables.
func TestChromeSurfaceDoesNotMutateInput(t *testing.T) {
	before := *surface.Outlined.Overlay
	ChromeSurface(surface.Outlined, &cfg.App{Borders: cfg.Borders{SlotBrackets: "tees"}, Glyphs: cfg.Glyphs{Rule: "="}})
	if surface.Outlined.Brackets != surface.BracketsNone || surface.Outlined.Divider.Glyph != "─" {
		t.Error("surface.Outlined was mutated")
	}
	if after := *surface.Outlined.Overlay; after.Brackets != before.Brackets || after.Divider != before.Divider {
		t.Error("surface.Outlined's overlay form was mutated")
	}
}

// The overrides only matter if they reach the components. theme.Table()
// is the seam every builder in this package goes through.
func TestChromeReachesComponentOptions(t *testing.T) {
	th := chromeTheme(t, palette.Nord(), &cfg.App{
		Glyphs:  cfg.Glyphs{SortAsc: "^", ColumnSep: ":"},
		Borders: cfg.Borders{Active: "rounded", SlotBrackets: "corners"},
	})
	tbl := th.Table()
	if tbl.Glyphs.SortAsc != "^" || tbl.Glyphs.ColumnSep != ":" {
		t.Errorf("table glyphs = %+v", tbl.Glyphs)
	}
	if tbl.Glyphs.Cursor != glyph.Default().Cursor {
		t.Errorf("table cursor = %q, want the default", tbl.Glyphs.Cursor)
	}
	if tbl.SlotBrackets != surface.BracketsCorners {
		t.Errorf("table slot brackets = %v, want corners", tbl.SlotBrackets)
	}
	if !sameLines(tbl.ActiveBorder, lipgloss.RoundedBorder()) || !sameLines(tbl.InactiveBorder, lipgloss.RoundedBorder()) {
		t.Error("`active:` should be every pane's shape, focused or not")
	}
	if th.List().Glyphs.SortAsc != "^" {
		t.Error("list did not receive the glyph override")
	}
}

// `overlay:` reaches the things drawn above a screen, and nothing else.
// The output console looks like it belongs here and doesn't: it is a
// pushed screen, so it takes the pane shape.
func TestOverlayShapeReachesOverlaysOnly(t *testing.T) {
	th := chromeTheme(t, palette.Nord(), &cfg.App{Borders: cfg.Borders{Active: "rounded", Overlay: "double"}})
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
		if !sameLines(c.border, overlay) {
			t.Errorf("%s did not get the overlay shape", c.what)
		}
	}
	if !sameLines(th.Table().ActiveBorder, active) || !sameLines(th.Logview().ActiveBorder, active) {
		t.Error("ordinary components should take the active shape, not the overlay one")
	}
}

// Without `overlay:`, a rounded `active:` keeps Outlined's thick modals.
func TestActiveShapeKeepsThickOverlay(t *testing.T) {
	th := chromeTheme(t, palette.Nord(), &cfg.App{Borders: cfg.Borders{Active: "rounded"}})
	if !sameLines(th.Confirm().ActiveBorder, lipgloss.ThickBorder()) {
		t.Error("setting active: replaced the overlay shape")
	}
}

// The help overlay is the screen a user reaches for when they are
// already lost, so it is the worst place for the chrome to revert to
// defaults.
func TestChromeReachesHelpOverlay(t *testing.T) {
	th := chromeTheme(t, palette.Nord(), &cfg.App{
		Glyphs:  cfg.Glyphs{Cursor: ">", Rule: "-"},
		Borders: cfg.Borders{SlotBrackets: "corners"},
	})
	ov := th.HelpOverlay()
	if ov.Glyphs.Cursor != ">" {
		t.Errorf("help overlay cursor = %q, want >", ov.Glyphs.Cursor)
	}
	if ov.Glyphs.ColumnSep != glyph.Default().ColumnSep {
		t.Errorf("help overlay column_sep = %q, want the default", ov.Glyphs.ColumnSep)
	}
	if ov.SlotBrackets != surface.BracketsCorners {
		t.Errorf("help overlay slot brackets = %v, want corners", ov.SlotBrackets)
	}
	if th.Surface.Overlay().Surface().Divider.Glyph != "-" {
		t.Error("rule did not reach the overlay form's divider")
	}
}

// The config validator accepts a surface name only if this package can
// map it.
func TestSurfaceNamesAllMap(t *testing.T) {
	for _, name := range cfg.SurfaceNames {
		if _, ok := builtinSurface(name); !ok {
			t.Errorf("cfg.SurfaceNames has %q but tuilib has no such built-in", name)
		}
	}
	if len(cfg.SurfaceNames) != len(surface.Builtins()) {
		t.Errorf("cfg.SurfaceNames lists %d surfaces, tuilib ships %d", len(cfg.SurfaceNames), len(surface.Builtins()))
	}
}

// The starting surface comes first and the rest follow in cycle order;
// every one carries the chrome, and Outlined keeps the shapes wherever
// it lands in the cycle.
func TestSurfacesStartFirstWithChrome(t *testing.T) {
	a := &cfg.App{Borders: cfg.Borders{Active: "rounded", SlotBrackets: "tees"}}
	got := Surfaces(a, "banded")
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
		if s.Brackets != surface.BracketsTees {
			t.Errorf("%s: slot brackets not applied", s.Name)
		}
	}
	if want := "banded elevated accented outlined filled"; strings.Join(names, " ") != want {
		t.Errorf("order = %v, want %s", names, want)
	}
	if b, _ := got[3].Lined(); !sameLines(b, lipgloss.RoundedBorder()) {
		t.Error("outlined lost app.borders.active when it isn't first")
	}
	if Surfaces(a, "")[0].Name != "outlined" {
		t.Error("no start should begin on outlined")
	}
}

// The filled surfaces carry a filled field form for inputs, toggles and
// the filter bar; the config's chrome must keep it rather than leave
// them drawing an outlined box under a filled card.
func TestChromeKeepsFilledFieldForms(t *testing.T) {
	caps := surface.Caps{Profile: termenv.TrueColor, BlockGlyphs: true}
	a := &cfg.App{Borders: cfg.Borders{Active: "rounded", SlotBrackets: "tees"}, Glyphs: cfg.Glyphs{Rule: "="}}
	for _, s := range Surfaces(a, "") {
		f := theme.Compose(palette.Nord(), s, caps).Input().Surface.Surface()
		switch s.Name {
		case "filled", "banded", "elevated":
			if !f.Filled() {
				t.Errorf("%s: input field form %q isn't filled", s.Name, f.Name)
			}
		case "outlined":
			if b, lined := f.Lined(); !lined || !sameLines(b, lipgloss.RoundedBorder()) {
				t.Errorf("outlined: input field should be its own rounded lines, got %q", f.Name)
			}
		}
	}
}
