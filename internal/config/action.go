package config

// This file owns the `actions:` schema — the write side of the config.
//
// Actions are deliberately NOT entries in `data.sources:`, even though
// an exec action and an exec source both shell out. Sources are the read
// side: they get polled on a `refresh:` timer, refetched on manual `r`,
// and re-run on screen re-entry. Putting a mutation in that graph means
// something eventually re-fires it, and "re-fetch" would silently mean
// "delete the pod again". The separation is the safety property.
//
// The split between Action and ActionBinding follows the same boundary
// the rest of the config does. An Action is data-layer: what to run,
// what inputs it needs, how to tell success from failure. A binding is
// TUI-layer: which key, which pane's selection feeds it, whether to
// confirm first. wrangl can describe the former and has no business
// knowing the latter.

// Action is one named, addressable unit of work. Actions live in the
// top-level `actions:` map and are referenced by name from a screen's
// binding list:
//
//	actions:
//	  sync_app:
//	    description: Trigger an argocd sync
//	    type: http
//	    method: POST
//	    url: ${env.ARGOCD_URL}/api/v1/applications/${inputs.app}/sync
//	    headers: {Authorization: "Bearer ${env.ARGOCD_TOKEN}"}
//	    inputs:
//	      app: {type: string, required: true}
//
// An action never references ${selection.*}. It declares what it needs
// as typed `inputs:` and refers to them as ${inputs.<name>}; mapping a
// focused row onto those inputs is the binding's job. That's what keeps
// an action reusable across screens — and what makes "this action wants
// data that doesn't exist here" a load-time error rather than an empty
// string substituted into a kubectl argv at 2am.
type Action struct {
	// Type discriminates the kind: "exec" (default) or "http". Mirrors
	// Source.Type — one bag-of-fields struct, each kind reading only
	// the fields it cares about, so adding a kind is a validator case
	// plus a builder rather than a schema migration.
	Type string `yaml:"type,omitempty"`
	// Description is the one-liner shown by wrangl --list-actions.
	Description string `yaml:"description,omitempty"`
	// Inputs declares the typed slots this action needs filled. Values
	// arrive from a binding's `bind:` map, from a generated form field
	// for anything left unbound, or from Default. Referenced in Run /
	// URL / Body / Headers as ${inputs.<name>}.
	Inputs map[string]*Parameter `yaml:"inputs,omitempty"`

	// --- type: exec ---

	// Run is the argv. Required for exec actions, and NOT passed through
	// a shell — use `sh -c "…"` explicitly if you want shell semantics,
	// so the quoting is visible in the config rather than implied.
	Run []string `yaml:"run,omitempty"`

	// ---- push ----

	// Push names a screen in `tui.screens:` to open. It is what the old
	// `on_key:` block used to be, folded in here so there is one
	// registry rather than two mechanisms binding keys to the same
	// components.
	//
	// A push is navigation, not background work, so it maps onto
	// tuilib's Do rather than Run — the same escape hatch an
	// interactive command uses, and for the same reason: there is
	// nothing to stream and nothing to cancel.
	//
	// The binding's `bind:` supplies the DESTINATION SCREEN's declared
	// `parameters:`, not this action's `inputs:`. Both are "fill in the
	// callee's declared interface"; a screen's interface is its
	// parameters and an action's is its inputs.
	Push string `yaml:"push,omitempty"`

	// --- type: http ---

	// Method defaults to POST for http actions. GET is legal but suspect:
	// an action that doesn't change anything probably wants to be a
	// source instead, where it gets caching and polling for free.
	Method string `yaml:"method,omitempty"`
	// URL is the endpoint. Required for http actions.
	URL string `yaml:"url,omitempty"`
	// Headers are sent as-is. ${inputs.*} / ${env.*} substitute.
	Headers map[string]string `yaml:"headers,omitempty"`
	// Body is the request body, sent verbatim after substitution.
	Body string `yaml:"body,omitempty"`
	// Timeout bounds the request. Go duration string; defaults to 30s.
	Timeout string `yaml:"timeout,omitempty"`

	// --- result contract ---

	// Success overrides the per-kind default for "did this work?" —
	// exec: `code == 0`, http: `code < 400`. An expr-lang expression
	// over `code` (exit status / HTTP status) and `output` (stdout /
	// response body). Exists because plenty of tools lie: grep exits 1
	// on no-match, and an API may 409 an already-in-progress sync that
	// you'd rather not see painted red.
	//
	//	success: code == 0 or code == 409
	Success string `yaml:"success,omitempty"`
	// Message is the head line on success — the one that paints the
	// statusbar summary AND heads the console entry. Supports ${inputs.*}
	// and, for http, dot-paths into the parsed JSON body via ${body.*}.
	// Defaults to "action complete".
	Message string `yaml:"message,omitempty"`
	// ErrorMessage is Message's failure counterpart. Same substitution.
	// Defaults to the last line of stderr (exec) or the status code
	// (http).
	//
	// This is worth setting for any JSON API: without it the summary is
	// a status code, and the actual reason sits unread inside a body
	// too long for a footer.
	//
	//	error_message: ${body.message}
	ErrorMessage string `yaml:"error_message,omitempty"`
}

// Kind returns the action's type with the default applied. Callers
// switch on this rather than on Type so the empty-string default lives
// in exactly one place.
func (a *Action) Kind() string {
	if a.Type != "" {
		return a.Type
	}
	// `push:` is unambiguous on its own — there is no other reason to
	// name a screen — so it doesn't need `type: push` spelled out. That
	// keeps a drilldown as short as it was under `on_key:`.
	if a.Push != "" {
		return "push"
	}
	return "exec"
}

// ActionBinding wires a key on a screen to an action. It carries every
// concern that only means something inside a running TUI — the key, the
// pane whose selection feeds the inputs, the confirm modal — while the
// action itself stays a pure description of work.
//
//	actions:
//	  - key: s
//	    action: sync_app
//	    from: apps_table
//	    bind: {app: "${selection.Name}"}
//	    confirm: "Sync ${selection.Name}?"
//
// An action may also be declared inline, for the genuine one-off. Inline
// declarations require an explicit `name:` and are hoisted into the
// top-level registry at load time, so by the time anything downstream
// reads the config there is only one kind of action and one place to
// look one up:
//
//	actions:
//	  - key: n
//	    name: new_namespace
//	    run: [kubectl, create, namespace, demo]
type ActionBinding struct {
	// Key is the trigger. Any tea.KeyMsg.String() name. Reserved keys
	// (see load.go's reservedKeys) are rejected at load — binding one
	// either does nothing at all or silently shadows navigation.
	Key string `yaml:"key"`
	// Action names an entry in the top-level actions: map. Mutually
	// exclusive with an inline declaration.
	Action string `yaml:"action,omitempty"`
	// Name is required when declaring inline, and becomes the hoisted
	// action's registry key. Explicit rather than synthesised from the
	// screen and key, so the name stays stable when the key is rebound
	// and reads honestly in --list-actions.
	Name string `yaml:"name,omitempty"`
	// From names the list / table / tree component whose focused row
	// supplies ${selection.*} to the Bind templates.
	//
	// Required exactly when a Bind template references ${selection.*},
	// and forbidden otherwise — so it's derived from what the binding
	// actually does rather than declared. An action that needs no row
	// omits it, and its key then fires regardless of which pane holds
	// focus.
	From string `yaml:"from,omitempty"`
	// Bind maps the action's input names to templates resolved at fire
	// time. Templates support ${selection.*} (the focused row from
	// From) and ${env.*}. Inputs left unbound are collected in a
	// generated form; inputs that are neither bound nor promptable and
	// have no default are a load error.
	Bind map[string]string `yaml:"bind,omitempty"`
	// Label appears in the help strip. Defaults to the action name.
	Label string `yaml:"label,omitempty"`
	// Confirm, when non-empty, shows a yes/no modal before dispatch.
	// Substituted the same way Bind templates are, plus ${inputs.*} so
	// the message can preview values the form just collected.
	//
	// This is the one blocking interrupt left in the action flow, and
	// it's deliberately a pre-action gate rather than a result report:
	// the moment to stop someone is before the delete, not after.
	Confirm string `yaml:"confirm,omitempty"`
	// Notice is printed once after the TUI suspends and before an
	// interactive subprocess starts. Only meaningful with Interactive.
	Notice string `yaml:"notice,omitempty"`
	// Interactive hands the terminal to the subprocess (vim, ssh,
	// kubectl exec) instead of capturing its output. Defaults to false:
	// the overwhelming majority of actions are non-interactive, and the
	// capturing path is the one that streams into the output console.
	//
	// Meaningless for type: http and rejected by the validator there —
	// there's no TTY to hand over to an HTTP request.
	Interactive *bool `yaml:"interactive,omitempty"`
	// Inline carries an inline action declaration. Empty when Action
	// names a registry entry.
	Inline Action `yaml:",inline"`
}

// IsInteractive reports whether this binding wants a TTY handoff.
// Defaults to false — see the Interactive field.
func (b *ActionBinding) IsInteractive() bool {
	return b.Interactive != nil && *b.Interactive
}

// IsInline reports whether the binding declares its action inline
// rather than referencing one by name.
func (b *ActionBinding) IsInline() bool {
	return b.Action == ""
}
