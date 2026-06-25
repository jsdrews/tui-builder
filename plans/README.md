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
