package screen

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jsdrews/tuilib/pkg/app"
	"github.com/jsdrews/tuilib/pkg/theme"

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

	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			"pods_prod":    {Type: "http", URL: prod.URL + "/api/v1/pods", Root: "items"},
			"pods_staging": {Type: "http", URL: staging.URL + "/api/v1/pods", Root: "items"},
			"pods_all": {
				Type:     "merge",
				Sources:  []string{"pods_prod", "pods_staging"},
				TagField: "cluster",
				OnError:  "skip",
			},
		},
		Components: map[string]*cfg.Component{
			"all_pods": {
				Type:   "table",
				Title:  "All pods",
				Source: "pods_all",
				Columns: []cfg.Column{
					{Title: "Cluster",   Width: 14, Value: cfg.Path{"cluster"}},
					{Title: "Namespace", Width: 18, Value: cfg.Path{"metadata.namespace"}},
					{Title: "Name",      Width: 30, Value: cfg.Path{"metadata.name"}},
					{Title: "Status",    Width: 12, Value: cfg.Path{"status.phase"}},
				},
			},
		},
		Screen: cfg.Screen{
			Title:  "All",
			Layout: cfg.Node{Component: "all_pods"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	root, err := New(&c.Screen, c.Components, c.DataSources, theme.Nord())
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
	c := cfg.Config{
		DataSources: map[string]*cfg.DataSource{
			// Three URLs that will refuse connection — a port range we
			// know nothing's listening on. The merge fails all-3 → modal.
			"pods_prod":    {Type: "http", URL: "http://127.0.0.1:1/api/v1/pods", Root: "items", Timeout: "1s"},
			"pods_staging": {Type: "http", URL: "http://127.0.0.1:2/api/v1/pods", Root: "items", Timeout: "1s"},
			"pods_dev":     {Type: "http", URL: "http://127.0.0.1:3/api/v1/pods", Root: "items", Timeout: "1s"},
			"pods_all": {
				Type:    "merge",
				Sources: []string{"pods_prod", "pods_staging", "pods_dev"},
				OnError: "skip",
			},
		},
		Components: map[string]*cfg.Component{
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
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	root, err := New(&c.Screen, c.Components, c.DataSources, theme.Nord())
	if err != nil {
		t.Fatal(err)
	}
	var m tea.Model = app.New(app.Options{
		Root:       root,
		Themes:     []theme.Theme{theme.Nord()},
		SkipConfig: true,
	})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

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
	// The alert modal title should appear, naming the failed source.
	if !strings.Contains(view, "pods_all: initial fetch failed") {
		t.Errorf("expected alert modal naming the all-clusters-down failure\n--- view ---\n%s", view)
	}
	// And the error body should name the unreachable children.
	if !strings.Contains(view, "merge") {
		t.Errorf("expected merge error message in the alert body\n--- view ---\n%s", view)
	}
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
