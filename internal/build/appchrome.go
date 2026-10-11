package build

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"

	"github.com/jsdrews/tuilib/pkg/form"
	"github.com/jsdrews/tuilib/pkg/list"
	"github.com/jsdrews/tuilib/pkg/table"
	"github.com/jsdrews/tuilib/pkg/theme"
	"github.com/jsdrews/tuilib/pkg/tree"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// appChrome is the part of `app:` that reaches inside components and
// forms rather than the shell: the keymap every list, table and tree
// uses, and how forms lay out and mask. There is one config per run, so
// it's set once at startup (SetAppChrome) rather than threaded through
// every builder, and the rebuild after a theme swap reads the same
// values. The zero value is tuilib's defaults, which is what tests get.
var appChrome struct {
	keys        map[string]cfg.KeyList
	formSpacing int
	mask        rune
}

// SetAppChrome records the app-wide component and form settings. Call it
// once, after the config loads and before any screen is built.
func SetAppChrome(a *cfg.App) {
	appChrome.keys = a.Keys
	appChrome.formSpacing = a.FormSpacing
	appChrome.mask = 0
	if a.Glyphs.Mask != "" {
		appChrome.mask = []rune(a.Glyphs.Mask)[0]
	}
}

// FormOptions is th.Form() with the app's form spacing applied. Every
// form tui-builder opens (action inputs, boot prompts) starts here.
func FormOptions(th theme.Theme) form.Options {
	opts := th.Form()
	if appChrome.formSpacing > 0 {
		n := appChrome.formSpacing
		opts.FieldSpacing = &n
	}
	return opts
}

// MaskChar is the character a masked field draws per typed character;
// 0 keeps tuilib's default.
func MaskChar() rune { return appChrome.mask }

// remap points each named binding at the app's keys for that name,
// keeping the binding's own help description. Names the component
// doesn't have are simply absent from bindings.
func remap(bindings map[string]*key.Binding) {
	for name, keys := range appChrome.keys {
		b, ok := bindings[name]
		if !ok || len(keys) == 0 {
			continue
		}
		*b = key.NewBinding(
			key.WithKeys(keys...),
			key.WithHelp(strings.Join(keys, "/"), b.Help().Desc),
		)
	}
}

func remapListKeys(k *list.Keys) {
	remap(map[string]*key.Binding{
		"up": &k.Up, "down": &k.Down, "top": &k.Top, "bottom": &k.Bottom,
		"half_up": &k.HalfUp, "half_down": &k.HalfDown, "filter": &k.Filter,
		"mark": &k.Mark, "mark_all": &k.MarkAll, "clear_marks": &k.ClearMarks,
		"mark_range": &k.MarkRange,
	})
}

func remapTableKeys(k *table.Keys) {
	remap(map[string]*key.Binding{
		"up": &k.Up, "down": &k.Down, "top": &k.Top, "bottom": &k.Bottom,
		"half_up": &k.HalfUp, "half_down": &k.HalfDown, "filter": &k.Filter,
		"mark": &k.Mark, "mark_all": &k.MarkAll, "clear_marks": &k.ClearMarks,
		"mark_range": &k.MarkRange,
		"sort_prev":  &k.SortPrev, "sort_next": &k.SortNext, "sort_dir": &k.SortDir,
		"col_prev": &k.ColPrev, "col_next": &k.ColNext,
	})
}

// remapTreeKeys maps `filter` onto the tree's search: on a tree `/` is
// search, and the binding is named for that key in lists and tables.
func remapTreeKeys(k *tree.Keys) {
	remap(map[string]*key.Binding{
		"up": &k.Up, "down": &k.Down, "top": &k.Top, "bottom": &k.Bottom,
		"filter": &k.Search, "mark": &k.Mark, "mark_all": &k.MarkAll,
		"clear_marks": &k.ClearMarks, "mark_range": &k.MarkRange,
	})
}
