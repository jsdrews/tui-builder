# Pipeline operators & the data-layer DAG

**Status**: planning, not started. Source of truth for "what does the
data layer look like once pipelines are more than passthrough."

This doc captures what we've designed but not yet implemented. The
reference for what's *actually built* is [`docs/data-layer.md`](../docs/data-layer.md).
When something here ships, move the description there and trim or
delete the corresponding section below.

---

## The DAG today (implicit)

Every config already builds a directed graph at load time:

```
            ┌─ pods_prod ─┐
            │             │
            ├─ pods_stage ┼─► pods_all (merge) ─► all_pods (component / wrangl)
            │             │
            └─ pods_dev ──┘
```

- **Leaves** are sources (`http`, `exec`, `file`, `websocket`).
- **Inner nodes** are composers (`merge` today) and pipelines.
- **Sinks** are TUI components or wrangl stdout.
- **Edges** are `from:` references, merge `sources:`/`children:`, and
  component `source:`/`pipeline:` bindings.

`datasource.Build` topologically resolves the source graph with cycle
rejection. `pipeline.Build` layers pipelines on top. The infrastructure
for "a DAG of fetches" is real; what's limited is the **vocabulary of
nodes** you can use to express dependencies.

---

## Operator catalog

Organized by arity (number of upstream inputs). Every operator must
satisfy the `ds.Source` interface so TUI components and wrangl
consume them through the existing contract.

### Identity — 1 in, 1 out

| Operator | In | Out | Status |
|---|---|---|---|
| **passthrough** | any source / pipeline | unchanged | ✅ shipped |

```yaml
data:
  sources:
    pods:
      type: passthrough
      from: pods_raw
```

### Transforms — 1 in, 1 out (different shape)

| Operator | In | Out | Lifecycle behavior | Status |
|---|---|---|---|---|
| **filter** | iterable | subset of input items | preserves (per-item check) | ✅ shipped |
| **project** | iterable of objects | iterable of slimmer objects | preserves | ✅ shipped |
| **derive** | iterable of objects | iterable with extra fields | preserves | ✅ shipped |
| **sort** | iterable | reordered | snapshot-only; streaming returns ErrNotStreaming until windowing lands | ✅ shipped |

### Composers — N in, 1 out

| Operator | In | Out | Status |
|---|---|---|---|
| **merge** (source) | N child sources (homogeneous) | flat union, per-row tags | ✅ shipped — legacy form |
| **union** (operator) | N children (sources OR pipelines, homogeneous) | flat union, per-row tags | ✅ shipped — preferred for new configs; reuses NewMerge under the hood; key win = pipelines as children |
| **compose** | N children (heterogeneous) | object with named children | ✅ shipped — `parts:` map of output_key → child, parallel fan-out, snapshot-only |

### Joins — driver + lookups, 1 enriched out

| Operator | In | Out | Status |
|---|---|---|---|
| **join** | driver iterable + N lookup sources | driver items enriched with lookup results | ✅ shipped — snapshot only; lookups must be parameterized sources; emit separate (default) / merged; on_error fail / skip; no caching yet |

```yaml
data:
  sources:
    pods_with_detail:
      type: join
      driver:
        from: pods                 # one fetch returns N pods
      lookups:
        detail:
          from: pod_detail         # on-demand source, needs namespace + name
          on:
            namespace: ${driver.metadata.namespace}
            name:      ${driver.metadata.name}
      emit: merged                 # or keep-separate: {row, detail}
```

Architecturally the most interesting operator — it's the **kubectl
describe on tree highlight** pattern expressed at the *data layer*
instead of as a TUI screen drilldown. Once it exists, TUI and wrangl
both get it for free.

---

## Missing primitives

What we have today expresses every dependency as either "source needed
before fetch" (resolved at Build) or "screen pushed with params"
(resolved at runtime per UI event). Five things are missing to make
true cross-source chained fetches first-class:

1. **Pipeline-level params flowing through operators.** Today
   `${params.X}` lives on a source. A pipeline wrapping a parameterized
   source can't refine, rename, or compose those params. Joins need
   this — driver params thread into the operator; lookup params get
   computed from each driver row.

2. **Computed expressions.** Most operators (`filter where:`, `derive
   compute:`, `join on:`) need an expression language. Today we have
   only `${params.X}` / `${selection.X}` / `${env.X}` text substitution
   — fine for URL templates, not enough for `status.phase ==
   'Running'`. Smallest viable language: comparisons (`==`, `!=`, `<`,
   `>`, `<=`, `>=`), boolean (`&&`, `||`, `!`), dot-path access on
   item, a few built-ins (`now()`, `parseTime()`, `len()`). Could be
   built on an existing Go expression library (`expr-lang/expr`,
   `Knetic/govaluate`) to avoid hand-writing one.

3. **Materialization & caching.** When two pipelines share an upstream
   source, both should observe the same fetch result per tick, not
   re-fetch. The source registry partially does this (same `*Source`
   pointer), but per-fetch caching across operators isn't formalized.

4. **Per-item triggered fetches.** Joins specifically need this: for
   each driver row, call lookup with row-bound params. Today the
   source model is "one fetch per source per refresh tick" —
   driver-then-lookup-per-row is a new execution shape. Probably
   implemented via a parameterized source being invoked N times per
   tick with different bound params, with a small LRU keyed on the
   params tuple to avoid re-fetching when driver rows persist.

5. **Streaming join semantics.** When driver streams and lookup is
   on-demand, what does "join" mean? Likely: cache the latest driver
   snapshot, run lookups lazily as driver rows are accessed (sink
   pulls). Needs designing before building. Sort-on-stream has the
   same flavor.

---

## Suggested implementation order

1. ~~**Expression language**~~ ✅ shipped — `internal/expr` wraps
   `expr-lang/expr` with a small `Compile` / `Eval` / `EvalBool`
   surface plus a few built-ins (`now`, `parseTime`, `lower`,
   `upper`). The wrapper boundary lets us swap evaluators later
   without touching every operator that compiles expressions.
2. ~~**`filter` operator**~~ ✅ shipped — see `docs/data-layer.md`.
3. ~~**`project` and `derive`**~~ ✅ shipped — see `docs/data-layer.md`.
4. ~~**`sort`**~~ ✅ shipped — snapshot-only with `ErrNotStreaming`
   for streaming upstreams. Windowing (sort the last N events / sort
   within a tumbling window) deferred until a use case emerges.
5. ~~**`union` operator (as alternative to `merge` source)**~~ ✅ shipped —
   reuses the merge composer code (NewMerge exported from
   internal/datasource); the new capability is that children can be
   pipelines, not just leaf sources. `merge` source stays working
   for backwards compat. Multi-input cycle detection + Build
   resolution generalized via `upstreamsOf` to support future
   multi-input operators (compose, join).
6. ~~**`compose`**~~ ✅ shipped — bundles N heterogeneous upstreams
   into a single object keyed by caller-chosen output names. Custom
   `composeSource` does parallel fan-out + assembly; snapshot-only
   (subscribe returns ErrNotStreaming, same pattern as sort).
7. ~~**Pipeline params + plumbing**~~ ✅ shipped — pipelines declare
   their own `parameters:` block (same shape as source params).
   Bound values are visible in operator expressions as `params.X`.
   wrangl `--param` routes to pipeline when the target declares
   parameters; falls through to source binding otherwise. Required
   params unbound at Build → clean error.
   **Not done**: auto-forwarding of pipeline params into upstream
   source params (explicit forwarding via a `bind:` block on the
   pipeline). That's a sub-feature for when join lands.
8. ~~**`join` operator**~~ ✅ shipped — driver iterable + N parameterized
   lookups. For each driver row, expressions evaluate against the row
   to compute lookup params; `cfg.Source.Clone` + `BindParams`
   produces a per-row lookup source; lookups fan out in parallel
   within a row. emit: separate (default) | merged; on_error: fail
   (default) | skip. Snapshot-only (Subscribe → ErrNotStreaming);
   no caching (every row triggers fresh fetches). Lookups must be
   leaf sources with `parameters:` declared — pipelines-as-lookups +
   per-row LRU caching + streaming-with-windowing are the next-level
   enhancements when use cases materialize.
9. ~~**Materialization / caching**~~ ✅ shipped — `cache:` operator
   wraps an upstream with TTL-bounded Fetch memoisation. Reads
   within the TTL return the cached snapshot; the first read after
   expiry re-fetches. Errors aren't cached (retry on next call).
   Streaming passes through. Motivating case (shared upstream in
   union/compose fan-out) exercised in tests.
   **Not done**: single-flight (concurrent waiters may briefly race
   to refresh together) and per-Fetch-call dedup via ctx (cache
   without TTL, valid for one user-initiated Fetch). Add when use
   cases materialize.
10. **Streaming-aware operators**. Sort-with-window, join-with-driver-stream.
    Probably need real motivation before designing.

Joins (8) are where the architecture is forced to stretch — worth a
separate plan doc once we get there.
