# Templating

Strings in a config can carry `${…}` tokens that are filled in when
they're used. Which tokens work depends on where the string is and when
it's used. The full list is in
[Reference → Templating](../reference/templating.md).

## The namespaces

| Token | Comes from | Typical use |
|---|---|---|
| `${env.NAME}` | the environment, including boot prompts | tokens, hosts, user choices |
| `${params.NAME}` | values a caller binds to a source | reusable source definitions |
| `${selection…}` | the row a binding or push started from | `bind:`, `confirm:`, a pushed screen |
| `${cursor…}` | the live cursor of an `on_cursor` driver | `on_cursor.bind` |
| `${inputs.NAME}` | an action's inputs, after the form | an action's argv, URL, body, messages |
| `${body.PATH}` | an action's JSON response | `message:`, `error_message:` |

`${selection}` alone is a list's item or a table's first cell;
`${selection.Name}` is the cell in the column titled `Name`, and
`${selection.2}` the second cell. `${cursor…}` works the same way.

## Keep sources reusable

Let sources use only `${params.*}` and `${env.*}`, and map rows onto
parameters in `bind:`:

```yaml
data:
  sources:
    pod:
      type: http
      parameters: {namespace: {required: true}, name: {required: true}}
      url: http://localhost:8001/api/v1/namespaces/${params.namespace}/pods/${params.name}
```

The same `pod` source then serves a push binding, an `on_cursor` detail
pane, a `join` lookup and `wrangl --param`, unchanged.

## Dot-paths

`root:`, a column's `value:` and an inspector field's `path:` name data
with dot-paths: `metadata.name`, `items.0.status`. A missing step gives
an empty value, not an error. Where a field accepts a list of paths, the
first non-empty one wins.

## Expressions

Pipeline operators and an action's `success:` take
[expr-lang](https://expr-lang.org/docs/language-definition) expressions.
An item's fields are in scope directly:

```yaml
where: "status.phase == 'Running' && len(spec.containers) > 1"
compute:
  short_name: "lower(metadata.name)"
by: "status.containerStatuses[0].restartCount"
```

A plain text line is `item` (`item contains 'ERROR'`), and an operator's
own parameters are `params.NAME`.
