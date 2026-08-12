# Tabs (`type: tabs`)

Wire `tuilib.pkg/tab` into tui-builder as a container component that
references other components as tabs. Feature E from the original A-I
integration batch — deferred so we can pick it up when we have a
concrete use case rather than building it speculatively.

## Why deferred

- No pressing use case shipped through the door yet. `on_cursor:`
  covers the "table + detail" pattern that would otherwise reach for
  tabs.
- The interesting design question is focus semantics (see below).
  Getting it wrong before we have a real config pushing on it burns
  churn.

## What tuilib gives us

- `pkg/tab.Model` — pre-existed before the integration batch. Handles
  strip rendering + active-tab index + navigation keys internally.
- v0.16.0 added `StripPos` (`StripTop` default, `StripBottom` option).

## YAML surface

```yaml
tui:
  components:
    resource_tabs:
      type: tabs
      strip_pos: top             # top | bottom (default: top)
      tabs:
        - {label: Pods,     component: pods_table}
        - {label: Services, component: services_table}
        - {label: Ingress,  component: ingress_table}
```

## Schema additions

```go
// On cfg.Component
Tabs     []Tab  `yaml:"tabs,omitempty"`
StripPos string `yaml:"strip_pos,omitempty"` // "top" | "bottom"

// New
type Tab struct {
    Label     string `yaml:"label"`
    Component string `yaml:"component"`
}
```

## Validator rules

- `type: tabs` requires `tabs:` to have ≥1 entry.
- Each `component:` must be a defined component in the same screen's
  layout (walk both directions — the child appearing under a tabs
  container shouldn't ALSO be placed elsewhere in the layout tree).
- `strip_pos:` in `{"", "top", "bottom"}`.
- Tabs container has no `source:` (it's a layout container, not
  data-bound).

## Build wiring

- New `KTabs` kind.
- New `Textview *tab.Model` on `build.Component`.
- `buildTabs` constructs `tab.Options` with StripPos + children
  references.
- Layout: `layout.Sized(c.Tabs)`.
- Rebuild path preserves the active-tab index.

## Focus semantics (the interesting question)

Two viable models:

**Option 1 — all children participate in `tree.All()`**. Focus cycling
walks every tab child regardless of visibility. Polling still fires
per bound source. Simplest to implement; harmless for cheap
components; awkward UX when tab-cycle lands focus on an invisible
component.

**Option 2 — only the active tab's child participates**. Requires a
hook into tuilib's active-tab index and dynamic `tree.All()`
membership. Better UX; more work.

Ship Option 1 first, iterate to Option 2 if the UX chafes.

## Source polling behavior

Should invisible tabs poll their bound sources?

- **Yes** — simple, no state to lose, cursor-driven bindings just
  work when tab flips.
- **No** — cheaper, but requires source-lifecycle hooks that don't
  exist today.

Ship "yes." Revisit if a real config hits a cost wall.

## Tests

- Validator: tabs requires entries; each entry's component exists;
  strip_pos in enum; no `source:` on tabs.
- Build: `buildTabs` wires labels + children; Rebuild preserves
  active-tab index across theme swap.
- Runtime: tab-switch keystroke dispatches to the newly active child;
  invisible children still receive source updates.

## Example

`examples/tabs.yaml` — kubectl-shape: three tabs (Pods / Services /
Ingress) each with their own table backed by a namespace-scoped
source. Left tab active by default.

## Scope estimate

~80–100 LOC + 4–5 tests + example + docs. Roughly half a day.

## When to build

- User surfaces a real case that needs multiple parallel views of the
  same context (kubectl `Pods | Services | Ingress`, cluster `Overview
  | Nodes | Events`, monitoring `Latency | Errors | Traffic`).
- Or when we need it as scaffolding for a demo config we want to
  ship.

Until then, `hstack`/`vstack` covers the "multiple visible panes"
pattern well enough.
