package config

// Locators: where a response-level value is read from. A dot-path into
// the raw body, or header:NAME, link:REL, or last:PATH (a dot-path into
// the last item after root:). See plans/http-source.md §4.

import (
	"fmt"
	"strings"
)

var locatorPrefixes = []string{"header:", "link:", "last:"}

// locatorPrefix returns the locator prefix loc starts with, or "" for a
// plain body path.
func locatorPrefix(loc string) string {
	for _, p := range locatorPrefixes {
		if strings.HasPrefix(loc, p) {
			return p
		}
	}
	return ""
}

// validateLocator checks a response-level field: any locator, each with
// something after its prefix.
func validateLocator(path, loc string) error {
	if p := locatorPrefix(loc); p != "" && strings.TrimSpace(strings.TrimPrefix(loc, p)) == "" {
		return fmt.Errorf("%s: %q names nothing after %s", path, loc, p)
	}
	return nil
}

// validateItemPath checks an item-level field, which reads a value out
// of one item: a body dot-path only.
func validateItemPath(path, p string) error {
	if pre := locatorPrefix(p); pre != "" {
		return fmt.Errorf("%s: %s locators only work on response-level fields (total_path, next_path, error_path) — this field reads a path inside each item", path, pre)
	}
	return nil
}

// validateResponse checks the response fields of an http source:
// empty_on and error_path.
func (s *Source) validateResponse(path string) error {
	if s.Type != "http" {
		for field, set := range map[string]bool{"empty_on": len(s.EmptyOn) > 0, "error_path": s.ErrorPath != ""} {
			if set {
				return fmt.Errorf("%s.%s: only applies to type: http", path, field)
			}
		}
		return nil
	}
	for i, code := range s.EmptyOn {
		if code < 100 || code > 599 {
			return fmt.Errorf("%s.empty_on[%d]: %d isn't an HTTP status", path, i, code)
		}
		if code/100 == 2 {
			return fmt.Errorf("%s.empty_on[%d]: %d is a success — 204 and empty bodies are already the empty result", path, i, code)
		}
	}
	if err := validateLocator(path+".error_path", s.ErrorPath); err != nil {
		return err
	}
	if s.Paginate != nil {
		if err := validateLocator(path+".paginate.next_path", s.Paginate.NextPath); err != nil {
			return err
		}
	}
	if w := s.Window; w != nil {
		if err := validateLocator(path+".window.total_path", w.TotalPath); err != nil {
			return err
		}
		if err := validateItemPath(path+".window.cursor", w.Cursor); err != nil {
			return err
		}
		if w.Find != nil {
			if err := validateItemPath(path+".window.find.position", w.Find.Position); err != nil {
				return err
			}
		}
	}
	return nil
}
