# Adopting tuilib v0.21 – v0.25

The bump from `v0.20.0` to `v0.25.0` is already on this branch and
compiles green. This plan covered what to do with the five releases'
new surface. **All of it has now landed**; what's left below is the
record of how, and one open question.

## The bump itself (done)

`go.mod` → `v0.24.0`. One breaking change touched us:
`list.Options.SelectedColor` (a `TerminalColor`) became `SelectedStyle`
(a full `lipgloss.Style`), so a list cursor can now take a background
and not just a foreground. `internal/build/component.go` now writes
`opts.SelectedStyle = opts.SelectedStyle.Foreground(v)` — identical
rendering, since `theme.List()` still supplies bold+Accent and we only
override the foreground. Palettes are otherwise unchanged, so existing
screens look the same.

Everything below is additive.

## What the four releases give us

| Release | Feature | Package |
|---|---|---|
| v0.21.0 | Action menu, `runner.Go`, right-click targeting | `pkg/action`, `pkg/runner` |
| v0.22.0 | Password masking in forms | `pkg/form`, `pkg/input` |
| v0.23.0 | Multi-select (marking) on list / table / tree | `pkg/{list,table,tree}/mark.go` |
| v0.24.0 | Glyph vocabulary, border shapes, slot brackets | `pkg/glyph`, `pkg/theme` |
| v0.25.0 | Help is a searchable modal, not a footer panel | `pkg/help`, `pkg/app` |

v0.25 compiles clean against this branch and the removed `HelpMaxRows`
was never set here. It did change one fact this plan asserted (see
Workstream 3) and cost one schema field: `app.help_verbose` is **gone**.
Verbose mode packs bindings inline and only opens the overlay once they
overflow the statusbar, so it made `?` conditional on terminal width —
and the key overlay is the only discovery surface a config-built screen
has. Minimal mode is now the only mode. `DisableHelpSearch` stays
unexposed for the same reason.

The v0.21–v0.24 four are not independent. Marking (v0.23) is what makes
`action.Action.Multi` mean anything, and the action menu (v0.21) is the
only surface that can tell a user whether "Delete" is about to hit one
row or twelve. Plan them together; ship them in the order at the bottom.

---

## Workstream 0 — the output console (done)

`cmd/tui-builder/main.go` doesn't set `app.Options.OutputKey`, so the
shell's output console is off. Everything downstream depends on it:
`runner.Capture` and `runner.Go` both stream into it, the statusbar
badge counts its events, and the kill picker lists what's in flight.
Without it an action's output has nowhere to go but an alert modal,
which is where main puts it today.

```yaml
app:
  output_key: o        # default "o"; empty string disables
```

Wire `OutputKey` from that knob, defaulting to `o`. Add `o` to the
reserved-key list so an action binding can't shadow it.

**Cost:** ~30 lines plus a config field. **Do this first** — it's the
one change that makes the actions work observable.

---

## Workstream 1 — actions onto `pkg/action` (done)

The `origin/feature/actions` registry (named actions, `type: exec | http`,
typed `inputs:`, `success:` expressions) landed via a merge, then moved
onto tuilib's action menu. The user-facing model is in
[`docs/components.md`](../docs/components.md) and the README; this is
only what the docs don't carry.

- **Menu only; `enter` is the one direct key.** `key:` is optional and
  menu-scoped. `key: enter` also fires directly and on double-click, via
  `activate()`. `tryAction`, `tryPush` and the screen's own
  confirm/dispatch/alert path are gone.
- **`on_key:` folded into the registry.** A push is an action with
  `push:`, and the binding's `bind:` fills the destination screen's
  `parameters:`. `section:` is gone from actions.
- **Run vs Do.** Fully bound exec and http actions use `Run`, so they go
  through the shell's path (console, badge, kill picker, cancellation).
  Pushes, interactive exec, and anything with unbound `inputs:` use `Do`.
  Prompted runs finish through `runner.GoWith`, with the confirm kept
  screen-side because the shell's fires before the form exists.
- **Fan-out** (`multi: true`) is N runs, one per marked row, each tagged
  `action.RunKey(a, target)`. It goes through `Do`, because the shell's
  `Run` path starts exactly one run per Set.
- **`exclusive:`** defaults to true for `type: http` and false otherwise,
  and is overridable either way. **Correction:** the shell only records
  in-flight runs it launched itself (its `Run` path). So exclusivity
  holds for single-target runs, while fan-out and prompted runs, which
  go out via `Do`, are not held against. Closing that needs tuilib to
  let a host register its own runs in the menu's running set.
- **Confirms are fitted to the shell modal** (`fitShellConfirm`). It's a
  fixed 52×7 box that doesn't wrap, so a long confirm used to lose its
  tail, which is where "cannot be undone" lives.
- **Right-click** needed no `RetargetMsg` handling: the shell forwards
  the press, then opens the menu. The fix was on our side. Focus now
  moves *during* the press rather than one Update later, so `Actions()`
  reads the clicked pane and not the previously focused one.

---

## Workstream 2 — multi-select (done)

### Row keys

Marks are held **by key, never by index**, so a poll that reorders rows
between marking and acting can't slide the selection onto neighbours.
`SetRows` / `SetItems` make every mark operation a deliberate no-op, so
markable components must go through `SetKeyedRows` / `SetKeyedItems`.

Most of this is free:

- **tree** — nothing to do. A node's path already is its identity, and
  it's the same path the tree uses for expansion state and cursor
  restore.
- **list** — the item string. Duplicates collapse, which is arguably
  correct for a list.
- **table** — needs one config field.

And resolving a key back to a row is trivial, not a design problem:
`TableRows` already walks the source items to build cells, so it can
build a `map[string]Selection` in the same loop. `Selection()` returns
keys; that map turns each one back into the `{String, Cells, Columns}`
that `${selection.*}` substitution wants.

**The one real decision is what the key expression is.** Note that
`TableRows` projects cells with `ds.FirstString(it, col.Value)` — it has
the *original source item* in hand, not just the rendered cells. So the
key does not have to be a column at all:

```yaml
components:
  pods:
    type: table
    markable: true
    key: metadata.uid    # any field path, same shape as a column's value:
    columns: [...]
```

That's strictly better than naming a column: the identity can be a
stable field the table never displays (`uid`, `id`, a self-link), which
is usually the right one.

**Why not default it to the first column.** The failure mode is silent
and data-dependent, and it comes in two flavours:

- *Non-unique* — a pods table across namespaces where `Name` repeats.
  Two rows collapse onto one key, so marking one marks both.
- *Volatile* — the first column is `AGE`, `STATUS`, or `RESTARTS`. The
  key changes on the next poll, and the user's marks evaporate or land
  somewhere else. tuilib protects against index drift; it cannot protect
  against a key that isn't stable.

Marking six pods and having the selection quietly change under a 2s
refresh is exactly the class of bug the keyed design exists to prevent,
and defaulting the key would reintroduce it one layer up.

**Recommend: `key:` is required when `markable: true` on a table** —
a load error the author fixes once, rather than a selection that goes
wrong at 2am. Validate that the path resolves against at least one row
at bind time if we can, and document the stability requirement.

### Binding changes

`internal/build/binding.go` switches `SetRows` → `SetKeyedRows` (and
`SetItems` → `SetKeyedItems`) when the component is markable, building
the key→`Selection` map in the same pass (see above).

`selectionFrom` grows a sibling that returns `[]Selection`: the marked
set resolved through that map, or the focused row when nothing is
marked (mirroring tuilib's `Selection()` contract, and for the same
reason — a hand-written branch that someone forgets is how a verb
quietly acts on one row when the user marked six).

### What landed, and three corrections

- **`mark_key:`, not `key:`.** `Component` already has `row_key:`
  (streaming keyed-upsert). Two adjacent fields called `key` and
  `row_key`, meaning different things, is how an author sets the wrong
  one. `mark_key:` also pairs visibly with `markable:`. Reusing
  `row_key:` itself was rejected: on a streaming table it *also* flips
  insert mode from ring-buffer to keyed-upsert, so turning marking on
  would silently change how rows arrive.
- **Static components need the keyed setters too.** `Options.Items` and
  `Options.Rows` seed cells but leave the key slices nil, so a static
  markable list or table would draw a gutter that never responds.
  `buildList` / `buildTable` call `SetKeyedItems` / `SetKeyedRows` after
  `New`. A static table keys on row position — the rows are fixed at
  load, so position cannot drift.
- **Streaming tables mark fine; no restriction needed.** The plan worried
  about the `SetRows` path. Routing it through `SetKeyedRows` covers both
  stream modes: keyed-upsert rows are stable by construction, and
  ring-buffer rows slide down as events arrive while the mark travels
  with its row. When a marked row falls off the end, tuilib simply stops
  reporting it — `Marks()` returns only keys the table still holds.

One thing the plan assumed that isn't quite true: `Rebuild` does **not**
re-deliver a source-bound table's rows, so right after a theme swap
`Marks()` reports nothing. The mark *set* is carried by `SetMarks` and
reattaches on the next fetch; `MarkCount()` is what survives in between.

### Fan-out semantics

An action with `Multi: true` over N targets: one run, or N runs?

**Recommend N runs, one per target.** `action.RunKey(a, target)` pairs
exclusivity with the target, so N runs are individually tracked,
individually cancellable, and individually logged — restarting `web`
while `api` restarts is fine, restarting `web` twice is not. One run
with a joined argv would collapse all of that, and would need a
templating story for "the list of selections" that doesn't exist.

Actions default to `Multi: false` (tuilib's default, and the safe one):
under a multi-selection the menu disables them with a reason, which the
user sees rather than discovers afterwards.

### Constraints to validate

- `markable: true` + `window:` → load error. A windowed table carries
  rows without keys; marking there is inert, and an affordance that
  silently no-ops reads as broken.
- `markable: true` on inspector → load error. No verb acts on a set of
  its fields; tuilib ships no marking there.
- Carry marks across a theme rebuild with `SetMarks`, the same way
  `internal/build` already carries the cursor (tuilib rule 4). The
  rebuild path in `component.go` is where this goes.

Note for docs: marks survive filtering, because a key doesn't care
whether its row is on screen. A user can mark a row, filter it away, and
still act on it. Correct, and a genuine surprise — which is exactly why
`Set.Target` goes on the menu's border.

---

## Workstream 3 — glyphs and border shapes (done)

Purely cosmetic, entirely additive, no interaction with the above.

`theme.Theme` gained `Glyphs glyph.Set`, `BorderShapeActive`,
`BorderShapeInactive`, `BorderShapeOverlay`, and `SlotBrackets`. All
zero-valued in the shipped palettes and resolved to library defaults, so
a theme literal written before these existed keeps its chrome.

```yaml
app:
  theme: nord
  glyphs:                 # all 13 optional; unset keeps the library mark
    cursor: ">"
    mark: "*"
    expand_open: "-"
    expand_closed: "+"
    rule: "-"
    scroll_thumb: "#"
    scroll_track: "."
    h_scroll_thumb: "="
    h_scroll_track: "-"
    sort_asc: "^"
    sort_desc: "v"
    column_sep: "|"
    placeholder: "~"
  borders:
    active: rounded       # normal | rounded | thick | double | hidden | block | ascii
    inactive: rounded
    overlay: double
    slot_brackets: corners  # none | corners | tees
```

**Two corrections to the sketch this plan shipped with.** `slot_brackets`
is `none | corners | tees` (`pane.SlotBracketStyle`), not the
`none | square | round` guessed here — `corners` is `┐ text ┌`, `tees` is
`─┤ text ├─`. And `glyph.Set` has 13 fields, not the 8 listed: `rule`,
`scroll_track`, `h_scroll_thumb`, `h_scroll_track` and `placeholder` were
missing.

**`overlay:` does not cover the output console.** It reaches `Confirm()`,
`Alert()`, `Actions()` and — as of v0.25 — `HelpOverlay()`. Those are
what get drawn *above* a screen. The console is a pushed screen and takes
the pane shape. Easy to get backwards, so
`TestOverlayShapeReachesOverlaysOnly` pins the split.

That list gaining a member one release after it was written is the point:
`?` was a footer panel when this workstream landed and is a modal now, so
the enumeration is exactly the kind of prose that rots. v0.25's
`theme.HelpOverlay()` also threads `Glyphs` and `SlotBrackets`, so the
chrome reaches the screen a lost user is most likely to be looking at —
`TestChromeReachesHelpOverlay` covers that.

**Not taken:** v0.25's `app.Options.DisableHelpSearch` (the overlay's
search field, on by default) is a plausible `app.help_search:` knob, but
it is a help feature rather than a chrome one and nothing has asked for
it yet.

### How it landed

- `cfg.Glyphs` / `cfg.Borders` are plain-string structs on `App`, with
  `BorderShapeNames` / `SlotBracketNames` as the canonical enums.
  `internal/config` stays tuilib- and lipgloss-free, so the glyph width
  check is a rune count rather than a display-width one — it catches
  `"=>"`, not a double-width emoji.
- `build.ApplyChrome(themes, &c.App)` does the mapping and is the only
  place that knows a name like `rounded`. `TestBorderNamesAllMap` walks
  `cfg.BorderShapeNames` so the two lists can't drift.
- It applies to **every** palette, not just the one `theme:` names.
  Cycling themes (`t`) walks the whole slice; a palette is a choice of
  color, glyphs and border shapes are a choice of vocabulary, and the
  vocabulary shouldn't change halfway through the cycle.
- Unset fields stay zero rather than being filled in, so "unset" keeps
  one meaning and it lives upstream in tuilib.
- Zero changes in the component builders: every one already goes through
  `th.List()` / `th.Table()` / …, which copy `Glyphs` and `SlotBrackets`
  in. The whole feature is a config block plus one mapping function.
- Both are load errors when wrong, because both fail *quietly*
  otherwise: an unknown border name keeps the default shape (the config
  looks ignored) and an over-long glyph shifts every row it's drawn on
  (looks like a layout bug elsewhere).
- `examples/chrome.yaml` demos an ASCII-safe vocabulary — the case that
  actually comes up, when a terminal or font renders `▸ ✓ █ │` as tofu.

**Cost:** ~150 lines of config + mapping, as estimated.

**Found while verifying this: `t` never worked.** `app.Options.ThemeKey`
was never set, and tuilib treats a zero binding as "cycling disabled" —
so `theme.All()`, `reorderThemes`, and every "press `t`" in the docs
(`examples/themes.yaml` said it before this branch existed) described
something that could not happen. Now bound, with `app.theme_key:`
mirroring `app.output_key:` — default `t`, `-` to pin the palette, and
reserved against action bindings the same way. Which means the
apply-to-every-palette design above is now actually observable, rather
than argued.

---

## Workstream 4 — password prompts (done)

The smallest change here and arguably the highest value per line.

`app.prompts:` collects boot-time values and `os.Setenv`s them so
`${env.*}` picks them up. That is where API tokens are entered today —
`ARGOCD_TOKEN`, `AWX_TOKEN` — **in the clear, on screen**.

```yaml
app:
  prompts:
    - key: ARGOCD_TOKEN
      label: ArgoCD token
      type: password
```

One case in `newPromptForm` and one in `collectAppPrompts`:

```go
case "password":
    fields[i] = form.Password(form.PasswordOptions{
        Key: p.Key, Label: labelOr(p.Label, p.Key),
        Placeholder: p.Placeholder, Required: p.Required,
    })
```

Plus `password` in the `Prompt.Type` validator enum and a docs line.
Applies to action prompts too, for the same reason.

**Cost:** ~20 lines. Ship it first — it's independent of everything else
and closes a real hole.

---

## Suggested implementation order

All done: password prompts (as `mask:`, not `type: password` — see
`Parameter.Mask`), the output console, the actions rebase, actions onto
`pkg/action`, multi-select with fan-out, right-click retargeting, and
glyphs/border shapes.

## Tests

All covered. The ones worth knowing exist: `action.Validate` runs over
every generated Set (`TestGeneratedSetIsValid`); fan-out tags each run
with its own target (`TestFanOutTagsEachRunWithItsOwnTarget`); the
`exclusive:` defaults by kind (`TestExclusiveDefaultsByKind`); marks
survive reorder, filter and theme rebuild; and `overlay:` reaches
overlays only (`TestOverlayShapeReachesOverlaysOnly`).

## Open questions

- **Does `bind:` stay its own field on a push action?** It landed
  unchanged: on a push it fills the destination screen's `parameters:`,
  and on anything else it fills the action's `inputs:`. Two substitution
  targets, one field. It hasn't caused trouble yet. Revisit if the two
  ever want different syntax.

Resolved: http actions default to `Exclusive` (see Workstream 1), and
`Screen.Actions []cfg.Action` was replaced outright rather than kept as
an alias. The only configs were ours in `examples/`, and they were
rewritten.
