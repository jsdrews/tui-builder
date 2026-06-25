// Package pipeline composes and transforms data flowing from sources to
// consumers. A Pipeline wraps an upstream (a source or another
// pipeline) and either passes its data through (v1) or applies an
// operation (future: filter, project, sort, derive, union, compose,
// join). Pipelines satisfy datasource.Source so any consumer that
// reads from a source can read from a pipeline without changes —
// including the TUI's component-binding layer and wrangl's stdout
// printer.
//
// # Hard import rule
//
// This package — and internal/datasource, internal/output, and
// internal/config — MUST NOT import anything from internal/screen,
// internal/build, or any tuilib package. The data layer is
// architecturally independent of the TUI. A CI check enforces this;
// see .github/workflows/import-boundary.yml.
//
// Violating this rule defeats the point of the wrangl binary: it
// should compile and run with zero TUI code in its dependency graph.
package pipeline
