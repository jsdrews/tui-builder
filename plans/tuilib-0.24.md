# Adopting tuilib v0.21 – v0.25

The bump from `v0.20.0` to `v0.25.0` is already on this branch and
compiles green. This plan covers what to do with the five releases'
new surface — none of which is wired into the YAML schema yet.

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

v0.25 needs nothing from us: it compiles clean, `app.HelpVerbose` keeps
its meaning, and the removed `HelpMaxRows` was never set here. It does
change one fact this plan asserted, though — see Workstream 3.

The v0.21–v0.24 four are not independent. Marking (v0.23) is what makes
`action.Action.Multi` mean anything, and the action menu (v0.21) is the
only surface that can tell a user whether "Delete" is about to hit one
row or twelve. Plan them together; ship them in the order at the bottom.

---

## Workstream 0 — the output console (prerequisite)

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

## Workstream 1 — actions onto `pkg/action`

### Where we are

Two implementations exist:

- **`main`** — `Screen.Actions []cfg.Action`: key-bound, tied to one
  `source:` component, exec-only. Flow is prompts (form modal) →
  confirm modal → `dispatch` → alert on error, `app.Info` on success.
  `internal/screen/screen.go` owns all of it.
- **`origin/feature/actions`** (unmerged, one commit, ~2500 lines) — a
  much better shape already: a top-level `actions:` registry of named,
  reusable actions with `type: exec | http`, typed `inputs:` referenced
  as `${inputs.*}`, a separate TUI-layer `ActionBinding` (key, `from:`,
  `bind:`, `confirm:`), `success:` expressions, `message:` /
  `error_message:`, and a data-layer `internal/action` package that
  resolves but does not execute.

That branch was written against v0.20, before `pkg/action` existed. Its
own doc comment already reaches for `runner.CaptureWith` "so output
streams into the console" — it anticipated this release without having
it. Do not re-derive it.

**How to bring it in — unresolved.** The obvious move is to rebase it
onto the bump and land it green first, but a trial cherry-pick turned up
two things that argue against:

- Its parent is `274d539`, **before** the pagination/window work
  (`1499efb`, 3660 lines). So the rebase crosses that schema commit as
  well as the bump — seven files conflict: `README.md`,
  `cmd/tui-builder/main.go`, `docs/components.md`,
  `examples/prompts_boot.yaml`, `internal/config/{config,env,load}.go`
  and `internal/screen/screen.go`.
- Most of the `screen.go` conflict is in the dispatch / confirm / alert
  path that the `pkg/action` step **deletes**. Merging it carefully
  across pagination in order to remove it next step is wasted work.

The cheaper route is probably to cherry-pick only what applies cleanly —
`internal/action/`, `internal/config/action.go`,
`internal/config/action_validate.go`, `cmd/wrangl/actions.go` and the
examples are all new files — and write the screen wiring straight against
`pkg/action`. That keeps the branch's design (the registry, typed
`inputs:`, exec + http, `success:` expressions) and skips a throwaway
merge. Not decided; the schema halves of `config.go` and `load.go` still
have to be merged either way.

### What the shell takes over

`app.Options.ActionsKey` is the single switch for the whole feature —
leave it zero and no menu, no right-click, no key. Set it and the shell
owns the menu overlay, the confirm modal, the goroutine, cancellation,
the console entry, the statusbar badge, the kill picker, and the
per-target exclusivity check. A screen supplies verbs by implementing
one method:

```go
// internal/screen
func (m *Model) Actions() action.Set
```

So `internal/screen` **deletes** its action-owned confirm modal, its
`dispatch`, its `actionOutcome`, and the alert-on-error path. That is
the real win here: less tui-builder code, which is the whole thesis in
AGENTS.md ("tui-builder code is a thin wrapper — trust tuilib for
behavior").

### Mapping

| tui-builder (`ActionBinding` + `Action`) | tuilib `action.Action` |
|---|---|
| `label:` (or action name) | `Label` |
| `description:` | `Desc` |
| `key:` (now optional) | `Key` — live only while the menu is open |
| `confirm:` (substituted) | `Confirm` — shell owns the modal |
| binding's `from:` pane not focused | `Disabled` with a reason |
| exec non-interactive / http | `Run` (an `action.Func`) |
| exec `interactive: true` | `Do` → `runner.Run(cmd)` |
| action can fan out over marked rows | `Multi` |
| http, or any mutating action | `Exclusive` (see open question) |

`Set.Target` comes from `SelectionLabel()` ("cache-redis", or
"3 items"); `Set.Count` from the selection length. `Set.Actions` is the
screen's bindings filtered to those whose `from:` names the focused
component, plus those with no `from:` at all (which fire regardless of
focus — already the branch's semantics).

`internal/action.Resolved` fits `action.Func` almost exactly. Both kinds
collapse to one signature:

```go
Run: func(ctx context.Context, out io.Writer) error {
    // exec: cmd.Stdout, cmd.Stderr = out, out; cmd.Run()
    // http: act.Do(ctx, resolved, out)
}
```

which is what earns both kinds the console, the badge, and cancellation
with no extra code on either side. Today the http path has no way to
report progress at all.

### Keys: the menu replaces them

**Decided: menu only.** One key (`a`) opens a picker that lists every
verb the screen has, with its shortcut in the right-hand column and a
visible reason next to anything unavailable. The screen's own
`tryAction` key-dispatch path is **deleted**.

This is tuilib rule 8's position and the reason `action.Action.Key` is
deliberately not advertised in `Help()`: moving discovery into the menu
is most of the point. A footer holds one row; a screen can easily have
nine verbs.

What changes:

- Config `key:` becomes `action.Action.Key` — a **menu-scoped**
  shortcut, live only while the menu is open. It no longer fires from
  the screen.
- `key:` becomes **optional**. Once the menu exists, a verb without a
  key is perfectly usable, and the letter budget stops being the ceiling
  on how many verbs a screen can have. This is the actual payoff.
- Reserved-key validation relaxes for action bindings. A menu shortcut
  can't shadow a global (`q`, `t`, `?`, `tab`, `esc`, `/`) because the
  menu owns the keyboard while it's open. It must still be unique
  within one screen's set — `action.Validate` catches duplicate
  shortcuts and duplicate identities, both of which otherwise fail far
  from their cause.
- `internal/screen` loses `tryAction` and its call sites in `Update`,
  along with the on_key-before-actions precedence comment that only
  existed to arbitrate the collision.

**Migration:** `key: d` still parses and still means something, but `d`
alone stops firing — it's `a` then `d` now. That's a real behavior
change for every existing config in `examples/`. They're ours, so it's
a rewrite rather than a compatibility problem, but the docs need a line
saying so.

**Open:** `on_key:` screen pushes are a separate mechanism and stay on
direct keys. `enter` to drill down is a navigation gesture, not a verb,
and should not move. But a `d → describe` push arguably *is* a verb, and
tuilib's `Do` exists precisely for "push a child screen". Worth deciding
whether pushes join the menu later; not part of this cut.

### Prompts need a seam the shell doesn't give us

An action with unbound `inputs:` must ask for them between the pick and
the run. There's nowhere to put that: `app.Update` matches
`action.ChosenMsg` at app.go:747 and returns, while the line forwarding
to our screen is app.go:1014. Pick and run happen in one `Update`, and
we're never in that path — the action has already started before
anything of ours could react.

**Fix: those actions use `Do`, not `Run`.** `Do func() tea.Cmd` hands
control back instead of running anything, so its cmd emits one of our
messages, falls through to 1014, and the screen opens its form. On
submit we substitute and call `runner.GoWith` ourselves — the shell
intercepts capture messages globally (app.go:899/916/928), so the
console entry, badge and kill picker still work.

Confirm stays screen-side for these; the shell's fires before the form
exists. That's anticipated, not a workaround — its `ConfirmedMsg`
handler is guarded on `m.confUp` *"so a screen hosting its own confirm
modal keeps receiving its own results."*

Fully-bound actions stay on `Run` and use the shell's path. Common case.

### Right-click

`action.RetargetMsg` reports a right-press outside the open menu — "ask
me about this one instead". The host moves its own selection to the
event and reopens. tui-builder already hit-tests mouse events per
component (`Mouse: app.MouseClick`), so this is a small addition:
route the forwarded event through the existing focus/hit-test path,
then reopen. Worth doing — it's the gesture that makes the menu feel
native — but it can trail the first cut.

---

## Workstream 2 — multi-select

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

---

## Workstream 4 — password prompts

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

1. **Password prompts.** Independent, tiny, closes a real hole.
2. **Output console** (`OutputKey`). Prerequisite for everything in 3–4.
3. **Rebase `origin/feature/actions`** onto this branch. Land it as-is,
   green, before layering anything new on it.
4. **Actions onto `pkg/action`.** `Actions() action.Set` + `ActionsKey`;
   move exec and http onto `Run`, interactive onto `Do`; delete
   `tryAction` and the screen's confirm/dispatch/alert path; rewrite the
   `examples/` configs off direct keys. Prompts via `Do` + form +
   `runner.GoWith`.
5. **Multi-select.** `markable:` + `key:`, keyed setters, the key→row
   map, `Multi` fan-out, validator constraints.
6. **Right-click retargeting.** Small, follows naturally from 4.
7. ~~**Glyphs and border shapes.**~~ **Done** — independent of 3-6, so
   it landed while actions are parked.

Steps 1–2 and 7 are worth doing regardless of whether 3–6 ever ship;
all three have landed. **Actions (3, 4, 6) are parked** — 5 is landable
without them, but its fan-out half and most of its payoff aren't.

## Tests

- **Password:** `type: password` produces a masked field; the submitted
  value is the real text, not the mask; validator rejects unknown types.
- **Actions:** `Actions()` returns the right `Set` for the focused pane
  (and disables bindings whose `from:` isn't focused); `action.Validate`
  runs clean over every set a config can produce — it exists to be
  called from a test, and catches duplicate shortcuts and duplicate
  identities, both of which fail far from their cause; exec and http
  both stream into the console; interactive still suspends the
  alt-screen; a bare `d` press no longer fires the action bound to `d`
  (menu-only), and does reach the focused component instead.
- **Marking:** marks survive a poll that reorders rows; marks survive a
  filter; marks survive a theme rebuild; `markable:` + `window:` is a
  load error; a non-`Multi` action is disabled under a multi-selection.
- **Fan-out:** N marked rows produce N runs with distinct `RunKey`s.
- ~~**Glyphs/borders:**~~ **Done.** A partial `glyphs:` block resolves the
  rest from defaults; unknown border name is a load error. Plus: the
  overrides land on every palette (not just the initial one); every one
  of the 13 glyph fields is both validated and copied; `ApplyChrome`
  doesn't mutate its input; an empty block leaves the shapes zero so
  tuilib's defaults still apply; and `overlay:` reaches confirm / alert /
  the action menu but not ordinary components.

## Open questions

- **Should http actions default to `Exclusive`?** Firing the same POST
  at the same target twice is usually a mistake, and the menu renders
  the refusal with a reason rather than dropping the press. But it's a
  behavior change for anything already relying on repeat-fire. Leaning
  yes for `type: http`, no for `type: exec`.
- **Do we keep `Screen.Actions []cfg.Action` (main's shape) as a
  deprecated alias?** The feature branch replaces it outright. Existing
  configs in `examples/` would need rewriting either way — but they're
  ours, so this is only a question if anyone else's configs exist.
