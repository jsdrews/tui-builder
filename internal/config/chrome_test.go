package config

import (
	"strings"
	"testing"
)

func TestBorderNamesAccepted(t *testing.T) {
	for _, name := range BorderShapeNames {
		c := promptConfig()
		c.App.Borders = Borders{Active: name, Inactive: name, Overlay: name}
		if err := c.Validate(); err != nil {
			t.Errorf("border %q: Validate: %v", name, err)
		}
	}
}

// An unknown name would otherwise keep the default shape, so the config
// change just appears not to have worked.
func TestBorderUnknownNameRejected(t *testing.T) {
	c := promptConfig()
	c.App.Borders = Borders{Inactive: "wobbly"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for an unknown border name")
	}
	if !strings.Contains(err.Error(), "app.borders.inactive") || !strings.Contains(err.Error(), "wobbly") {
		t.Errorf("error should name the field and the bad value, got: %v", err)
	}
	// And the message should list what is allowed.
	if !strings.Contains(err.Error(), "rounded") {
		t.Errorf("error should list the valid names, got: %v", err)
	}
}

func TestSlotBracketNamesAccepted(t *testing.T) {
	for _, name := range SlotBracketNames {
		c := promptConfig()
		c.App.Borders = Borders{SlotBrackets: name}
		if err := c.Validate(); err != nil {
			t.Errorf("slot_brackets %q: Validate: %v", name, err)
		}
	}
}

func TestSlotBracketUnknownNameRejected(t *testing.T) {
	c := promptConfig()
	c.App.Borders = Borders{SlotBrackets: "square"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for an unknown slot bracket style")
	}
	if !strings.Contains(err.Error(), "app.borders.slot_brackets") || !strings.Contains(err.Error(), "square") {
		t.Errorf("error should name the field and the bad value, got: %v", err)
	}
}

// Empty means "keep the library default", so a partial block is the
// common case and must stay legal.
func TestPartialChromeBlockValid(t *testing.T) {
	c := promptConfig()
	c.App.Glyphs = Glyphs{Cursor: "→"}
	c.App.Borders = Borders{Overlay: "double"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A multi-character glyph renders fine on its own but shifts every row
// it is drawn on, which reads as a layout bug somewhere else.
func TestGlyphMustBeSingleCharacter(t *testing.T) {
	c := promptConfig()
	c.App.Glyphs = Glyphs{Cursor: "=>"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected an error for a two-character cursor")
	}
	if !strings.Contains(err.Error(), "app.glyphs.cursor") {
		t.Errorf("error should name the field, got: %v", err)
	}
}

// Multi-byte is not multi-character: the defaults are themselves
// multi-byte, so a naive length check would reject every real glyph.
func TestMultiByteGlyphAccepted(t *testing.T) {
	c := promptConfig()
	c.App.Glyphs = Glyphs{
		Cursor:    "▸",
		SortAsc:   "▲",
		ColumnSep: "│",
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// Every glyph field is checked, not just the first — a typo in the
// twelfth is as invisible as one in the first.
func TestEveryGlyphFieldValidated(t *testing.T) {
	fields := map[string]func(*Glyphs){
		"cursor":         func(g *Glyphs) { g.Cursor = "xx" },
		"mark":           func(g *Glyphs) { g.Mark = "xx" },
		"expand_open":    func(g *Glyphs) { g.ExpandOpen = "xx" },
		"expand_closed":  func(g *Glyphs) { g.ExpandClosed = "xx" },
		"rule":           func(g *Glyphs) { g.Rule = "xx" },
		"scroll_thumb":   func(g *Glyphs) { g.ScrollThumb = "xx" },
		"scroll_track":   func(g *Glyphs) { g.ScrollTrack = "xx" },
		"h_scroll_thumb": func(g *Glyphs) { g.HScrollThumb = "xx" },
		"h_scroll_track": func(g *Glyphs) { g.HScrollTrack = "xx" },
		"sort_asc":       func(g *Glyphs) { g.SortAsc = "xx" },
		"sort_desc":      func(g *Glyphs) { g.SortDesc = "xx" },
		"column_sep":     func(g *Glyphs) { g.ColumnSep = "xx" },
		"placeholder":    func(g *Glyphs) { g.Placeholder = "xx" },
	}
	for field, set := range fields {
		c := promptConfig()
		set(&c.App.Glyphs)
		err := c.Validate()
		if err == nil {
			t.Errorf("%s: expected an error for a two-character glyph", field)
			continue
		}
		if !strings.Contains(err.Error(), "app.glyphs."+field) {
			t.Errorf("%s: error should name the field, got: %v", field, err)
		}
	}
}
