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
- [`tuilib-0.24.md`](tuilib-0.24.md) — adopting tuilib v0.21–v0.25:
  the action menu, multi-select marking, masked prompts, and
  glyph/border theming. **All landed**; kept as the record of how, plus
  one open question on `bind:`.
- [`tuilib-0.31.md`](tuilib-0.31.md) — adopting tuilib v0.26–v0.32:
  activity, claims, eventlog, Anchored sources and span tables.
- [`tuilib-0.35.md`](tuilib-0.35.md) — giving every tuilib v0.35
  package and option a verdict: tabs, metrics, input/toggle, stream
  resume, app and component options, and the docs fixes. **All landed**
  (#62–#72); kept as the record of how, with what changed during the
  build and the follow-ups it left. It replaced the old `tabs.md` draft.
- [`http-source.md`](http-source.md) — survey of ~25 real HTTP APIs
  (pagination, auth, rate limits, streaming, formats) and the tiered
  list of `type: http` features that would cover them. The starting
  point for [Map: HTTP source spec](https://github.com/jsdrews/tui-builder/issues/7),
  which rewrites it into a locked spec.
