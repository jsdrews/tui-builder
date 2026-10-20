package datasource

import "sync"

// notices remembers the last notice a source posted on each subject, so
// a condition that holds on every refresh (a walk cut short, a total
// that isn't there) is said once, and again only when it changes.
type notices struct {
	mu   sync.Mutex
	last map[string]string
}

// post sends msg unless it's what subject last said.
func (n *notices) post(subject, msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last[subject] == msg {
		return
	}
	if n.last == nil {
		n.last = map[string]string{}
	}
	n.last[subject] = msg
	notify("%s", msg)
}

// clear forgets subject, so the next notice on it is posted again.
func (n *notices) clear(subject string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.last, subject)
}
