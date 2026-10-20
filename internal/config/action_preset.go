package config

// http actions extending presets: a write takes its request — base url,
// headers, query, auth, timeout — from the same preset its reads do.
// Everything else on the preset (format, root, paginate, window,
// refresh, retry, …) is a source's business and ignored here, without
// error, since presets are shared with sources. Actions never retry: a
// POST or DELETE isn't safe to send twice.

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

// actionPresetFields are what an action takes from a preset, and what
// its unset: may name.
var actionPresetFields = []string{"auth", "headers", "query", "timeout", "url"}

// resolveActionPresets merges each action's preset into it. Runs after
// resolvePresets, so a preset's own chain is already folded in.
func resolveActionPresets(c *Config) error {
	names := make([]string, 0, len(c.Actions))
	for name := range c.Actions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := c.Actions[name]
		if a == nil {
			continue
		}
		path := "actions." + name
		if a.Extends == "" {
			if len(a.Unset) > 0 {
				return fmt.Errorf("%s.unset: only applies with extends:", path)
			}
			continue
		}
		p, ok := c.Data.Presets[a.Extends]
		if !ok {
			return unknownPreset(path, a.Extends, c.Data.Presets)
		}
		if t := a.Kind(); t != "http" {
			return fmt.Errorf("%s: extends: only applies to an http action (this is %s)", path, t)
		}
		if p.Type != "" && p.Type != "http" {
			return fmt.Errorf("%s: preset %q is type %s; an action can only extend an http preset", path, a.Extends, p.Type)
		}
		if err := mergeActionPreset(a, p, path); err != nil {
			return err
		}
	}
	return nil
}

func mergeActionPreset(a *Action, p *Source, path string) error {
	inherit := p.Clone()
	for _, f := range a.Unset {
		switch f {
		case "url":
			inherit.URL = ""
		case "headers":
			inherit.Headers = nil
		case "query":
			inherit.Query = nil
		case "auth":
			inherit.Auth = nil
		case "timeout":
			inherit.Timeout = ""
		default:
			return unknownField(path+".unset", f, actionPresetFields)
		}
	}

	// An action fills ${inputs.*}; nothing fills a preset's ${params.*}.
	var tmpls []string
	tmpls = append(tmpls, inherit.URL, inherit.Timeout)
	for _, v := range inherit.Headers {
		tmpls = append(tmpls, v)
	}
	for _, vs := range inherit.Query {
		tmpls = append(tmpls, vs...)
	}
	inherit.Auth.visit(func(_ string, v *string) { tmpls = append(tmpls, *v) })
	for _, t := range tmpls {
		if m := paramToken.FindStringSubmatch(t); m != nil {
			return fmt.Errorf("%s: preset %q uses ${params.%s}, which an action can't fill — declare it in the action's url or headers with ${inputs.%s}", path, a.Extends, m[1], m[1])
		}
	}

	a.Inherited = map[string]string{}
	from := func(field string) {
		origin := a.Extends
		if o, ok := p.Inherited[field]; ok {
			origin = o
		}
		a.Inherited[field] = origin
	}
	if inherit.URL != "" {
		if a.URL == "" {
			from("url")
		}
		a.URL = joinPresetURL(inherit.URL, a.URL)
	}
	if len(inherit.Headers) > 0 {
		merged := maps.Clone(inherit.Headers)
		for k, v := range a.Headers {
			if v == "" {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		a.Headers = merged
		from("headers")
	}
	if len(inherit.Query) > 0 {
		merged := cloneQuery(inherit.Query)
		for k, v := range a.Query {
			if v.empty() {
				delete(merged, k)
			} else {
				merged[k] = v
			}
		}
		a.Query = merged
		from("query")
	}
	if a.Auth == nil && inherit.Auth != nil {
		a.Auth = inherit.Auth
		from("auth")
	}
	if a.Timeout == "" && inherit.Timeout != "" {
		a.Timeout = inherit.Timeout
		from("timeout")
	}
	if a.Type == "" {
		a.Type = "http"
	}
	a.ExtendsChain = append([]string{a.Extends}, p.ExtendsChain...)
	a.Extends, a.Unset = "", nil
	return nil
}

// validateActionRequest checks an http action's auth: and query:, the
// way a source's are checked.
func validateActionRequest(a *Action, path string) error {
	if a.Auth != nil {
		s := &Source{Type: "http", Auth: a.Auth, Headers: a.Headers}
		if err := s.validateAuth(strings.TrimSuffix(path, ".auth")); err != nil {
			return err
		}
	}
	return nil
}
