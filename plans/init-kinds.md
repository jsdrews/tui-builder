# `tui-builder init <kind>`: an example of any kind, every field shown

`tui-builder init` writes the offline starter. `tui-builder init <kind>`
writes a small config built around one component kind or source type,
which runs as written, with **every field valid for that kind** below it
as a commented-out line. It does two jobs: it starts a config from any
kind, and it lists every option that kind has.

Decided in conversation (no map). Terms follow
[`CONTEXT.md`](../CONTEXT.md). It builds on the scaffolding plan's
[`init`](scaffolding.md).

## 1. The command

- `tui-builder init` with no argument is unchanged.
- `tui-builder init <kind>` takes one positional: a component kind
  (`list`, `table`, `logview`, `textview`, `tree`, `inspector`,
  `eventlog`, `input`, `toggle`, `menu`; `config.ComponentKinds`) or a
  source type
  (`http`, `exec`, `file`, `websocket`, `static`, `merge`). The two sets
  share no names. An unknown kind is a usage error (exit 2) that lists
  the kinds and suggests the closest one (`tabel` → `table`).
- The flags are the same as now: `-o` (default `tui-builder.yaml`),
  `--title`, `--force`, and `--dry-run`, which prints to stdout. So the
  command also gives you a snippet to paste.
- Shell completion offers the kinds.
- **Later, not in this plan:** `--into FILE`, to splice the example into
  an existing config the way `add exec --into` does.

## 2. What it writes

Taking `init table` as the example:

1. **A small config that runs.** `app:`, a source and one screen, with
   the kind in use and only the fields it needs set. It runs offline
   wherever it can (see [§4](#4-kinds-that-need-something-running)).
2. **Every other field, commented out**, under the component or source
   it belongs to, grouped as the reference page groups them: common
   fields first (only those valid on this kind), then the kind's own
   fields. Each group starts with a header comment:
   `# ── Table fields (docs/reference/components.md#table) ──`.
3. **Each field line is the default, followed by the description:**

   ```yaml
       # filterable: false            # Enables the embedded / filter.
       # loading_label: "Loading…"    # Shown while the source fetches.
   ```

   - With a default, the value is the default, so uncommenting the line
     changes nothing until you edit it.
   - With no default (`—`), the value is a placeholder of the field's
     type that loads: `""` / `0` / `[]` / `{}`, or an example taken from
     the docs when a bare zero wouldn't load (a duration is `5s`, an
     enum is its first value).
   - The description is the docs table's "What it does" cell, cut to its
     first sentence and stripped of markdown links. A cell longer than
     one line wraps onto `#` lines under the field.
4. **Nested blocks are written out in full**, each as one commented
   example: `columns:` (one column with every column field), `window:`,
   `paginate:`, `auth:` (one block per scheme), `retry:`, `graphql:`,
   `colors:`, `color_rules:`, `on_cursor:`, `on_input:`, `activity:`,
   `metric:`, inspector
   `fields:`, menu `items:`. Their fields come from their own tables
   (`Column`, `WindowConfig` and so on, as `referencePages` maps them).
5. **Fields that can't be used together** are marked. A field the
   running config would conflict with (`items:` beside `source:`,
   `paginate:` beside `window:`) has a note: `# instead of source:`.

An `http` source with everything shown is about 150–200 lines. That's
the point of the feature, so nothing is collapsed.

## 3. Where the fields come from

- **The docs tables are the field list.** Every row in
  `docs/reference/*.md` already gives the field, its type, default,
  which kinds it's valid on, and a description.
  `internal/config/reference_test.go` already fails when a struct field
  has no row, so the tables are complete against the code.
- **`go generate ./internal/scaffold`** runs a small Go program
  (`internal/scaffold/gendocs/`). It parses those tables into
  `internal/scaffold/fields_gen.go`: for each section (`Common fields`,
  `table`, `Columns`, `http`, `window`, …), the rows as `{Field, Type,
  Default, ValidOn, Desc}`. The binary embeds that file, so `init` needs
  no docs at run time.
- **Which section belongs to which kind** is a small hand-written map in
  `internal/scaffold` (kind → its sections; nested block → its section),
  next to the generated file.
- **"Valid on/with" filters the common fields.** The cell names kinds in
  plain words ("list, logview, eventlog, auto inspector (with
  `path:`)"). The parser takes the kind names it finds; `—` or "any
  kind" means every kind; `required` means the field is in the running
  config already. A cell it can't read fails generation, so nothing is
  silently dropped.
- **The code still checks the field list.** The tests in §5 load what
  `init` writes with the strict loader, so a doc row that claims a
  field is valid when the code rejects it fails the test. The docs are
  the source of the descriptions only; validity comes from the code.

## 4. Kinds that need something running

- **Components** are bound to a `static` source, or hold static
  content (`items:`, `rows:`, `lines:`, `content:`, `root:`), so every
  one runs offline. `input` and `toggle` sit beside a table whose title
  reads `${input.NAME}`, so the value shows. `menu` pushes to a second
  screen. `eventlog` runs on a `static` source with `refresh:` so it
  moves.
- **`static`, `exec`, `file`, `merge`** run offline:
  - `exec` uses `sh -c` and `printf` JSON;
  - `file` reads a JSON file that `init` writes next to the config.
    `--dry-run` prints only the config, and the comment at its top says
    how to create the file;
  - `merge` joins two `static` sources.
- **`http` points at the `thingsd` fixture** (`http://localhost:8765/things`,
  bearer `${env.THINGS_API_TOKEN}`). The header comment says to run
  `go run ./internal/fixture/things/thingsd` and set the token to
  `fixture-token`.
- **`websocket`:** `thingsd` gets a `GET /events` websocket that sends
  a thing's change as JSON every second, using the websocket library
  the runtime already depends on. `init websocket` points at it.

## 5. Tests

- **Every kind loads as written.** For each kind, `init <kind>
  --dry-run` loads with the strict loader.
- **Every field is there.** For each kind, every field the generated
  table says is valid on it appears as a commented line.
- **Each commented line loads on its own.** For each kind and each
  commented field (or nested block), uncomment just that one and load
  it. A field marked `# instead of X:` is uncommented with `X` commented
  out. This test is what ties the docs' claims to the code.
- **The generated file is current.** A test runs the generator into a
  temp file and compares it with `fields_gen.go`, the same way the
  goldens fail when stale.
- **Goldens.** `internal/scaffold/testdata/init/<kind>.yaml`, re-recorded
  with `-update`.
- **e2e.** The `table`, `menu` and `http` examples (the last against
  `things.New()`) run headless through the screen harness and render.

## 6. Docs

- `docs/cli/tui-builder.md`: the `init <kind>` form, the kind list, and
  that the field comments come from the reference.
- `docs/reference/README.md`: one line saying `init <kind>` prints any
  kind with every field.
- `README.md`: one line after "A first config".
- The manual-testing guide (outside the repo) gets an `init <kind>`
  section.

## Implementation order

1. **The field table.** The `gendocs` generator, `fields_gen.go`, the
   kind → section map, and the staleness test. Any doc cell the parser
   can't read is fixed in the docs here.

   **As built.** `internal/scaffold/gendocs` parses every table whose
   header is the field-table header (preset, locator and token tables
   are skipped) into `reference`, a map keyed `page#anchor`; two tables
   under one heading are one section. A row whose Field cell isn't one
   key in backticks fails generation. Reading "Valid on/with" as kinds
   lives in `internal/scaffold/fields.go` (`kindsOf`, `fieldsFor`), not
   in the generator, and only for the three shared sections (component
   common fields, `colors`, leaf-source fields). A test fails on any
   cell there it can't read. Cells inside nested blocks (`window` and
   `auth` on http vs exec vs websocket) aren't read as kinds; PR 3's
   "loads on its own" test catches those. `activity` turned out to be a
   block on a table, not a component kind, so it moved to the block
   list. Two doc cells changed: the component `type` row now names all
   ten kinds (a test checks both `type` rows against the kind lists),
   and `filter_placeholder` lists its kinds instead of "any filterable
   or searchable kind".
2. **`init <kind>` for components.** The positional, completion, the
   usage error, the writer (running config + commented groups + nested
   blocks), and the four tests and goldens for the 10 component kinds.

   **As built.**
   - **The running configs are hand-written**, one per kind, in
     `internal/scaffold/kinds/<kind>.yaml` (embedded). A `# @fields`
     line marks where the kind's fields go; the keys already set beside
     it are left out. `# @fields columns` (or `fields`, `items`) inside a
     list writes one commented item with every field of that list.
     `scaffold.Kind(kind, title)` adds the header and `app.title`.
   - **The `#` stays at the block's indent** on every line, with the
     YAML's own indent after it, so deleting `# ` from any line leaves it
     where it belongs. In a list item, required keys come first, so the
     `- ` is on a key that's always set.
   - **Values.** A docs default that's a literal (`` `false` ``,
     `` `0` (none) ``) is the value. Otherwise it's the type's zero
     (`""`, `0`, `false`, `[]`, `{}`), which loads as unset. A small
     `examples` map gives the values a block's required keys need
     (`title: Title`, `color: red`, `activity.busy: [pending]`).
   - **Descriptions** are the first sentence of "What it does", links
     stripped, then the "Valid on/with" cell in brackets when it limits
     the field (not in the shared sections, where it's the kind list).
     `insteadOf` adds "Instead of source, item." to static fields like a
     list's `items:`, naming only keys the example sets.
   - **The "loads on its own" test** uncomments one entry plus its
     ancestors and their required keys, and loads the result. A short
     `needsMore` table in the test lists the entries that can't load
     alone, with the reason: a metric's kind keys, `mark_key` without
     `markable`, `on_cursor` / `on_input` without a second component,
     `distinct` without a window, a table's `max_items`. The test fails
     if one of those starts loading, so the table can't go stale. It
     already caught the tree's `children:` clashing with `group_by:`
     and `source:` on an input, toggle or menu.
   - **Component keys aren't decoded strictly**, so a misspelt doc row
     would load fine. `TestEveryFieldIsAKey` checks every documented
     component field against its struct's YAML tags.
   - **Docs:** the `source` row's "Valid on/with" is now "not input,
     toggle or menu", and `kindsOf` reads a leading "not" as "every
     kind but these".
   - **CLI:** `init [kind]` takes the 10 component kinds (sources come
     in PR 3), completes them, and refuses an unknown one with exit 2,
     the kind list, and a "did you mean" from `config.Suggest`, newly
     exported.
   - **e2e:** `internal/screen/init_e2e_test.go` runs `init table` and
     `init menu` headless; the menu test opens an item and comes back.
3. **`init <kind>` for sources.** The 6 source types, the `thingsd`
   websocket, and the `http` e2e.

   **As built.**
   - **Templates** for the six types sit beside the component ones.
     Each binds its source to a table. Every template now carries its
     own `app:` (with `title: __TITLE__`), since `http` and `websocket`
     need `app.env`.
   - **`http` and `websocket`** read `${env.THINGS_API_URL}/things` and
     `${env.THINGS_EVENTS_URL}`, with `app.env` defaults
     (`http://localhost:8765`, `ws://localhost:8765/events`,
     `fixture-token`), so they run against `thingsd` unchanged and the
     e2e tests point them at an `httptest` server.
   - **`file`:** `scaffold.Kind` now takes the config's directory and
     returns an `Example{Config, Files}`. `init file` writes
     `services.json` beside the config, and the `path:` names it from
     where tui-builder runs (`-o sub/app.yaml` → `path:
     sub/services.json`). Every file is checked before any is written,
     so a refusal leaves nothing half-done.
   - **The `thingsd` websocket** is `GET /events`: one thing's status as
     `{name, status, at}` at once and then every `things.EventEvery`
     (1s), cycling through the things. It takes the bearer token, changes
     nothing, and isn't in the spec, so the gen goldens don't move.
   - **Blocks inside blocks.** In a block's table, a row whose "Valid
     on/with" starts with a map row's key nests under it: auth's `user`
     (`` `basic`; required ``) sits under `basic:`. `parameters:` shows
     one entry, named `q`. `# @fields auth` inside a set `auth:` shows
     the other schemes, each "Instead of bearer".
   - **`window` and `auth` filter by kind too**, leniently: a cell that
     names no kind keeps the field. `kindsOf` now reads ;-clauses, so
     "required on http; not on exec" is http only. "required" in any
     clause marks a key required. One doc cell changed: `window.filters`
     is "exec, http; a map value: http only".
   - **`needsMore` grew by the stream and window cases:** `resume`
     without `follow`, an exec window whose command doesn't use
     `${window.offset}`, `anchor` without `cursor`, `growing` off an
     eventlog, `page_start` beside `offset_param`. `alsoOn` sets what a
     block can't load without (an http window's `offset_param` /
     `limit_param`, `paginate.page_param`, `find.params` and
     `position`).
   - **`TestEveryFieldIsAKey`** covers the source sections, and fails if
     a section init writes has no struct to check against.
   - **e2e:** `init http` and `init websocket` run against
     `things.New()`; a fixture test covers `/events`.
4. **Docs**, as in §6.

   **As built.** `docs/cli/tui-builder.md` has a `tui-builder init
   <kind>` section (the kinds, what a line shows, blocks, `init file`,
   the fixture behind `init http` / `init websocket`, the usage error,
   and where the lines come from). The usage line takes `[kind]`. The
   reference README links it, and its components row now names `menu`,
   which it had missed. The README and the quick start each have a
   short paragraph after the first config. The manual-testing guide
   (outside the repo) has an `init <kind>` section. The docs site
   builds clean, with the `#tui-builder-init-kind` anchor the links
   use.
