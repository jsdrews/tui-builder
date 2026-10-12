package datasource

import (
	"context"
	"testing"

	cfg "github.com/jsdrews/tui-builder/internal/config"
)

// ${resume.since} is empty on the first connect and the resume point on
// a reconnect; the source itself is left unchanged.
func TestExecSubscribeSince(t *testing.T) {
	s, err := newExec(&cfg.Source{Type: "exec", Follow: true,
		Command: []string{"sh", "-c", "echo since=[" + cfg.ResumeSince + "]"}})
	if err != nil {
		t.Fatal(err)
	}
	read := func(ch <-chan Event) string {
		for ev := range ch {
			if ev.Line != "" {
				return ev.Line
			}
		}
		return ""
	}
	first, err := s.(StreamingSource).Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := read(first); got != "since=[]" {
		t.Errorf("first connect: %q, want since=[]", got)
	}
	again, err := s.(ResumableSource).SubscribeSince(context.Background(), "2026-01-01T00:00:03Z")
	if err != nil {
		t.Fatal(err)
	}
	if got := read(again); got != "since=[2026-01-01T00:00:03Z]" {
		t.Errorf("resume: %q", got)
	}
	if first, _ := s.(StreamingSource).Subscribe(context.Background()); read(first) != "since=[]" {
		t.Error("SubscribeSince changed the source")
	}
}
