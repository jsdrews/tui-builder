package gen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"github.com/jsdrews/tui-builder/internal/columns"
	"github.com/jsdrews/tui-builder/internal/config"
)

// Options is what `tui-builder gen` was asked for.
type Options struct {
	Input  Input
	Select Select
	Doc    DocOptions
	// Layout is --layout: "tabs" or "menu" in place of the count's
	// choice.
	Layout string
	// Command is the command line as typed, for the header comment.
	Command string
	// Overrides is --overrides: a file of gen: hints and config to merge
	// in; PruneOverrides makes hints for operations gone from the spec
	// warnings rather than errors.
	Overrides      string
	PruneOverrides bool
	// InitOverrides asks for a starter overrides file (Result.Starter).
	InitOverrides bool
	// Previous is the config this run replaces, when there is one: its
	// footer lists the spec's operations then, so new ones can be told.
	Previous []byte
}

// Result is a generated config and what to tell the user.
type Result struct {
	Config []byte
	// Summary is a count of what was generated.
	Summary string
	// Skipped is one line per operation not generated, with why.
	Skipped []string
	// Warnings are things that will likely go wrong at runtime.
	Warnings []string
	// Hints suggest what to do about a big result: --tag, with a count
	// per tag.
	Hints []string
	// Notes say how lists are fetched where it's worth knowing: those
	// that walk every page, and why some that looked windowed don't.
	Notes []string
	// Starter is a starter overrides file, with InitOverrides.
	Starter []byte
	// New and Gone are the spec's operations since the Previous config
	// was generated: added, and removed.
	New, Gone []string
}

// Register adds the flags that shape what's generated to f.
func (o *Options) Register(f *pflag.FlagSet) {
	f.StringArrayVar(&o.Select.Tags, "tag", nil, "only operations with this tag (repeatable; globs)")
	f.StringArrayVar(&o.Select.ExcludeTags, "exclude-tag", nil, "leave out operations with this tag (repeatable; globs)")
	f.StringArrayVar(&o.Select.Operations, "operation", nil, "only these operationIds (repeatable; globs like list_*)")
	f.StringArrayVar(&o.Select.ExcludeOperations, "exclude-operation", nil, "leave out these operationIds (repeatable; globs)")
	f.BoolVar(&o.Select.ReadOnly, "read-only", false, "generate nothing from POST, PUT, PATCH or DELETE")
	f.BoolVar(&o.Select.IncludeDeprecated, "include-deprecated", false, "keep operations marked deprecated")
	f.StringVar(&o.Doc.Name, "name", "", "the preset's name and env prefix (default from info.title)")
	f.StringVar(&o.Doc.BaseURL, "base-url", "", "the base URL's default, in place of the spec's servers")
	f.BoolVar(&o.Doc.PromptServer, "prompt-server", false, "ask which server at startup when the spec lists several")
	f.DurationVar(&o.Input.Timeout, "timeout", DefaultTimeout, "how long fetching a spec from a URL may take")
	f.StringVar(&o.Layout, "layout", "", "tabs or menu for the home screen (default by how many screens)")
	f.StringVar(&o.Overrides, "overrides", "", "a file of gen: hints and config merged into what's generated")
	f.BoolVar(&o.PruneOverrides, "prune-overrides", false, "warn, rather than fail, on overrides for operations the spec no longer has")
}

// ErrNothing is returned when no selected operation maps to a screen.
var ErrNothing = errors.New("nothing to generate")

// Generate reads the spec and writes the config.
func Generate(ctx context.Context, opts Options) (Result, error) {
	var ov *overrides
	if opts.Overrides != "" {
		var err error
		if ov, err = loadOverrides(opts.Overrides); err != nil {
			return Result{}, err
		}
		if err := ov.applyGlobals(&opts); err != nil {
			return Result{}, err
		}
	}
	if opts.Layout != "" && opts.Layout != "tabs" && opts.Layout != "menu" {
		return Result{}, fmt.Errorf("--layout %q: want tabs or menu", opts.Layout)
	}
	s, err := load(ctx, opts.Input)
	if err != nil {
		return Result{}, err
	}
	all := operations(s.doc)
	var pruned []string
	if ov != nil {
		if pruned, err = ov.resolve(all, opts.PruneOverrides); err != nil {
			return Result{}, err
		}
		s.ov = ov
	}
	var ops []op
	for _, o := range all {
		if opts.Select.selected(o) {
			ops = append(ops, o)
		}
	}
	c := &comments{}
	sourceNames := map[string]bool{}
	for _, o := range ops {
		if o.method == "GET" {
			sourceNames[pathName(o.path, strings.HasSuffix(o.path, "}"))] = true
		}
	}
	p, err := buildPreset(ctx, s, opts.Doc, ops, c, sourceNames)
	if err != nil {
		return Result{}, err
	}
	title := apiTitle(s.doc, p.name)
	var res Result
	var skips []skip
	var gets, writes []op
	for _, o := range ops {
		if s.ov.hint(o).Skip {
			s.hint(o)
			skips = append(skips, skip{o, "skip: in the overrides"})
			continue
		}
		if why := opts.Select.skipReason(o); why != "" {
			skips = append(skips, skip{o, why})
			continue
		}
		switch o.method {
		case "GET":
			gets = append(gets, o)
		case "POST", "PUT", "PATCH", "DELETE":
			writes = append(writes, o)
		}
	}
	reads, readSkips, err := classify(s, gets)
	if err != nil {
		return Result{}, err
	}
	skips = sortSkips(append(skips, readSkips...))
	nameReads(reads, map[string]bool{"home": true, "home_menu": true})
	var warnings []string
	screens, actions, buildSkips, err := build(ctx, s, p, reads, title, func(w string) { warnings = append(warnings, w) })
	if err != nil {
		return Result{}, err
	}
	skips = append(skips, buildSkips...)
	writeSkips, err := attachWrites(ctx, s, p, writes, screens, actions, title)
	if err != nil {
		return Result{}, err
	}
	skips = sortSkips(append(skips, writeSkips...))
	if ov != nil {
		if s.order, err = ov.resolveOrder(all); err != nil {
			return Result{}, err
		}
	}
	res.Warnings = append(append(p.warnings, warnings...), pruned...)
	if ov != nil {
		res.Warnings = append(res.Warnings, ov.unused()...)
	}
	narrowed := len(opts.Select.Tags) > 0 || len(opts.Select.Operations) > 0
	for _, name := range sortedKeys(s.doc.Webhooks) {
		if narrowed {
			break
		}
		skips = append(skips, skip{op{method: "webhook", path: name}, "the API calls it, not the other way round"})
	}
	for _, sk := range skips {
		res.Skipped = append(res.Skipped, sk.String())
	}
	if len(screens) == 0 {
		return res, fmt.Errorf("%w from %s: no selected operation maps to a screen (%d skipped)", ErrNothing, s.name, len(skips))
	}

	for _, sc := range screens {
		if len(sc.more) == 0 {
			continue
		}
		lines := []string{"More fields from the schema; uncomment one to add it:"}
		for _, col := range sc.more {
			lines = append(lines, "- "+flowLine(columnNode(col, columns.Style{}, sc.sortable, sc.onWind)))
		}
		c.inSeq(get(sc.comp, "columns"), lines...)
	}
	res.Hints = hints(screens, len(ops))
	walks := 0
	byNote := map[string][]string{}
	var noteOrder []string
	for _, sc := range screens {
		if sc.walks {
			walks++
		}
		if sc.note != "" {
			if byNote[sc.note] == nil {
				noteOrder = append(noteOrder, sc.note)
			}
			byNote[sc.note] = append(byNote[sc.note], sc.op.String())
		}
	}
	// A note many lists share is one line, naming a few of them.
	for _, n := range noteOrder {
		ops := byNote[n]
		if len(ops) <= 3 {
			for _, o := range ops {
				res.Notes = append(res.Notes, o+": "+n)
			}
			continue
		}
		res.Notes = append(res.Notes, fmt.Sprintf("%d lists (%s, %s, …): %s", len(ops), ops[0], ops[1], n))
	}
	if walks > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%s every page, up to 20 (raise max_pages in overrides)", plural(walks, "list walks", "lists walk")))
	}
	if opts.Previous != nil {
		if prev, ok := previousOps(opts.Previous); ok {
			res.New, res.Gone = diffOps(prev, all)
		}
	}
	s.all = all
	doc := document(s, opts, p, screens, actions, res.Skipped, title)
	if ov != nil && ov.fragment != nil {
		var touched [][]string
		if err := merge(doc, ov.fragment, nil, &touched); err != nil {
			return Result{}, fmt.Errorf("%s: %w", ov.path, err)
		}
		if err := conflicts(ov.path, s.hinted, touched); err != nil {
			return Result{}, err
		}
	}
	b, err := c.render(doc)
	if err != nil {
		return Result{}, err
	}
	b = append(b, footer(s.all)...)
	if err := checkLoads(b); err != nil {
		if ov != nil {
			return Result{}, fmt.Errorf("the config with the overrides from %s doesn't load: %w", ov.path, err)
		}
		return Result{}, fmt.Errorf("the generated config doesn't load, which is a bug in gen: %w", err)
	}
	if opts.InitOverrides {
		res.Starter = starter(s, opts, screens, actions)
	}
	res.Config = b
	res.Summary = fmt.Sprintf("%s, %s", count(len(screens), "screen"), count(len(screens), "source"))
	if n := countWrites(actions); n > 0 {
		res.Summary += ", " + count(n, "action")
	}
	if len(skips) > 0 {
		res.Summary += fmt.Sprintf("; skipped %s", count(len(skips), "operation"))
	}
	return res, nil
}

// sortSkips puts skips in the spec's order.
func sortSkips(skips []skip) []skip {
	slices.SortStableFunc(skips, func(a, b skip) int { return a.op.line - b.op.line })
	return skips
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// opServers are the servers an operation (or its path) sets for itself.
func opServers(o op) openapi3.Servers {
	if o.op.Servers != nil && len(*o.op.Servers) > 0 {
		return *o.op.Servers
	}
	return o.item.Servers
}

// sortByTag orders screens by their group: the spec's tags: order, then
// tags it doesn't list in the order they're met, then untagged last.
// Within a group they keep the spec's order.
func sortByTag(screens []*built, tags openapi3.Tags) {
	rank := map[string]int{}
	for i, t := range tags {
		if t != nil {
			rank[t.Name] = i
		}
	}
	for _, sc := range screens {
		if _, ok := rank[sc.group]; !ok && sc.group != "" {
			rank[sc.group] = len(rank) + len(tags)
		}
	}
	last := len(rank) + len(tags) + 1
	slices.SortStableFunc(screens, func(a, b *built) int {
		ra, rb := last, last
		if a.group != "" {
			ra = rank[a.group]
		}
		if b.group != "" {
			rb = rank[b.group]
		}
		return ra - rb
	})
}

// document assembles the config: header, app, data, actions and tui.
func document(s *spec, opts Options, p *preset, screens []*built, actions *yaml.Node, skipped []string, title string) *yaml.Node {
	app := mapping("title", str(title))
	if s.doc.Info != nil && s.doc.Info.Version != "" {
		add(app, "version", str(s.doc.Info.Version))
	}
	add(app, "env", seq(p.envs...))
	prompts := seq()
	if p.prompt != nil {
		prompts.Content = append(prompts.Content, p.prompt)
	}
	seen := map[string]bool{}
	for _, sc := range screens {
		for _, pr := range sc.prompts {
			if k := get(pr, "key").Value; !seen[k] {
				seen[k] = true
				prompts.Content = append(prompts.Content, pr)
			}
		}
	}
	if len(prompts.Content) > 0 {
		add(app, "prompts", prompts)
	}
	// Order the action menu's headings when a screen has both kinds.
	for _, sc := range screens {
		if sc.writes > 0 && len(sc.bindings) > sc.writes {
			add(app, "action_groups", flow(strs([]string{"read", "write"})))
			break
		}
	}

	sources := mapping()
	components := mapping()
	for _, sc := range screens {
		add(sources, sc.name, sc.source)
		add(components, sc.name, sc.comp)
	}
	data := mapping("presets", mapping(p.name, p.node), "sources", sources)
	doc := mapping("app", app, "data", data)
	if len(actions.Content) > 0 {
		add(doc, "actions", actions)
	}
	add(doc, "tui", navigation(screens, components, title, s.doc.Tags, opts.Layout, s.order))
	doc.HeadComment = header(s, opts, skipped)
	return doc
}

// navigation lays the screens out. Top-level screens (lists and
// singletons): one is the app; two to five are tabs on a home screen;
// six or more are a home menu grouped by tag. Details and sub-lists are
// screens of their own, reached by a push from their list.
// order is the overrides' gen.order: operations ("GET /things") first,
// in that order.
func navigation(screens []*built, components *yaml.Node, title string, tags openapi3.Tags, layout string, order []string) *yaml.Node {
	var top []*built
	for _, sc := range screens {
		if sc.topLevel() && !sc.hidden {
			top = append(top, sc)
		}
	}
	if len(order) > 0 {
		at := func(b *built) int {
			if i := slices.Index(order, b.op.String()); i >= 0 {
				return i
			}
			return len(order)
		}
		slices.SortStableFunc(top, func(a, b *built) int { return at(a) - at(b) })
	}
	uniqueLabels(top)
	all := mapping()
	tui := mapping("components", components)
	own := func(sc *built) *yaml.Node {
		m := mapping("title", str(sc.title), "layout", flow(mapping("component", str(sc.name))))
		if len(sc.bindings) > 0 {
			add(m, "actions", seq(sc.bindings...))
		}
		return m
	}
	asTabs := len(top) >= 2 && len(top) <= 5
	switch layout {
	case "tabs":
		asTabs = len(top) > 0
	case "menu":
		asTabs = false
	}
	switch {
	case len(top) == 0:
		// Only pushed screens, which nothing can reach: show the first.
		add(tui, "initial", str(screens[0].name))
	case len(top) == 1 && layout == "":
		add(all, top[0].name, own(top[0]))
		add(tui, "initial", str(top[0].name))
	case asTabs:
		tabs := seq()
		for _, sc := range top {
			tab := mapping("label", str(sc.label), "layout", flow(mapping("component", str(sc.name))))
			if len(sc.bindings) > 0 {
				add(tab, "actions", seq(sc.bindings...))
			}
			tabs.Content = append(tabs.Content, tab)
		}
		add(all, "home", mapping("title", str(title), "tabs", tabs))
		add(tui, "initial", str("home"))
	default:
		items := seq()
		grouped := slices.Clone(top)
		sortByTag(grouped, tags)
		described := map[string]string{}
		for _, t := range tags {
			if t != nil && strings.TrimSpace(t.Description) != "" {
				described[t.Name] = strings.TrimSpace(t.Description)
			}
		}
		lastGroup := "\x00"
		for _, sc := range grouped {
			item := mapping()
			if sc.group != "" {
				add(item, "group", str(sc.group))
			}
			add(item, "label", str(sc.label))
			add(item, "screen", str(sc.name))
			add(item, "description", str(sc.describe))
			flow(item)
			// A tag's description goes above its group's first item.
			if sc.group != lastGroup {
				if d := described[sc.group]; d != "" {
					item.HeadComment = sc.group + ": " + d
				}
				lastGroup = sc.group
			}
			items.Content = append(items.Content, item)
		}
		add(components, "home_menu", mapping("type", str("menu"), "title", str(title), "items", items))
		add(all, "home", mapping("title", str(title), "layout", flow(mapping("component", str("home_menu")))))
		for _, sc := range top {
			add(all, sc.name, own(sc))
		}
		add(tui, "initial", str("home"))
	}
	for _, sc := range screens {
		if !sc.topLevel() || sc.hidden {
			add(all, sc.name, own(sc))
		}
	}
	add(tui, "screens", all)
	return tui
}

// header is the comment at the top: where it came from, how to
// regenerate it, and what was skipped.
func header(s *spec, opts Options, skipped []string) string {
	from := s.name
	if s.doc.Info != nil && s.doc.Info.Title != "" {
		from += " (" + s.doc.Info.Title
		if s.doc.Info.Version != "" {
			from += " " + s.doc.Info.Version
		}
		from += ")"
	}
	edit := "Don't edit it: change the flags (or put changes in an --overrides file) and generate it again."
	if opts.Overrides != "" {
		edit = "Don't edit it: put changes in " + opts.Overrides + " and generate it again."
	}
	lines := []string{
		"Generated by `tui-builder gen` from " + from + ".",
		edit,
	}
	if opts.Command != "" {
		lines = append(lines, "  "+opts.Command)
	}
	if len(skipped) > 0 {
		lines = append(lines, "", "Not generated:")
		for _, sk := range skipped {
			lines = append(lines, "  "+strings.TrimPrefix(sk, "skipped "))
		}
	}
	return strings.Join(lines, "\n")
}

// footer is the comment at the end: every operation in the spec, which
// the next run compares with to report what's new. At the end, so a big
// spec's list doesn't push the config down the file.
func footer(ops []op) []byte {
	lines := append([]string{"", "# " + opsMarker}, wrapOps(ops)...)
	for i := 2; i < len(lines); i++ {
		lines[i] = "# " + lines[i]
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// opsMarker heads the footer's list of the spec's operations, which the
// next run compares with to report what's new.
const opsMarker = "Operations in the spec, to spot new ones when it's generated again:"

// wrapOps lists operations comma-separated, wrapped to fit a comment.
func wrapOps(ops []op) []string {
	var out []string
	line := ""
	for i, o := range ops {
		item := o.String()
		if i < len(ops)-1 {
			item += ","
		}
		if line != "" && len(line)+1+len(item) > 74 {
			out = append(out, "  "+line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += item
	}
	if line != "" {
		out = append(out, "  "+line)
	}
	return out
}

// previousOps reads the operations an earlier config's footer lists. ok
// is false for a config with no list (not one gen wrote, or an older
// one).
func previousOps(b []byte) ([]string, bool) {
	var out []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "#") {
			if in {
				break
			}
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		switch {
		case text == opsMarker:
			in = true
		case in && text == "":
			return out, true
		case in:
			for _, item := range strings.Split(text, ",") {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, item)
				}
			}
		}
	}
	return out, in
}

// diffOps are the operations added to and gone from the spec since prev.
func diffOps(prev []string, now []op) (added, gone []string) {
	had := map[string]bool{}
	for _, p := range prev {
		had[p] = true
	}
	has := map[string]bool{}
	for _, o := range now {
		has[o.String()] = true
		if !had[o.String()] {
			added = append(added, o.String())
		}
	}
	for _, p := range prev {
		if !has[p] {
			gone = append(gone, p)
		}
	}
	return added, gone
}

// checkLoads runs config.Load on b from a temporary file, standing in
// for the required env vars the user supplies at runtime.
func checkLoads(b []byte) error {
	var head struct {
		App struct {
			Env []config.EnvSpec `yaml:"env"`
		} `yaml:"app"`
	}
	if err := yaml.Unmarshal(b, &head); err == nil {
		for _, e := range head.App.Env {
			if _, set := os.LookupEnv(e.Name); e.Required && !set {
				os.Setenv(e.Name, "stand-in")
				defer os.Unsetenv(e.Name)
			}
		}
	}
	dir, err := os.MkdirTemp("", "tui-builder-gen-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	_, err = config.LoadTo(path, io.Discard)
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), path, "the config"))
	}
	return nil
}

// hints suggest --tag for a big result: past 40 top-level screens, with
// a count per tag, and past 200 operations, noting nothing was dropped.
func hints(screens []*built, ops int) []string {
	top := 0
	byTag := map[string]int{}
	for _, sc := range screens {
		if sc.topLevel() {
			top++
			tag := sc.group
			if tag == "" {
				tag = "(untagged)"
			}
			byTag[tag]++
		}
	}
	if top <= 40 && ops <= 200 {
		return nil
	}
	tags := sortedKeys(byTag)
	slices.SortStableFunc(tags, func(a, b string) int { return byTag[b] - byTag[a] })
	var counts []string
	for _, t := range tags {
		counts = append(counts, fmt.Sprintf("%s %d", t, byTag[t]))
	}
	out := []string{fmt.Sprintf("%d top-level screens; narrow them with --tag: %s", top, strings.Join(counts, ", "))}
	if len(byTag) == 1 && byTag["(untagged)"] > 0 {
		// No tags to narrow by: operationIds, then.
		out[0] = fmt.Sprintf("%d top-level screens, none tagged; narrow them with --operation globs (list_*)", top)
	}
	if ops > 200 {
		out = append(out, fmt.Sprintf("the spec has %d operations, and every one gen can show was generated", ops))
	}
	return out
}

// uniqueLabels keeps tab and menu labels apart: a label two screens
// share becomes each one's own name in words ("User issues"), and, if
// that clashes too, its method and path.
func uniqueLabels(top []*built) {
	count := map[string]int{}
	for _, sc := range top {
		count[sc.label]++
	}
	seen := map[string]bool{}
	for _, sc := range top {
		if count[sc.label] > 1 {
			sc.label = titled(sc.name)
		}
		if seen[sc.label] {
			sc.label += " (" + sc.describe + ")"
		}
		seen[sc.label] = true
		if sc.title != sc.label && sc.kind == kindList {
			sc.title = sc.label
			if t := get(sc.comp, "title"); t != nil {
				t.Value = sc.label
			}
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// countWrites counts the actions that aren't pushes.
func countWrites(actions *yaml.Node) int {
	n := 0
	for i := 1; i < len(actions.Content); i += 2 {
		if get(actions.Content[i], "push") == nil {
			n++
		}
	}
	return n
}
