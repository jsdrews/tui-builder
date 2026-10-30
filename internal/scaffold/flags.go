package scaffold

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Param is one --param: an argv value made into a source parameter.
type Param struct {
	Name  string
	Value string
	// Type is string (the default), int, bool or duration.
	Type string
}

var paramName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// paramTypes are the types a --param can declare.
var paramTypes = []string{"string", "int", "bool", "duration"}

// ParseParam reads NAME=VALUE[:TYPE]. A trailing :TYPE counts only when
// it's one of the parameter types, so a value like a URL keeps its
// colons.
func ParseParam(s string) (Param, error) {
	name, value, ok := strings.Cut(s, "=")
	if !ok || value == "" {
		return Param{}, fmt.Errorf("--param %s: want NAME=VALUE[:TYPE]", s)
	}
	if !paramName.MatchString(name) {
		return Param{}, fmt.Errorf("--param %s: %q isn't a parameter name (letters, digits and _)", s, name)
	}
	p := Param{Name: name, Value: value, Type: "string"}
	if i := strings.LastIndex(value, ":"); i >= 0 && slices.Contains(paramTypes, value[i+1:]) {
		p.Value, p.Type = value[:i], value[i+1:]
	}
	if p.Value == "" {
		return Param{}, fmt.Errorf("--param %s: the value is empty", s)
	}
	if p.Type == "bool" && p.Value != "true" && p.Value != "false" {
		return Param{}, fmt.Errorf("--param %s: a bool is true or false", s)
	}
	return p, nil
}

// templateArgv swaps each --param's value in argv for ${params.NAME}:
// the one argument equal to it, else the one argument containing it
// once (--namespace=default). None, or more than one, is an error.
func templateArgv(argv []string, params []Param) ([]string, error) {
	return templateWith(argv, params, "--param", func(name string) string { return "${params." + name + "}" })
}

// templateWith is templateArgv with the flag named in errors and the
// token each value becomes.
func templateWith(argv []string, params []Param, flag string, token func(string) string) ([]string, error) {
	out := slices.Clone(argv)
	seen := map[string]bool{}
	for _, p := range params {
		if seen[p.Name] {
			return nil, fmt.Errorf("%s %s is given twice", flag, p.Name)
		}
		seen[p.Name] = true
		token := token(p.Name)
		var exact, inside []int
		for i := 1; i < len(out); i++ {
			switch {
			case out[i] == p.Value:
				exact = append(exact, i)
			case strings.Count(out[i], p.Value) == 1:
				inside = append(inside, i)
			case strings.Contains(out[i], p.Value):
				inside = append(inside, i, i) // twice in one argument: ambiguous
			}
		}
		switch {
		case len(exact) == 1:
			out[exact[0]] = token
		case len(exact) > 1:
			return nil, fmt.Errorf("%s %s=%s: the command has %q %d times; it must appear once", flag, p.Name, p.Value, p.Value, len(exact))
		case len(inside) == 1:
			out[inside[0]] = strings.Replace(out[inside[0]], p.Value, token, 1)
		case len(inside) > 1:
			return nil, fmt.Errorf("%s %s=%s: %q appears more than once in the command; it must appear once", flag, p.Name, p.Value, p.Value)
		default:
			return nil, fmt.Errorf("%s %s=%s: the command has no %q to mark", flag, p.Name, p.Value, p.Value)
		}
	}
	return out, nil
}

// ColumnSpec is one --columns entry: a dot-path and an optional title.
type ColumnSpec struct {
	Path  string
	Title string
}

// ParseColumns reads path[=Title],… .
func ParseColumns(s string) ([]ColumnSpec, error) {
	var out []ColumnSpec
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		path, title, _ := strings.Cut(strings.TrimSpace(part), "=")
		path, title = strings.TrimSpace(path), strings.TrimSpace(title)
		if path == "" {
			return nil, fmt.Errorf("--columns %s: an entry has no path", s)
		}
		if seen[path] {
			return nil, fmt.Errorf("--columns: %s is listed twice", path)
		}
		seen[path] = true
		out = append(out, ColumnSpec{Path: path, Title: title})
	}
	return out, nil
}
