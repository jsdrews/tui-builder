# tui-builder

A YAML config describes a data layer (sources that produce JSON) and a
presentation layer (a terminal UI that displays that data). `wrangl` runs
the data layer alone.

## Language

### Data layer

**Source**:
Any named entry under `data.sources` that produces JSON. Every source is either a leaf or a pipeline.
_Avoid_: data source, endpoint, feed

**Leaf**:
A source that gets its data from outside the config (`http`, `exec`, `file`, `websocket`, `static`, `merge`).
_Avoid_: leaf source, raw source

**Pipeline**:
A source whose `type:` is an operator (`passthrough`, `filter`, `project`, `derive`, `sort`, `union`, `compose`, `join`, `cache`) that transforms one or more upstream sources.
_Avoid_: operator entry, transform

**Operator**:
The transformation a pipeline applies, named by its `type:`.

**Pipe**:
An inline `pipe:` chain of operators on a source, which expands into hidden pipelines.
_Avoid_: pipeline (for the inline chain)

**Cadence**:
How a source emits values over time: streamed, polled, or one-shot. It is inferred from the config and never written.

**Stream**:
A source whose cadence is streamed: it pushes values as they happen rather than being fetched.
_Avoid_: feed, follow (for the source itself)

**Parameter**:
A typed slot a source or pipeline declares under `parameters:`, which a caller fills with an argument.
_Avoid_: param (in prose), input (that's an action's)

**Argument**:
A value a caller passes for one of a source's declared parameters, from wrangl `--param`, a push site, or code.
_Avoid_: binding, bound value

**Readiness**:
Whether a source can run as-is (self-contained) or needs arguments first (needs params). It is inferred from the config and never written.

**Preset**:
A named, partial leaf under `data.presets` that sources `extends:` to share settings, including declared parameters. It is not a source and produces nothing. Only a preset can be extended, and a preset is never a pipeline.
_Avoid_: connection, api, client, template, base

**Window**:
A source read a slice at a time, because its whole set is too big to hold. A window is either seekable or anchored.
_Avoid_: page (for the source), paginated source (that's one that walks every page up front)

**Seekable**:
A window whose item N can be fetched directly, with a total.
_Avoid_: offset-paged

**Anchored**:
A window reachable only by walking from an edge item to the items before or after it, with no offsets and no total.
_Avoid_: cursor-paged, infinite

**Cursor**:
The position token of an item in an anchored window: the value the next request walks from.
_Avoid_: using Cursor for the focused row (see Focused row)

**Locator**:
Where a response-level value is read from: a dot-path into the body, a header, or an entry in the `Link` header.
_Avoid_: path (for headers and links), selector

**Patch**:
A partial request merged into a source's query string or body for one request, by a window edge or a paginate walk.
_Avoid_: override, overlay

**Token**:
A `${namespace.key}` placeholder in a templated field, resolved from its namespace (`params`, `env`, `window`, …) at a set moment.
_Avoid_: variable, placeholder (in prose), template (that's the whole string)

### Presentation layer

**Component**:
One pane on a screen. It displays a source named by its `source:`, or holds a value of its own (an input field).
_Avoid_: widget, view

**Input field**:
A component that holds a value rather than displaying a source: a text input (`type: input`) or a toggle (`type: toggle`). Other components read its committed value as `${input.NAME}`.
_Avoid_: control, widget

**Focused row**:
The row, item or node under a component's cursor highlight. The `${cursor.*}` token and `on_cursor:` read a driver's focused row; those names predate this glossary.
_Avoid_: cursor (see Cursor)

**Action**:
A named unit of work under `actions:`: a command, a request, or a push to another screen.
_Avoid_: verb (except in the action menu), command

**Input**:
A typed slot an action declares under `inputs:`, filled by its binding or a form.
_Avoid_: parameter (that's a source's). Not to be confused with an input field, which is a component.

**Binding**:
A screen's entry tying an action to a key and to a component's focused or marked rows, and filling the action's inputs from them.
_Avoid_: using Binding for readiness, or for a component displaying its source
