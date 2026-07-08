package config

// Source is one entry in `data.sources:`. A Source carries a `type:`
// discriminator that picks its kind (one of leaf kinds — http /
// exec / file / websocket / static / merge — or operator kinds —
// passthrough / filter / project / derive / sort / union / compose /
// join / cache). Each kind reads only the subset of fields it cares
// about; the others are silently ignored.
//
// Bag-of-fields is intentional. A polymorphic-dispatch variant
// (one struct per kind, registry, custom UnmarshalYAML) lived here
// briefly and was reverted — the YAML surface is what users see and
// it didn't need 700 lines of type-system machinery underneath.
// See plans/revert-polymorphic-dispatch.md for the rationale.
//
// Adding a new kind: append the kind's fields here (or reuse a
// shared field like URL or From if applicable), add a `case "newkind"`
// branch to each of the five switch sites (Validate, Upstreams,
// Clone, BindParams, SubstituteStrings), and add the runtime builder
// in internal/{datasource,pipeline}.

import (
	"fmt"
	"sort"
	"time"
)

type Source struct {
	// Discriminator. Required.
	Type string `yaml:"type"`

	// Shared by every kind.
	Parameters map[string]*Parameter `yaml:"parameters,omitempty"`
	// Pipe is sugar for a chain of single-input transforms applied
	// on top of this entry's output. Each stage is itself a Source
	// (so every stage carries a `type:`); validators restrict
	// stages to single-input transforms (filter / project / derive
	// / sort / cache). Desugared at load into a chain of synthesized
	// intermediates.
	Pipe []Source `yaml:"pipe,omitempty"`

	// ── Leaf-shared fields (http / exec / file / websocket / static / merge) ──
	// Root is a dot-path into the response selecting the iterable
	// root for list/table bindings. Empty = response itself.
	Root string `yaml:"root,omitempty"`
	// Refresh is the polling interval (e.g. "30s"). Empty = fetch once.
	Refresh string `yaml:"refresh,omitempty"`
	// Timeout caps per-fetch latency. Default 10s for http/exec.
	Timeout string `yaml:"timeout,omitempty"`
	// Format selects how the response body is parsed:
	//   "" / "json"   parse as JSON (default)
	//   "text"        keep the body as a raw string
	Format string `yaml:"format,omitempty"`

	// ── http + websocket share URL and Headers; http + exec share Follow ──
	URL     string            `yaml:"url,omitempty"`
	Method  string            `yaml:"method,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Body    string            `yaml:"body,omitempty"`
	Follow  bool              `yaml:"follow,omitempty"`

	// Paginate opts an http source into multi-page walking. On Fetch,
	// the source fetches the URL, applies Root: to get the page's
	// items, extracts the next page's URL via the configured strategy,
	// and repeats until there's no next or MaxPages is reached. The
	// concatenated item list is returned. Mutually exclusive with
	// Follow (streaming) and Format: text. See PaginateConfig.
	Paginate *PaginateConfig `yaml:"paginate,omitempty"`

	// ── exec ──
	Command []string          `yaml:"command,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`

	// ── file ──
	Path string `yaml:"path,omitempty"`

	// ── websocket ──
	InitialMessages []string `yaml:"initial_messages,omitempty"`

	// ── static ──
	Data any `yaml:"data,omitempty"`

	// ── merge + union (same shape) ──
	Sources  []string     `yaml:"sources,omitempty"`
	Children []MergeChild `yaml:"children,omitempty"`
	TagField string       `yaml:"tag_field,omitempty"`
	MetaKey  *string      `yaml:"meta_key,omitempty"`
	OnError  string       `yaml:"on_error,omitempty"`

	// ── passthrough / filter / project / derive / sort / cache ──
	From string `yaml:"from,omitempty"`

	// ── filter ──
	Where string `yaml:"where,omitempty"`

	// ── project ──
	Keep map[string]string `yaml:"keep,omitempty"`

	// ── derive ──
	Compute map[string]string `yaml:"compute,omitempty"`

	// ── sort ──
	By    string `yaml:"by,omitempty"`
	Order string `yaml:"order,omitempty"`

	// ── compose ──
	Parts map[string]string `yaml:"parts,omitempty"`

	// ── join ──
	Driver  JoinDriver            `yaml:"driver,omitempty"`
	Lookups map[string]JoinLookup `yaml:"lookups,omitempty"`
	Emit    string                `yaml:"emit,omitempty"`

	// ── cache (as an operator, `type: cache`) ──
	TTL string `yaml:"ttl,omitempty"`

	// ── cache (as a per-leaf modifier on any parameterized source) ──
	//
	// Distinct from `type: cache`. The operator wraps an upstream once
	// and caches ONE snapshot for a TTL — right for sources whose
	// params don't change (a background poll that everyone shares).
	// This field, in contrast, opts a PARAMS-BOUND source into
	// memoisation keyed on the parameter tuple: N different calls
	// with M unique (param → value) tuples produce M upstream
	// fetches, not N. Populated on http/exec/file/websocket/static
	// sources that declare `parameters:`, or on any source used as a
	// join lookup, so cursor-driven inspectors + join-per-row lookups
	// don't hammer the upstream.
	Cache *CacheSpec `yaml:"cache,omitempty"`
}

// CacheSpec configures the per-leaf param-aware cache attached to a
// parameterized source. Applies to Fetch (snapshot mode) only —
// streaming Subscribe passes through untouched, since caching
// incremental events doesn't fit the (params → snapshot) model.
type CacheSpec struct {
	// TTL is the freshness window. Each entry evicts once
	// time.Since(insertedAt) >= TTL, then the next Fetch with the
	// same params re-invokes the upstream. Required; must parse
	// via time.ParseDuration ("30s", "2m", "1h").
	TTL string `yaml:"ttl"`
	// Size caps the entry count. When a Fetch with a new params
	// tuple would push over Size, the least-recently-used entry
	// evicts. Zero (or omitted) means 100. Negative means unbounded
	// — safe only when the parameter space itself is bounded (e.g.
	// enum values); watch memory otherwise.
	Size int `yaml:"size,omitempty"`
}

// NewEntry is an identity passthrough kept alive for test
// fixtures. The reverse migration leaves
// `cfg.NewEntry(&cfg.Source{...})` at every site so tests don't
// need to drop the wrapper themselves; this helper makes it valid.
// Production code never uses it.
func NewEntry(s *Source) *Source { return s }

// Kind classification — used by the dispatch switches below and by
// downstream consumers (build, wrangl --list / --describe) that need
// to distinguish leaves from operator pipelines.

// leafTypes is the set of `type:` values that fetch externally
// (rather than transforming an upstream).
var leafTypes = map[string]bool{
	"http": true, "exec": true, "file": true,
	"websocket": true, "static": true, "merge": true,
}

// operatorTypes is the set of `type:` values that wrap one or more
// upstream entries.
var operatorTypes = map[string]bool{
	"passthrough": true, "filter": true, "project": true, "derive": true,
	"sort": true, "union": true, "compose": true, "join": true, "cache": true,
}

// IsLeaf reports whether the source's type is a leaf kind.
func (s *Source) IsLeaf() bool {
	if s == nil {
		return false
	}
	return leafTypes[s.Type]
}

// IsOperator reports whether the source's type is an operator kind.
func (s *Source) IsOperator() bool {
	if s == nil {
		return false
	}
	return operatorTypes[s.Type]
}

// Validate runs per-kind required-field and value-range checks. path
// is the YAML path of this entry (e.g. `data.sources.users`), used in
// error messages.
func (s *Source) Validate(path string) error {
	if err := validateParametersMap(s.Parameters, path); err != nil {
		return err
	}
	// Pipe stages are themselves Sources. Each must be a single-
	// input transform; the desugar pass enforces that no stage
	// carries `from:` (input is implicit from the previous step).
	for i, stage := range s.Pipe {
		switch stage.Type {
		case "filter", "project", "derive", "sort", "cache":
			// allowed
		case "":
			return fmt.Errorf("%s.pipe[%d]: missing type", path, i)
		default:
			return fmt.Errorf("%s.pipe[%d]: %s is not allowed (single-input transforms only: filter / project / derive / sort / cache)", path, i, stage.Type)
		}
		if stage.From != "" {
			return fmt.Errorf("%s.pipe[%d].%s: `from:` is implicit in pipe stages — omit it", path, i, stage.Type)
		}
		if err := stage.Validate(fmt.Sprintf("%s.pipe[%d]", path, i)); err != nil {
			return err
		}
	}

	switch s.Type {
	case "":
		return fmt.Errorf("%s: missing type", path)
	case "http":
		return s.validateHTTP(path)
	case "exec":
		return s.validateExec(path)
	case "file":
		return s.validateFile(path)
	case "websocket":
		return s.validateWebsocket(path)
	case "static":
		return s.validateStatic(path)
	case "merge":
		return s.validateMergeUnion(path, "merge source")
	case "passthrough":
		return s.validatePassthrough(path)
	case "filter":
		return s.validateFilter(path)
	case "project":
		return s.validateProject(path)
	case "derive":
		return s.validateDerive(path)
	case "sort":
		return s.validateSort(path)
	case "union":
		return s.validateMergeUnion(path+".union", "union")
	case "compose":
		return s.validateCompose(path)
	case "join":
		return s.validateJoin(path)
	case "cache":
		return s.validateCache(path)
	}
	return fmt.Errorf("%s: unknown type %q (want one of: http|exec|file|websocket|static|merge|passthrough|filter|project|derive|sort|union|compose|join|cache)", path, s.Type)
}

// Upstreams returns every entry name this source reads from. Leaves
// usually return nil (merge being the exception — its children are
// upstreams). Operators return their `from:` / `sources:` / `parts:`
// / `driver+lookups` references. Used by validateEntryUpstreams +
// cycle detection + build's recursive resolve.
func (s *Source) Upstreams() []string {
	switch s.Type {
	case "merge", "union":
		return mergeUnionUpstreamNames(s)
	case "compose":
		if len(s.Parts) == 0 {
			return nil
		}
		out := make([]string, 0, len(s.Parts))
		for _, ref := range s.Parts {
			out = append(out, ref)
		}
		sort.Strings(out)
		return out
	case "join":
		out := []string{}
		if s.Driver.From != "" {
			out = append(out, s.Driver.From)
		}
		for _, l := range s.Lookups {
			if l.From != "" {
				out = append(out, l.From)
			}
		}
		sort.Strings(out)
		return out
	case "passthrough", "filter", "project", "derive", "sort", "cache":
		if s.From == "" {
			return nil
		}
		return []string{s.From}
	}
	return nil
}

func mergeUnionUpstreamNames(s *Source) []string {
	seen := map[string]struct{}{}
	add := func(name string) {
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, x := range s.Sources {
		add(x)
	}
	for _, c := range s.Children {
		add(c.Source)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Clone returns a deep copy of s. Reference fields (Headers, Env,
// Command, Sources, Children, InitialMessages, Pipe, Keep, Compute,
// Parts, Lookups) are duplicated; everything else copies by value.
// Used by the join operator's per-row lookup pattern and by
// SubstituteScreen.
func (s *Source) Clone() *Source {
	if s == nil {
		return nil
	}
	out := *s
	if s.Headers != nil {
		out.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			out.Headers[k] = v
		}
	}
	if s.Env != nil {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			out.Env[k] = v
		}
	}
	if s.Command != nil {
		out.Command = append([]string(nil), s.Command...)
	}
	if s.Sources != nil {
		out.Sources = append([]string(nil), s.Sources...)
	}
	if s.Children != nil {
		out.Children = append([]MergeChild(nil), s.Children...)
	}
	if s.InitialMessages != nil {
		out.InitialMessages = append([]string(nil), s.InitialMessages...)
	}
	if s.Pipe != nil {
		out.Pipe = append([]Source(nil), s.Pipe...)
	}
	if s.Paginate != nil {
		cp := *s.Paginate
		out.Paginate = &cp
	}
	// Parameters / Keep / Compute / Parts / Lookups are treated as
	// immutable declarations after load; aliasing is safe.
	return &out
}

// BindParams resolves caller-supplied parameter values against s's
// declared parameter schema and substitutes ${params.X} tokens in
// every templated string field. Mutates s in place; callers that
// want to preserve the original should Clone first.
//
// Only leaf kinds carry templated fields. Operator kinds with
// declared parameters use the resolved values in their operator
// expressions (params.X in `where:` / `keep:` / etc.), bound via
// pipeline.Build's boundParams arg — not via this method.
func (s *Source) BindParams(params map[string]string) error {
	if len(s.Parameters) == 0 {
		if len(params) > 0 {
			return fmt.Errorf("source has no `parameters:` declared (got: %v)", paramKeys(params))
		}
		return nil
	}
	resolved, err := ResolveParams(s.Parameters, params)
	if err != nil {
		return err
	}
	s.SubstituteStrings(func(t string) string { return substituteParams(t, resolved) })
	return nil
}

// SubstituteStrings applies fn to every templated string field on
// this source. Used by both BindParams (with a ${params.X} resolver)
// and SubstituteScreen (with a ${selection.X} / ${env.X} resolver).
//
// Only fields that actually carry templates are touched; unused
// fields (Where, Keep values, etc.) are left alone — those are
// expression-language strings, not templates.
func (s *Source) SubstituteStrings(fn func(string) string) {
	if s == nil {
		return
	}
	s.URL = fn(s.URL)
	s.Body = fn(s.Body)
	s.Method = fn(s.Method)
	s.Path = fn(s.Path)
	for k, v := range s.Headers {
		s.Headers[k] = fn(v)
	}
	for i, c := range s.Command {
		s.Command[i] = fn(c)
	}
	for k, v := range s.Env {
		s.Env[k] = fn(v)
	}
	for i, m := range s.InitialMessages {
		s.InitialMessages[i] = fn(m)
	}
}

// ────────────────────────────────────────────────────────────────────
// Per-kind validators. Each reads only the fields its kind requires.
// Shared rules (format whitelist, refresh / timeout duration parse)
// live in validateLeafShared so the leaf validators stay short.
// ────────────────────────────────────────────────────────────────────

func (s *Source) validateLeafShared(path string) error {
	switch s.Format {
	case "", "json", "text":
	default:
		return fmt.Errorf("%s: unknown format %q (want json|text)", path, s.Format)
	}
	if s.Refresh != "" {
		if _, err := time.ParseDuration(s.Refresh); err != nil {
			return fmt.Errorf("%s: invalid refresh %q: %w", path, s.Refresh, err)
		}
	}
	if s.Timeout != "" {
		if _, err := time.ParseDuration(s.Timeout); err != nil {
			return fmt.Errorf("%s: invalid timeout %q: %w", path, s.Timeout, err)
		}
	}
	if s.Cache != nil {
		if s.Cache.TTL == "" {
			return fmt.Errorf("%s.cache: `ttl:` is required (duration string, e.g. 30s)", path)
		}
		if _, err := time.ParseDuration(s.Cache.TTL); err != nil {
			return fmt.Errorf("%s.cache: invalid ttl %q: %w", path, s.Cache.TTL, err)
		}
	}
	return nil
}

func (s *Source) validateHTTP(path string) error {
	if err := s.validateLeafShared(path); err != nil {
		return err
	}
	if s.URL == "" {
		return fmt.Errorf("%s: http source needs url", path)
	}
	if s.Paginate != nil {
		if err := s.Paginate.validate(path + ".paginate"); err != nil {
			return err
		}
		if s.Follow {
			return fmt.Errorf("%s: `paginate:` and `follow: true` are mutually exclusive (pagination walks a finite N-page snapshot; follow is an open-ended stream)", path)
		}
		if s.Format == "text" {
			return fmt.Errorf("%s: `paginate:` requires json format (needs to parse the response to find the next-page reference)", path)
		}
	}
	return nil
}

// validate enforces PaginateConfig's per-strategy required fields +
// value ranges. path is the YAML path (e.g.
// `data.sources.users.paginate`); errors embed it.
func (p *PaginateConfig) validate(path string) error {
	switch p.Strategy {
	case "":
		return fmt.Errorf("%s: `strategy:` is required (one of: link)", path)
	case "link":
		if p.NextPath == "" {
			return fmt.Errorf("%s.next_path: required for strategy=link (dot-path into the response body to the next page's URL, e.g. \"next\")", path)
		}
	default:
		return fmt.Errorf("%s: unknown strategy %q (want: link)", path, p.Strategy)
	}
	if p.MaxPages < 0 {
		return fmt.Errorf("%s.max_pages: must be >= 0 (0 = unlimited; positive = cap)", path)
	}
	switch p.OnPageError {
	case "", "fail", "skip":
	default:
		return fmt.Errorf("%s.on_page_error: unknown %q (want: fail|skip)", path, p.OnPageError)
	}
	return nil
}

func (s *Source) validateExec(path string) error {
	if err := s.validateLeafShared(path); err != nil {
		return err
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("%s: exec source needs command (non-empty argv)", path)
	}
	return nil
}

func (s *Source) validateFile(path string) error {
	if err := s.validateLeafShared(path); err != nil {
		return err
	}
	if s.Path == "" {
		return fmt.Errorf("%s: file source needs path", path)
	}
	return nil
}

func (s *Source) validateWebsocket(path string) error {
	if err := s.validateLeafShared(path); err != nil {
		return err
	}
	if s.URL == "" {
		return fmt.Errorf("%s: websocket source needs url (ws:// or wss://)", path)
	}
	return nil
}

func (s *Source) validateStatic(path string) error {
	if err := s.validateLeafShared(path); err != nil {
		return err
	}
	if s.Data == nil {
		return fmt.Errorf("%s: static source needs data (inline YAML — list, object, scalar, etc.)", path)
	}
	return nil
}

func (s *Source) validateMergeUnion(path, kindName string) error {
	if s.Type == "merge" {
		if err := s.validateLeafShared(path); err != nil {
			return err
		}
	}
	hasSources := len(s.Sources) > 0
	hasChildren := len(s.Children) > 0
	switch {
	case hasSources && hasChildren:
		return fmt.Errorf("%s: set either `sources:` (shorthand) or `children:` (per-child tags), not both", path)
	case !hasSources && !hasChildren:
		return fmt.Errorf("%s: %s needs `sources:` or `children:`", path, kindName)
	case hasChildren && s.TagField != "":
		return fmt.Errorf("%s: `tag_field:` is only valid with `sources:`; with `children:` each child declares its own `tags:`", path)
	}
	for i, child := range s.Children {
		if child.Source == "" {
			return fmt.Errorf("%s.children[%d]: source is required", path, i)
		}
	}
	switch s.OnError {
	case "", "fail", "skip":
	default:
		return fmt.Errorf("%s: unknown on_error %q (want fail|skip)", path, s.OnError)
	}
	return nil
}

func (s *Source) validatePassthrough(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s: passthrough needs `from:`", path)
	}
	return nil
}

func (s *Source) validateFilter(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s.filter: `from:` is required", path)
	}
	if s.Where == "" {
		return fmt.Errorf("%s.filter: `where:` is required (predicate expression)", path)
	}
	return nil
}

func (s *Source) validateProject(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s.project: `from:` is required", path)
	}
	if len(s.Keep) == 0 {
		return fmt.Errorf("%s.project: `keep:` is required (non-empty map of output_name -> expression)", path)
	}
	for k, v := range s.Keep {
		if k == "" {
			return fmt.Errorf("%s.project.keep: empty output key", path)
		}
		if v == "" {
			return fmt.Errorf("%s.project.keep.%s: empty expression", path, k)
		}
	}
	return nil
}

func (s *Source) validateDerive(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s.derive: `from:` is required", path)
	}
	if len(s.Compute) == 0 {
		return fmt.Errorf("%s.derive: `compute:` is required (non-empty map of output_name -> expression)", path)
	}
	for k, v := range s.Compute {
		if k == "" {
			return fmt.Errorf("%s.derive.compute: empty output key", path)
		}
		if v == "" {
			return fmt.Errorf("%s.derive.compute.%s: empty expression", path, k)
		}
	}
	return nil
}

func (s *Source) validateSort(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s.sort: `from:` is required", path)
	}
	if s.By == "" {
		return fmt.Errorf("%s.sort: `by:` is required (key expression)", path)
	}
	switch s.Order {
	case "", "asc", "desc":
	default:
		return fmt.Errorf("%s.sort.order: unknown %q (want asc|desc)", path, s.Order)
	}
	return nil
}

func (s *Source) validateCompose(path string) error {
	if len(s.Parts) == 0 {
		return fmt.Errorf("%s.compose: `parts:` is required (non-empty map of output_key -> input_name)", path)
	}
	for k, v := range s.Parts {
		if k == "" {
			return fmt.Errorf("%s.compose.parts: empty output key", path)
		}
		if v == "" {
			return fmt.Errorf("%s.compose.parts.%s: empty input name", path, k)
		}
	}
	switch s.OnError {
	case "", "fail", "skip":
	default:
		return fmt.Errorf("%s.compose: unknown on_error %q (want fail|skip)", path, s.OnError)
	}
	return nil
}

func (s *Source) validateJoin(path string) error {
	if s.Driver.From == "" {
		return fmt.Errorf("%s.join.driver: `from:` is required", path)
	}
	if len(s.Lookups) == 0 {
		return fmt.Errorf("%s.join: at least one lookup is required", path)
	}
	for lname, look := range s.Lookups {
		if lname == "" {
			return fmt.Errorf("%s.join.lookups: empty lookup name", path)
		}
		if look.From == "" {
			return fmt.Errorf("%s.join.lookups.%s: `from:` is required", path, lname)
		}
		if len(look.On) == 0 {
			return fmt.Errorf("%s.join.lookups.%s: `on:` is required (map of lookup param → expression against driver row)", path, lname)
		}
		for paramName, expr := range look.On {
			if paramName == "" {
				return fmt.Errorf("%s.join.lookups.%s.on: empty param name", path, lname)
			}
			if expr == "" {
				return fmt.Errorf("%s.join.lookups.%s.on.%s: empty expression", path, lname, paramName)
			}
		}
	}
	switch s.Emit {
	case "", "separate", "merged":
	default:
		return fmt.Errorf("%s.join: unknown emit %q (want separate|merged)", path, s.Emit)
	}
	switch s.OnError {
	case "", "fail", "skip":
	default:
		return fmt.Errorf("%s.join: unknown on_error %q (want fail|skip)", path, s.OnError)
	}
	return nil
}

func (s *Source) validateCache(path string) error {
	if s.From == "" {
		return fmt.Errorf("%s.cache: `from:` is required", path)
	}
	if s.TTL == "" {
		return fmt.Errorf("%s.cache: `ttl:` is required (duration string, e.g. 30s)", path)
	}
	if _, err := time.ParseDuration(s.TTL); err != nil {
		return fmt.Errorf("%s.cache: invalid ttl %q: %w", path, s.TTL, err)
	}
	return nil
}
