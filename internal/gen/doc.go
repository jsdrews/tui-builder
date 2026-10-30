package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// Document-level options.
type DocOptions struct {
	// Name is --name: the preset's name and the env prefix's source.
	Name string
	// BaseURL is --base-url: the base URL's default, overriding servers.
	BaseURL string
	// PromptServer is --prompt-server: ask which server at startup when
	// the spec lists several.
	PromptServer bool
	// Security is the overrides' gen.security: the scheme whose
	// alternative to use, in place of the first.
	Security string
}

// preset is the API's one preset and what it needs from the environment.
type preset struct {
	name   string
	env    string // THINGS_API
	node   *yaml.Node
	envs   []*yaml.Node // app.env entries
	prompt *yaml.Node   // app.prompts entry, with --prompt-server
	// auth is the preset's auth: block, nil when it has none; scheme is
	// its scheme's name, for comparing an operation's requirement.
	auth   *yaml.Node
	scheme string
	// requirement is the security requirement the preset satisfies.
	requirement openapi3.SecurityRequirement
	warnings    []string
	comments    *comments
}

var serverVar = regexp.MustCompile(`\{([^}]+)\}`)

// buildPreset maps the document level: info, servers and security.
// taken are the names screens' sources will have: a preset can't share
// one, so a title that would ("Jobs" and /jobs) gets an _api suffix.
func buildPreset(ctx context.Context, s *spec, opts DocOptions, ops []op, c *comments, taken map[string]bool) (*preset, error) {
	doc := s.doc
	name := opts.Name
	if name == "" {
		title := ""
		if doc.Info != nil {
			title = doc.Info.Title
		}
		name = snake(title)
		if taken[name] {
			name += "_api"
		}
	} else if taken[name] {
		return nil, fmt.Errorf("--name %s: a source will have that name too, and presets and sources share one namespace", name)
	}
	p := &preset{name: name, env: envName(name), comments: c}
	title := apiTitle(doc, name)

	base, comment, err := baseURL(s, opts)
	if err != nil {
		return nil, err
	}
	urlEnv := p.env + "_URL"
	p.node = mapping("type", str("http"))
	keyed(p.node, "url", str("${env."+urlEnv+"}"), comment)
	urlEntry := mapping("name", str(urlEnv), "default", str(base), "description", str("Base URL for "+title+"."))
	p.envs = append(p.envs, urlEntry)
	if opts.PromptServer && len(doc.Servers) > 1 {
		var choices []string
		for _, sv := range doc.Servers {
			if u, err := serverURL(s, sv); err == nil {
				choices = append(choices, u)
			}
		}
		if opts.BaseURL != "" && !slices.Contains(choices, opts.BaseURL) {
			choices = append([]string{opts.BaseURL}, choices...)
		}
		p.prompt = mapping("key", str(urlEnv), "label", str("Server"), "options", flow(strs(choices)), "default", str(base))
	}

	if err := p.security(ctx, s, ops, title, opts.Security); err != nil {
		return nil, err
	}
	if wantsRetry(ops) {
		keyed(p.node, "retry", flow(mapping()), "The API documents rate limits (429 or Retry-After).")
	}
	return p, nil
}

func apiTitle(doc *openapi3.T, name string) string {
	if doc.Info != nil && doc.Info.Title != "" {
		return doc.Info.Title
	}
	return titled(name)
}

// baseURL is the base URL's default and a comment for the preset's url
// line: other servers, or a variable's allowed values.
func baseURL(s *spec, opts DocOptions) (string, string, error) {
	doc := s.doc
	var notes []string
	if len(doc.Servers) > 1 {
		lines := []string{"Other servers in the spec:"}
		for _, sv := range doc.Servers[1:] {
			if u, err := serverURL(s, sv); err == nil {
				lines = append(lines, "  "+u+describe(sv.Description))
			}
		}
		if !opts.PromptServer {
			lines = append(lines, "(--prompt-server asks which at startup.)")
		}
		notes = append(notes, strings.Join(lines, "\n"))
	}
	if len(doc.Servers) > 0 {
		for _, name := range sortedKeys(doc.Servers[0].Variables) {
			if v := doc.Servers[0].Variables[name]; len(v.Enum) > 0 {
				notes = append(notes, fmt.Sprintf("{%s} is one of %s.", name, strings.Join(v.Enum, ", ")))
			}
		}
	}
	comment := strings.Join(notes, "\n")
	if opts.BaseURL != "" {
		return strings.TrimSuffix(opts.BaseURL, "/"), comment, nil
	}
	if len(doc.Servers) == 0 {
		if s.url != nil {
			return s.url.Scheme + "://" + s.url.Host, comment, nil
		}
		return "", "", fmt.Errorf("the spec lists no servers, so there's no base URL: pass --base-url")
	}
	u, err := serverURL(s, doc.Servers[0])
	return u, comment, err
}

func describe(d string) string {
	if d == "" {
		return ""
	}
	return " (" + d + ")"
}

// serverURL is a server's URL with its variables at their defaults,
// resolved against the spec's URL when relative.
func serverURL(s *spec, sv *openapi3.Server) (string, error) {
	u := serverVar.ReplaceAllStringFunc(sv.URL, func(m string) string {
		if v := sv.Variables[m[1:len(m)-1]]; v != nil {
			return v.Default
		}
		return m
	})
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("server %q: %w", sv.URL, err)
	}
	if parsed.IsAbs() {
		return strings.TrimSuffix(u, "/"), nil
	}
	if s.url == nil {
		return "", fmt.Errorf("the server URL %q is relative, and the spec wasn't fetched from a URL to resolve it against: pass --base-url", sv.URL)
	}
	return strings.TrimSuffix(s.url.ResolveReference(parsed).String(), "/"), nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// security works out the preset's auth: the document's requirement, else
// the first operation's. Of alternatives the first is used; the first
// scheme of a requirement goes in auth:, any others into headers or the
// query.
func (p *preset) security(ctx context.Context, s *spec, ops []op, title, pick string) error {
	doc := s.doc
	reqs := doc.Security
	if len(reqs) == 0 {
		for _, o := range ops {
			if o.op.Security != nil && len(*o.op.Security) > 0 {
				reqs = *o.op.Security
				break
			}
		}
	}
	if len(reqs) == 0 {
		if pick != "" {
			return fmt.Errorf("gen.security: %s: the spec has no security requirement to pick from", pick)
		}
		return nil
	}
	if pick != "" {
		i := slices.IndexFunc(reqs, func(r openapi3.SecurityRequirement) bool { _, ok := r[pick]; return ok })
		if i < 0 {
			var names []string
			for _, r := range reqs {
				names = append(names, sortedKeys(r)...)
			}
			return fmt.Errorf("gen.security: no alternative uses %q%s", pick, suggest(pick, names))
		}
		reqs = append(openapi3.SecurityRequirements{reqs[i]}, slices.Delete(slices.Clone(reqs), i, i+1)...)
	}
	var alts []string
	for _, r := range reqs[1:] {
		alts = append(alts, strings.Join(sortedKeys(r), " + "))
	}
	p.requirement = reqs[0]
	names := sortedKeys(reqs[0])
	if len(names) == 0 {
		return nil // {} : optional auth
	}
	var schemes = map[string]*openapi3.SecurityScheme{}
	if doc.Components != nil {
		for n, ref := range doc.Components.SecuritySchemes {
			if ref != nil && ref.Value != nil {
				schemes[n] = ref.Value
			}
		}
	}
	for i, n := range names {
		sch := schemes[n]
		if sch == nil {
			return fmt.Errorf("security names the scheme %q, which components.securitySchemes doesn't define", n)
		}
		// With several schemes at once, each var is named for its scheme.
		envBase, what := p.env, title
		if len(names) > 1 {
			envBase, what = p.env+"_"+envName(snake(n)), title+" ("+n+")"
		}
		if i == 0 {
			auth, comment, err := p.authFor(ctx, s, n, sch, reqs[0][n], envBase, what, "")
			if err != nil {
				return err
			}
			if auth == nil {
				continue
			}
			if len(alts) > 0 {
				comment = strings.TrimSpace(comment + "\nThe API also accepts: " + strings.Join(alts, "; ") + ".")
			}
			p.auth, p.scheme = auth, n
			keyed(p.node, "auth", auth, comment)
			continue
		}
		p.extra(n, sch, envBase, what)
	}
	return nil
}

// authFor maps one scheme to an auth: block, adding the app.env entries
// it reads. nil when there's no block to write (mutualTLS, unknown).
// where is the operation it's for, or "" for the whole API.
func (p *preset) authFor(ctx context.Context, s *spec, name string, sch *openapi3.SecurityScheme, scopes []string, env, title, where string) (*yaml.Node, string, error) {
	switch {
	case sch.Type == "http" && strings.EqualFold(sch.Scheme, "bearer"):
		p.secret(env+"_TOKEN", described(sch, "Bearer token for "+title+"."))
		return flow(mapping("bearer", str("${env."+env+"_TOKEN}"))), "", nil
	case sch.Type == "http" && strings.EqualFold(sch.Scheme, "basic"):
		p.secret(env+"_USER", "User name for "+title+".")
		p.secret(env+"_PASSWORD", described(sch, "Password for "+title+"."))
		return flow(mapping("basic", flow(mapping("user", str("${env."+env+"_USER}"), "pass", str("${env."+env+"_PASSWORD}"))))), "", nil
	case sch.Type == "apiKey":
		if sch.In != "header" && sch.In != "query" && sch.In != "cookie" {
			return nil, "", fmt.Errorf("scheme %q: an API key in %q, which isn't header, query or cookie", name, sch.In)
		}
		p.secret(env+"_API_KEY", described(sch, "API key for "+title+"."))
		return flow(mapping("api_key", flow(mapping("in", str(sch.In), "name", str(sch.Name), "value", str("${env."+env+"_API_KEY}"))))), "", nil
	case sch.Type == "oauth2" && sch.Flows != nil && sch.Flows.ClientCredentials != nil:
		return p.clientCredentials(sch.Flows.ClientCredentials.TokenURL, scopes, env, title, sch), "", nil
	case sch.Type == "openIdConnect":
		if s.url != nil && sch.OpenIdConnectUrl != "" {
			if tokenURL, err := discoverTokenURL(ctx, s, sch.OpenIdConnectUrl); err == nil {
				return p.clientCredentials(tokenURL, scopes, env, title, sch), "From the OpenID Connect discovery document at " + sch.OpenIdConnectUrl + ".", nil
			} else {
				p.warnings = append(p.warnings, fmt.Sprintf("couldn't read the OpenID Connect discovery document (%v), so the token comes from the environment", err))
			}
		}
		p.secret(env+"_TOKEN", described(sch, "Bearer token for "+title+"."))
		return flow(mapping("bearer", str("${env."+env+"_TOKEN}"))), "This API signs in with OpenID Connect. Get a token another way (a CLI, a password manager)\nand set from: on " + env + "_TOKEN.", nil
	case sch.Type == "oauth2":
		how := "OAuth2"
		if f := sch.Flows; f != nil {
			switch {
			case f.AuthorizationCode != nil:
				how = "OAuth2 authorization code"
			case f.Implicit != nil:
				how = "OAuth2 implicit"
			case f.Password != nil:
				how = "OAuth2 password"
			}
		}
		p.secret(env+"_TOKEN", described(sch, "Bearer token for "+title+"."))
		return flow(mapping("bearer", str("${env."+env+"_TOKEN}"))), "This API signs in with " + how + ", which tui-builder can't do itself. Get a token\nanother way (a CLI, a password manager) and set from: on " + env + "_TOKEN.", nil
	}
	what := sch.Type
	if sch.Type == "http" {
		what = "http " + sch.Scheme
	}
	if where != "" {
		p.warnings = append(p.warnings, fmt.Sprintf("%s uses the %s scheme %q, which tui-builder can't send, so it will likely fail with 401", where, what, name))
		return nil, fmt.Sprintf("This operation uses the %s scheme %q, which tui-builder can't send.", what, name), nil
	}
	p.warnings = append(p.warnings, fmt.Sprintf("the %s scheme %q isn't one tui-builder can send, so requests go without auth and will likely fail with 401", what, name))
	return nil, "", nil
}

func (p *preset) clientCredentials(tokenURL string, scopes []string, env, title string, sch *openapi3.SecurityScheme) *yaml.Node {
	p.secret(env+"_CLIENT_ID", "OAuth2 client ID for "+title+".")
	p.secret(env+"_CLIENT_SECRET", described(sch, "OAuth2 client secret for "+title+"."))
	o := mapping("token_url", str(tokenURL), "client_id", str("${env."+env+"_CLIENT_ID}"), "client_secret", str("${env."+env+"_CLIENT_SECRET}"))
	if len(scopes) > 0 {
		add(o, "scopes", flow(strs(scopes)))
	}
	return mapping("oauth2", o)
}

// extra sends a second scheme of the same requirement as a header or
// query parameter.
func (p *preset) extra(name string, sch *openapi3.SecurityScheme, env, title string) {
	switch {
	case sch.Type == "apiKey" && sch.In == "query":
		p.secret(env+"_API_KEY", described(sch, "API key for "+title+"."))
		m := get(p.node, "query")
		if m == nil {
			m = flow(mapping())
			add(p.node, "query", m)
		}
		add(m, sch.Name, str("${env."+env+"_API_KEY}"))
	case sch.Type == "apiKey" && sch.In == "header":
		p.secret(env+"_API_KEY", described(sch, "API key for "+title+"."))
		p.header(sch.Name, "${env."+env+"_API_KEY}")
	case sch.Type == "http" && strings.EqualFold(sch.Scheme, "bearer"):
		p.secret(env+"_TOKEN", described(sch, "Bearer token for "+title+"."))
		p.header("Authorization", "Bearer ${env."+env+"_TOKEN}")
	default:
		p.warnings = append(p.warnings, fmt.Sprintf("the scheme %q is required alongside another, and gen can only send API keys and bearer tokens that way", name))
	}
}

func (p *preset) header(name, value string) {
	m := get(p.node, "headers")
	if m == nil {
		m = flow(mapping())
		add(p.node, "headers", m)
	}
	add(m, name, str(value))
}

// secret declares a required env var with commented from: examples.
func (p *preset) secret(name, description string) {
	for _, e := range p.envs {
		if get(e, "name").Value == name {
			return
		}
	}
	entry := mapping("name", str(name), "required", boolean(true), "description", str(description))
	store := kebab(p.name) + "/" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(name, p.env), "_"))
	p.comments.in(entry,
		"from: {command: [pass, show, "+store+"]}",
		"from: {file: ~/.config/"+store+"}",
	)
	p.envs = append(p.envs, entry)
}

func described(sch *openapi3.SecurityScheme, fallback string) string {
	if d := strings.TrimSpace(sch.Description); d != "" {
		return strings.SplitN(d, "\n", 2)[0]
	}
	return fallback
}

// get is a mapping's value for k, or nil.
func get(m *yaml.Node, k string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return m.Content[i+1]
		}
	}
	return nil
}

// discoverTokenURL reads an OpenID Connect discovery document's token
// endpoint.
func discoverTokenURL(ctx context.Context, s *spec, u string) (string, error) {
	b, err := s.fetch(ctx, u)
	if err != nil {
		return "", err
	}
	var d struct {
		TokenEndpoint string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(b, &d); err != nil || d.TokenEndpoint == "" {
		return "", fmt.Errorf("no token_endpoint in %s", u)
	}
	return d.TokenEndpoint, nil
}

// wantsRetry is true when an operation documents a 429 or rate-limit
// headers.
func wantsRetry(ops []op) bool {
	for _, o := range ops {
		if o.op.Responses == nil {
			continue
		}
		for code, r := range o.op.Responses.Map() {
			if code == "429" {
				return true
			}
			if r == nil || r.Value == nil {
				continue
			}
			for h := range r.Value.Headers {
				lh := strings.ToLower(h)
				if lh == "retry-after" || strings.HasPrefix(lh, "x-ratelimit-") || strings.HasPrefix(lh, "ratelimit-") {
					return true
				}
			}
		}
	}
	return false
}

// opAuth is how an operation's own security differs from the preset's:
// unset when it has none, a block of its own when another scheme.
// comment is set, with no auth, for a scheme that can't be sent.
func (p *preset) opAuth(ctx context.Context, s *spec, o op, title string) (unset bool, auth *yaml.Node, comment string, err error) {
	if o.op.Security == nil {
		return false, nil, "", nil
	}
	reqs := *o.op.Security
	if len(reqs) == 0 || (len(reqs) == 1 && len(reqs[0]) == 0) {
		return p.auth != nil, nil, "", nil
	}
	for _, r := range reqs {
		if _, ok := r[p.scheme]; ok && p.scheme != "" {
			return false, nil, "", nil
		}
	}
	names := sortedKeys(reqs[0])
	sch := s.doc.Components.SecuritySchemes[names[0]]
	if sch == nil || sch.Value == nil {
		return false, nil, "", fmt.Errorf("%s: security names the scheme %q, which isn't defined", o, names[0])
	}
	auth, comment, err = p.authFor(ctx, s, names[0], sch.Value, reqs[0][names[0]], p.env+"_"+envName(snake(names[0])), title, o.String())
	return false, auth, comment, err
}
