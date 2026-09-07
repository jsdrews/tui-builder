package screen

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

	"github.com/jsdrews/tui-builder/internal/build"
	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// TestKubeMultiEndToEnd builds the kube_multi pattern in a test harness:
// two stub kube-like servers, a merge source unioning them, a table
// bound to the merge. Pumps init through the app shell and inspects
// the rendered View() for the expected pod names + cluster tags.
//
// Reproduces the user's "I see nothing" report under conditions we can
// control (no real cluster needed). Fails fast if the data path is
// broken.
func TestKubeMultiEndToEnd(t *testing.T) {
	prod := stubKube(map[string]string{
		"prod-api-1": "Running",
		"prod-api-2": "Running",
		"prod-web":   "Pending",
	})
	defer prod.Close()
	staging := stubKube(map[string]string{
		"staging-api": "Running",
	})
	defer staging.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"pods_prod": cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", URL: prod.URL + "/api/v1/pods"}),
		"pods_staging": cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", URL: staging.URL + "/api/v1/pods"}),
		"pods_all": cfg.NewEntry(&cfg.Source{Type: "merge", Sources: []string{"pods_prod", "pods_staging"},
			TagField: "cluster",
			OnError:  "skip"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"all_pods": {
			Type:   "table",
			Title:  "All pods",
			Source: "pods_all",
			Columns: []cfg.Column{
				// merge tags land under `_meta` by default, so the
				// cluster column reads `_meta.cluster`.
				{Title: "Cluster", Width: 14, Value: cfg.Path{"_meta.cluster"}},
				{Title: "Namespace", Width: 18, Value: cfg.Path{"metadata.namespace"}},
				{Title: "Name", Width: 30, Value: cfg.Path{"metadata.name"}},
				{Title: "Status", Width: 12, Value: cfg.Path{"status.phase"}},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "All",
			Layout: cfg.Node{Component: "all_pods"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// Pump init + cascading cmds until the queue settles (bounded so
	// runaway loops don't hang the test).
	queue := []tea.Cmd{m.Init()}
	for steps := 0; len(queue) > 0 && steps < 200; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range bm {
				queue = append(queue, sub)
			}
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}

	view := m.View()
	for _, want := range []string{
		"prod-api-1", "prod-api-2", "prod-web", "staging-api",
		"pods_prod", "pods_staging",
		"Running", "Pending",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("rendered view missing %q\n--- view ---\n%s", want, view)
			return
		}
	}
}

// TestKubeMultiAllClustersDown is the "I started the demo without any
// proxies running" case. All three http sources fail; merge returns
// the all-failed error; the screen should pop an alert modal so the
// empty table isn't silent and confusing.
func TestKubeMultiAllClustersDown(t *testing.T) {
	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{

		// Three URLs that will refuse connection — a port range we
		// know nothing's listening on. The merge fails all-3 → modal.
		"pods_prod":    cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", Timeout: "1s", URL: "http://127.0.0.1:1/api/v1/pods"}),
		"pods_staging": cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", Timeout: "1s", URL: "http://127.0.0.1:2/api/v1/pods"}),
		"pods_dev":     cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", Timeout: "1s", URL: "http://127.0.0.1:3/api/v1/pods"}),
		"pods_all": cfg.NewEntry(&cfg.Source{Type: "merge", Sources: []string{"pods_prod", "pods_staging", "pods_dev"},
			OnError: "skip"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"all_pods": {
			Type:   "table",
			Title:  "All pods",
			Source: "pods_all",
			Columns: []cfg.Column{
				{Title: "Name", Width: 30, Value: cfg.Path{"metadata.name"}},
			},
		},
	},
		Screen: cfg.Screen{
			Title:  "All",
			Layout: cfg.Node{Component: "all_pods"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.TUI.Screen, c.TUI.Components, c.Data.Sources, c.Actions, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
		// The failure sink under test. Without this the whole console
		// feature is off and app.ErrorDetail's Body has nowhere to go.
		OutputKey: key.NewBinding(key.WithKeys("o")),
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	pump := func(seed tea.Cmd) {
		queue := []tea.Cmd{seed}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range bm {
					queue = append(queue, sub)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	pump(m.Init())

	// Nothing blocks: the failed first fetch leaves an empty table and a
	// one-line summary, not a modal. The full error is recoverable from
	// the console, which is what the statusbar's unread badge advertises.
	if v := m.View(); strings.Contains(v, "[ OK ]") {
		t.Errorf("expected no blocking modal after an all-clusters-down fetch\n--- view ---\n%s", v)
	}

	// Open the console — the error should be there in full, summary line
	// plus the merge detail that never fits in a footer.
	var next tea.Cmd
	m, next = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	pump(next)

	view := m.View()
	if !strings.Contains(view, "pods_all: initial fetch failed") {
		t.Errorf("expected the console to name the all-clusters-down failure\n--- view ---\n%s", view)
	}
	if !strings.Contains(view, "merge") {
		t.Errorf("expected the merge error detail in the console body\n--- view ---\n%s", view)
	}
}

// TestParamsBindEndToEnd exercises the full push-site bind → destination
// source params flow: a parent table whose selected row's columns feed
// into the child screen's parameterized HTTP source via the `bind:`
// block. The stub server inspects the requested URL and verifies the
// path was templated correctly — so this fails fast if push-time
// binding drops a value, substitutes the wrong cell, or routes around
// the parameters resolution.
//
// Schema this exercises (the kube.yaml drilldown pattern, abstracted):
//
//	users         → list of users
//	    on_key    → push posts with bind: {user_id: ${selection.ID}}
//	posts        → parameterized source /users/${params.user_id}/posts
//
// If this passes, kube.yaml's namespaces → pods → pod_detail chain
// works mechanically the same way; if it fails, the same chain fails.
func TestParamsBindEndToEnd(t *testing.T) {
	var requestedURLs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURLs = append(requestedURLs, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/users":
			fmt.Fprint(w, `[{"ID":"7","Name":"Ada"},{"ID":"11","Name":"Grace"}]`)
		case strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/posts"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/posts")
			fmt.Fprintf(w, `[{"title":"post for user %s alpha"},{"title":"post for user %s beta"}]`, id, id)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"users": cfg.NewEntry(&cfg.Source{Type: "http", URL: srv.URL + "/users"}),
		// Parameterized source — refuses to run without `user_id`
		// bound. The TUI must supply it via the push site below.
		"posts_by_user": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"user_id": {Type: "string", Required: true},
		}, URL: srv.URL + "/users/${params.user_id}/posts"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"users_table": {
			Type:   "table",
			Title:  "Users",
			Source: "users",
			Columns: []cfg.Column{
				{Title: "ID", Width: 8, Value: cfg.Path{"ID"}},
				{Title: "Name", Width: 16, Value: cfg.Path{"Name"}},
			},
		},
		"posts_table": {
			Type:   "table",
			Title:  "Posts",
			Source: "posts_by_user",
			Columns: []cfg.Column{
				{Title: "Title", Width: 40, Value: cfg.Path{"title"}},
			},
		},
	},
		Screens: map[string]*cfg.Screen{
			"users": {
				Title:  "Users",
				Layout: cfg.Node{Component: "users_table"},
				Actions: []cfg.ActionBinding{
					{
						From:   "users_table",
						Action: "open_posts",
						Key:    "enter",
						// The whole point of this test: ${selection.ID}
						// must resolve to the focused row's ID cell and
						// land in the destination source's user_id param.
						Bind: map[string]string{"user_id": "${selection.ID}"},
					},
				},
			},
			"posts": {
				Title:  "Posts",
				Layout: cfg.Node{Component: "posts_table"},
			},
		},
		Initial: "users"},
		Actions: pushRegistry(),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	multi := &Multi{
		Screens:    c.TUI.Screens,
		Components: c.TUI.Components, Sources: c.Data.Sources,
		Actions: c.Actions,
	}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain := func(start tea.Cmd) {
		queue := []tea.Cmd{start}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range bm {
					queue = append(queue, sub)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	drain(m.Init())

	// First screen rendered with both users + the right requested URL.
	view := m.View()
	for _, want := range []string{"Ada", "Grace"} {
		if !strings.Contains(view, want) {
			t.Fatalf("users screen missing %q\n%s", want, view)
		}
	}
	if !sliceHas(requestedURLs, "/users") {
		t.Fatalf("expected /users to be fetched; got %v", requestedURLs)
	}

	// Press enter on the users table — should push to posts with the
	// bind: block resolved against the focused row (Ada, ID=7).
	var pushCmd tea.Cmd
	m, pushCmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(pushCmd)

	// The destination's source should have been fetched with the
	// substituted URL: /users/7/posts (NOT /users/${params.user_id}/posts).
	if !sliceHas(requestedURLs, "/users/7/posts") {
		t.Fatalf("expected /users/7/posts to be fetched after push; requested URLs: %v", requestedURLs)
	}
	view = m.View()
	for _, want := range []string{"post for user 7 alpha", "post for user 7 beta"} {
		if !strings.Contains(view, want) {
			t.Errorf("posts screen missing %q\n%s", want, view)
		}
	}
}

// TestParamsBindFromListSelection mirrors the kube.yaml namespaces→pods
// case: a LIST (not table) parent, bare ${selection} (not ${selection.Col})
// piped into a child source's parameter. If the list-selection path
// drops the value, this fails — and matches the exact shape the user
// reported "enter does nothing" on.
func TestParamsBindFromListSelection(t *testing.T) {
	var requestedURLs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURLs = append(requestedURLs, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/namespaces":
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"default"}},{"metadata":{"name":"kube-system"}}]}`)
		case strings.HasPrefix(r.URL.Path, "/namespaces/") && strings.HasSuffix(r.URL.Path, "/pods"):
			ns := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/namespaces/"), "/pods")
			fmt.Fprintf(w, `{"items":[{"metadata":{"name":"pod-a-in-%s"}},{"metadata":{"name":"pod-b-in-%s"}}]}`, ns, ns)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"namespaces": cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", URL: srv.URL + "/namespaces"}),
		"pods": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"namespace": {Type: "string", Required: true},
		}, Root: "items", URL: srv.URL + "/namespaces/${params.namespace}/pods"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"namespaces_list": {
			Type:       "list",
			Source:     "namespaces",
			Item:       "metadata.name",
			Filterable: true, // mirror kube.yaml — see if this gates enter
		},
		"pods_table": {
			Type:   "table",
			Source: "pods",
			Columns: []cfg.Column{
				{Title: "Name", Width: 30, Value: cfg.Path{"metadata.name"}},
			},
		},
	},
		Screens: map[string]*cfg.Screen{
			"namespaces": {
				Layout: cfg.Node{Component: "namespaces_list"},
				Actions: []cfg.ActionBinding{
					{
						From:   "namespaces_list",
						Action: "open_pods",
						Key:    "enter",
						// Bare ${selection} — the list's selected
						// item drives the bind. Same shape as
						// kube.yaml uses.
						Bind: map[string]string{"namespace": "${selection}"},
					},
				},
			},
			"pods": {
				Layout: cfg.Node{Component: "pods_table"},
			},
		},
		Initial: "namespaces"},
		Actions: pushRegistry(),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	multi := &Multi{
		Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources,
		Actions: c.Actions,
	}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain := func(start tea.Cmd) {
		queue := []tea.Cmd{start}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range bm {
					queue = append(queue, sub)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	drain(m.Init())

	view := m.View()
	if !strings.Contains(view, "default") {
		t.Fatalf("namespaces list missing 'default'; view:\n%s", view)
	}

	// Press enter on the namespaces list — should push pods screen
	// with namespace=default.
	var pushCmd tea.Cmd
	m, pushCmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(pushCmd)

	if !sliceHas(requestedURLs, "/namespaces/default/pods") {
		t.Fatalf("expected /namespaces/default/pods to be fetched after pushing on 'default'; requested URLs: %v", requestedURLs)
	}
	view = m.View()
	if !strings.Contains(view, "pod-a-in-default") {
		t.Errorf("pods screen missing pod-a-in-default; view:\n%s", view)
	}
}

// TestParamsBindIgnoresUnusedSources reproduces the kube.yaml regression:
// pushing to a screen that USES one parameterized source while the
// Multi config ALSO defines other parameterized sources (for sibling
// screens) used to silently fail because SubstituteScreen tried to
// bind every source's required params from the push-site bind:.
// Sources for OTHER screens would error ("required X not provided")
// and the push would never happen.
//
// Mirrors the kube.yaml shape: a "pods" screen using the `pods`
// source with `{namespace}`, while `pod_detail`/`pod_logs` (for the
// downstream `detail` screen) also exist and need `{namespace, name}`.
// Pushing namespaces→pods must succeed.
func TestParamsBindIgnoresUnusedSources(t *testing.T) {
	var requestedURLs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURLs = append(requestedURLs, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/namespaces":
			fmt.Fprint(w, `{"items":[{"metadata":{"name":"default"}}]}`)
		case strings.HasPrefix(r.URL.Path, "/namespaces/") && strings.HasSuffix(r.URL.Path, "/pods"):
			ns := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/namespaces/"), "/pods")
			fmt.Fprintf(w, `{"items":[{"metadata":{"name":"pod-a","namespace":"%s"}}]}`, ns)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"namespaces": cfg.NewEntry(&cfg.Source{Type: "http", Root: "items", URL: srv.URL + "/namespaces"}),
		"pods": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"namespace": {Type: "string", Required: true},
		},

			// Unrelated to the namespaces→pods push: these are used by
			// the (not-yet-pushed-to) detail screen. They need params
			// the namespaces→pods bind cannot supply, but that
			// shouldn't break the namespaces→pods push.
			Root: "items", URL: srv.URL + "/namespaces/${params.namespace}/pods"},
		),

		"pod_detail": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"namespace": {Type: "string", Required: true},
			"name":      {Type: "string", Required: true},
		}, URL: srv.URL + "/namespaces/${params.namespace}/pods/${params.name}"},
		),
		"pod_logs": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"namespace": {Type: "string", Required: true},
			"name":      {Type: "string", Required: true},
		}, URL: srv.URL + "/namespaces/${params.namespace}/pods/${params.name}/log"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"namespaces_list": {
			Type: "list", Source: "namespaces", Item: "metadata.name",
			Filterable: true,
		},
		"pods_table": {
			Type: "table", Source: "pods",
			Columns: []cfg.Column{
				{Title: "Name", Width: 24, Value: cfg.Path{"metadata.name"}},
				{Title: "Namespace", Width: 16, Value: cfg.Path{"metadata.namespace"}},
			},
		},
		// Components for the detail screen — would be touched only
		// if we pushed into detail (which this test doesn't).
		"pod_inspector": {Type: "inspector", Source: "pod_detail"},
		"pod_logs_view": {Type: "logview", Source: "pod_logs"},
	},
		Screens: map[string]*cfg.Screen{
			"namespaces": {
				Layout: cfg.Node{Component: "namespaces_list"},
				Actions: []cfg.ActionBinding{
					{
						From: "namespaces_list", Action: "open_pods", Key: "enter",
						Bind: map[string]string{"namespace": "${selection}"},
					},
				},
			},
			"pods": {
				Layout: cfg.Node{Component: "pods_table"},
				// No further push here — keep this test focused on the
				// namespaces→pods step.
			},
			"detail": {
				Layout: cfg.Node{VStack: []cfg.Item{
					{Flex: 2, Node: cfg.Node{Component: "pod_inspector"}},
					{Flex: 3, Node: cfg.Node{Component: "pod_logs_view"}},
				}},
			},
		},
		Initial: "namespaces"},
		Actions: pushRegistry(),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	multi := &Multi{
		Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources,
		Actions: c.Actions,
	}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain := func(start tea.Cmd) {
		queue := []tea.Cmd{start}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range bm {
					queue = append(queue, sub)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	drain(m.Init())

	var pushCmd tea.Cmd
	m, pushCmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(pushCmd)

	// The push must have succeeded — pods URL fetched, view shows
	// the pod. Pre-fix, this errored on pod_detail/pod_logs missing
	// `name` and we never got here.
	if !sliceHas(requestedURLs, "/namespaces/default/pods") {
		t.Fatalf("namespaces→pods push didn't fire; requested URLs: %v", requestedURLs)
	}
	view := m.View()
	if !strings.Contains(view, "pod-a") {
		t.Errorf("pods screen missing the pod row; view:\n%s", view)
	}
}

// TestParamsBindMissingRequired verifies that pushing to a screen
// whose parameterized source requires a param that the bind: block
// doesn't supply is rejected cleanly — no push happens, no broken
// fetch is issued.
//
// The error itself surfaces via app.Error → statusbar (a brief
// post-action feedback, not a modal). The contract under test is the
// load-bearing one: a missing bind doesn't construct a half-built
// destination screen that 404s on first fetch. Asserting on the
// statusbar text is fragile (it's outside the body rect we render in
// tests); asserting on "we're still on users + no posts URL was hit"
// captures the actual guarantee.
func TestParamsBindMissingRequired(t *testing.T) {
	var requestedURLs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURLs = append(requestedURLs, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"ID":"7","Name":"Ada"}]`)
	}))
	defer srv.Close()

	c := cfg.Config{Data: cfg.DataBlock{Sources: map[string]*cfg.Source{"users": cfg.NewEntry(&cfg.Source{Type: "http", URL: srv.URL + "/users"}),
		"posts_by_user": cfg.NewEntry(&cfg.Source{Type: "http", Parameters: map[string]*cfg.Parameter{
			"user_id": {Type: "string", Required: true},
		}, URL: srv.URL + "/users/${params.user_id}/posts"},
		)}}, TUI: cfg.TUIBlock{Components: map[string]*cfg.Component{
		"users_table": {
			Type: "table", Source: "users",
			Columns: []cfg.Column{
				{Title: "ID", Width: 8, Value: cfg.Path{"ID"}},
				{Title: "Name", Width: 16, Value: cfg.Path{"Name"}},
			},
		},
		"posts_table": {
			Type: "table", Source: "posts_by_user",
			Columns: []cfg.Column{{Title: "Title", Value: cfg.Path{"title"}}},
		},
	},
		Screens: map[string]*cfg.Screen{
			"users": {
				Layout: cfg.Node{Component: "users_table"},
				Actions: []cfg.ActionBinding{
					// Intentionally omit Bind — destination needs user_id.
					{From: "users_table", Action: "open_posts", Key: "enter"},
				},
			},
			"posts": {Layout: cfg.Node{Component: "posts_table"}},
		},
		Initial: "users"},
		Actions: pushRegistry(),
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	multi := &Multi{
		Screens: c.TUI.Screens, Components: c.TUI.Components, Sources: c.Data.Sources,
		Actions: c.Actions,
	}
	root, err := NewMulti(c.TUI.Initial, multi, build.Selection{}, nil, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root: root, Themes: []theme.Theme{theme.Nord()}, SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	drain := func(start tea.Cmd) {
		queue := []tea.Cmd{start}
		for steps := 0; len(queue) > 0 && steps < 200; steps++ {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			if bm, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range bm {
					queue = append(queue, sub)
				}
				continue
			}
			var next tea.Cmd
			m, next = m.Update(msg)
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	drain(m.Init())

	// Press enter — push should be rejected because user_id wasn't
	// bound; NewMulti returns an error before the screen constructs.
	var pushCmd tea.Cmd
	m, pushCmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	drain(pushCmd)

	// We should still be on the users screen — Ada visible, no posts
	// columns rendered.
	view := m.View()
	if !strings.Contains(view, "Ada") {
		t.Errorf("expected to remain on users screen after rejected push; view:\n%s", view)
	}
	// And critically: no fetch was made against the unresolved
	// `/users/${params.user_id}/posts` URL. If the binding silently
	// fell through, we'd see a request for `/users/` (empty
	// substitution) or for the literal template.
	for _, url := range requestedURLs {
		if strings.Contains(url, "/posts") {
			t.Errorf("destination source was fetched despite missing bind: requested %s", url)
		}
	}
}

func sliceHas(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// stubKube returns an httptest.Server that responds to /api/v1/pods
// with a PodList containing pods named per the input map (name →
// status.phase). Mirrors the wire shape kube_multi.yaml's http sources
// expect.
func stubKube(pods map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var items []string
		for name, phase := range pods {
			items = append(items, fmt.Sprintf(
				`{"metadata":{"name":%q,"namespace":"default"},"status":{"phase":%q}}`,
				name, phase,
			))
		}
		body := fmt.Sprintf(`{"kind":"PodList","apiVersion":"v1","items":[%s]}`, strings.Join(items, ","))
		fmt.Fprint(w, body)
	}))
}

// pushRegistry declares every push action the multi-screen fixtures bind
// to. Pushes are registry entries now, not their own on_key: block.
func pushRegistry() map[string]*cfg.Action {
	return map[string]*cfg.Action{
		"open_pods":  {Push: "pods"},
		"open_posts": {Push: "posts"},
	}
}
