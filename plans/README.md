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
- [`http-source.md`](http-source.md) — the locked spec for `type: http`:
  presets, `query:`, `json:`/`form:` bodies, locators, error handling,
  pagination strategies, `auth:`, `retry:`, conditional GET and a
  `graphql:` shorthand, each mapping from OpenAPI 3. Decided on
  [Map: HTTP source spec](https://github.com/jsdrews/tui-builder/issues/7);
  seven PRs, **all landed** (#81–#87). The API survey behind it is in
  [`research/http-survey.md`](research/http-survey.md).
- [`generated-config-gaps.md`](generated-config-gaps.md) — the locked
  spec for the tuilib table bugs and tui-builder gaps found running
  generated configs: refused sorts, action groups, env from a file or
  command, a menu component, filter comparisons and rules, sortable
  fields on the source. Decided on
  [Map: tuilib table bugs and tui-builder gaps from generated configs](https://github.com/jsdrews/tui-builder/issues/105);
  six tuilib PRs and a release, then nine tui-builder PRs. **All
  built**, on tuilib v0.43.0; what changed is recorded in the plan.
- [`scaffolding.md`](scaffolding.md) — the locked spec for
  `tui-builder init` and `tui-builder add exec -- <command>`: Cobra
  subcommands, the offline starter, sampling a command and inferring a
  source, columns, ranking and status colours, streams, windows and
  eventlogs, and splicing into an existing config. Decided on
  [Map: Scaffolding — init and exec sources from a command](https://github.com/jsdrews/tui-builder/issues/127);
  eight PRs, all built and uncommitted.
- [`openapi-generator.md`](openapi-generator.md) — the locked spec for
  `tui-builder gen <spec>`: parsing with kin-openapi, the preset,
  servers and auth, operations to screens, tabs or a menu, windows,
  columns, actions, and an overrides file for what the spec can't say,
  tested against the `internal/fixture/things` stub. Decided on
  [Map: OpenAPI → tui-builder config generator](https://github.com/jsdrews/tui-builder/issues/113);
  seven PRs after the scaffolding plan's PR 1; all seven PRs
  built and uncommitted.
