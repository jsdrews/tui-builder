package config

// Entry-based desugar for the unified `pipe:` shorthand. Operates on
// the `data.sources:` map and rewrites two patterns into a normalized
// chain of standalone entries:
//
//  1. Leaf entry with non-empty Pipe — the leaf moves to
//     `_<name>_raw`; a fresh `passthrough` at `<name>` takes over
//     with `from: _<name>_raw, pipe: [stages]`.
//  2. Passthrough entry with non-empty Pipe — expand into N-1
//     anonymous intermediates (`_<name>_step<i>`) plus a final
//     entry at `<name>` carrying the last stage.
//
// Composed: a leaf-with-pipe becomes
//
//	_X_raw   (leaf)            ← fetches externally
//	_X_step1 (filter from=…)   ← intermediate stage 1
//	_X_step2 (project from=…)  ← intermediate stage 2
//	X        (sort from=…)     ← user-facing entry
//
// Single-stage `pipe:` shortcuts the chain: the stage's operator is
// lifted directly onto the user-facing name with `from:` set to the
// upstream — no intermediates created.

import (
	"fmt"
	"sort"
)

// ExpandSourcesPipe is the single entry point. Mutates sources in
// place. Idempotent (re-running on an already-expanded map is a
// no-op).
func ExpandSourcesPipe(sources map[string]*Source) error {
	// Phase 1: leaves with pipe → rename + synthesize passthrough.
	var leafNames []string
	for name, s := range sources {
		if s != nil && s.IsLeaf() && len(s.Pipe) > 0 {
			leafNames = append(leafNames, name)
		}
	}
	sort.Strings(leafNames)
	for _, name := range leafNames {
		if err := wrapLeafWithPipe(name, sources); err != nil {
			return err
		}
	}

	// Phase 2: passthrough-with-pipe (including the ones synthesized
	// by phase 1).
	var passNames []string
	for name, s := range sources {
		if s != nil && s.Type == "passthrough" && len(s.Pipe) > 0 {
			passNames = append(passNames, name)
		}
	}
	sort.Strings(passNames)
	for _, name := range passNames {
		if err := expandPassthroughPipe(name, sources); err != nil {
			return err
		}
	}
	return nil
}

func wrapLeafWithPipe(name string, sources map[string]*Source) error {
	s := sources[name]
	pathPrefix := "data.sources." + name

	// merge is multi-input — pipe makes no sense; reject early.
	if s.Type == "merge" {
		return fmt.Errorf("%s: `pipe:` is not supported on `type: merge` (multi-input); declare a separate entry with `union:` or `compose:` if you need transforms on merged data", pathPrefix)
	}

	stages := s.Pipe
	if err := validatePipeStages(pathPrefix, stages); err != nil {
		return err
	}
	s.Pipe = nil

	rawName := "_" + name + "_raw"
	if _, exists := sources[rawName]; exists {
		return fmt.Errorf("%s: desugared raw name %q collides with an existing entry", pathPrefix, rawName)
	}
	sources[rawName] = s
	delete(sources, name)

	sources[name] = &Source{
		Type: "passthrough",
		From: rawName,
		Pipe: stages,
	}
	return nil
}

func expandPassthroughPipe(name string, sources map[string]*Source) error {
	s := sources[name]
	pathPrefix := "data.sources." + name

	if s.From == "" {
		return fmt.Errorf("%s: `pipe:` requires `from:`", pathPrefix)
	}
	stages := s.Pipe
	if err := validatePipeStages(pathPrefix, stages); err != nil {
		return err
	}

	if len(stages) == 1 {
		// Single-stage sugar: lift the stage's operator into the
		// user-facing name with from: set to the parent's upstream.
		lifted := stages[0]
		lifted.From = s.From
		sources[name] = &lifted
		return nil
	}

	// N stages: N-1 intermediates + parent becomes the last stage.
	upstream := s.From
	for i := 0; i < len(stages)-1; i++ {
		stepName := fmt.Sprintf("_%s_step%d", name, i+1)
		if _, exists := sources[stepName]; exists {
			return fmt.Errorf("%s: desugared step name %q collides with an existing entry", pathPrefix, stepName)
		}
		mid := stages[i]
		mid.From = upstream
		sources[stepName] = &mid
		upstream = stepName
	}
	lastIdx := len(stages) - 1
	last := stages[lastIdx]
	last.From = upstream
	sources[name] = &last
	return nil
}

// validatePipeStages enforces that each stage is a single-input
// transform with no stage-level `from:` (input is implicit from the
// previous step). Multi-input operators (union / compose / join) are
// rejected because they take additional upstreams that don't fit
// the implicit-previous-step model.
func validatePipeStages(pathPrefix string, stages []Source) error {
	for i, stage := range stages {
		switch stage.Type {
		case "filter", "project", "derive", "sort", "cache":
			// allowed
		case "":
			return fmt.Errorf("%s.pipe[%d]: empty stage (missing type)", pathPrefix, i)
		default:
			return fmt.Errorf("%s.pipe[%d]: %s is not allowed (single-input transforms only: filter / project / derive / sort / cache)", pathPrefix, i, stage.Type)
		}
		if stage.From != "" {
			return fmt.Errorf("%s.pipe[%d].%s: `from:` is implicit in pipe stages — omit it", pathPrefix, i, stage.Type)
		}
	}
	return nil
}
