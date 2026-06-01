package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Path is a dot-path or a fallback chain of dot-paths. In YAML it
// accepts either a single string (the common case) or a list of
// strings; at resolve time the first path that produces a non-empty
// value wins. This mirrors how `kubectl get pods` derives its STATUS
// column from container state with a fall-through to status.phase.
//
//	value: "status.phase"                                  # single
//	value:                                                  # chain
//	  - "status.containerStatuses.0.state.waiting.reason"
//	  - "status.containerStatuses.0.state.terminated.reason"
//	  - "status.phase"
type Path []string

// UnmarshalYAML lets a Path field be specified as either a scalar string
// or a sequence of strings in YAML.
func (p *Path) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		*p = []string{s}
		return nil
	case yaml.SequenceNode:
		var ss []string
		if err := node.Decode(&ss); err != nil {
			return err
		}
		*p = ss
		return nil
	}
	return fmt.Errorf("path: expected string or list of strings, got %s", node.Tag)
}

// MarshalYAML round-trips Path back to YAML — a single-element chain
// renders as the bare scalar (so configs don't grow noise after a load
// + save cycle), longer chains render as a sequence.
func (p Path) MarshalYAML() (any, error) {
	if len(p) == 1 {
		return p[0], nil
	}
	return []string(p), nil
}
