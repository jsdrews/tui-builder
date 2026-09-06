# Plans

Forward-looking design docs for upcoming changes. One file per topic.
Each plan captures:

- What we've decided / want to do
- Why (the constraint or use case driving it)
- What's already in place vs. what's still missing
- Suggested implementation order

When something in a plan actually ships, the description moves to
[`docs/`](../docs/) — the user-facing reference — and the corresponding
section in the plan gets trimmed or deleted.

## Current plans

- [`pipeline-operators.md`](pipeline-operators.md) — pipeline operator
  catalog (`filter`, `project`, `sort`, `derive`, `union`, `compose`,
  `join`), the implicit DAG that already exists, and the missing
  primitives that block richer chained data fetches.
- [`tuilib-0.24.md`](tuilib-0.24.md) — adopting tuilib v0.21–v0.24:
  the action menu (`pkg/action`) over the existing `feature/actions`
  rework, multi-select marking, password prompts, and glyph/border
  theming. Ordered so the small independent wins land first.
- [`tabs.md`](tabs.md) — deferred `type: tabs` container component
  (feature E from the tui-builder integration batch). Schema, focus-
  semantics tradeoffs, scope estimate. Build when a concrete case
  shows up.
