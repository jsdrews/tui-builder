// Package output is the data layer's stdout-side sink. It takes a
// datasource.Source (which includes pipelines, since Pipeline satisfies
// Source) and writes its data to an io.Writer in JSON (one-shot
// polled sources) or NDJSON (streamed sources, one event per line).
//
// Like internal/pipeline, this package MUST NOT import internal/screen,
// internal/build, or any tuilib package — the boundary that makes the
// wrangl binary TUI-free.
package output
