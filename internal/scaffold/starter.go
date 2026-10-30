// Package scaffold writes config for the user: the starter `tui-builder
// init` prints.
package scaffold

import (
	_ "embed"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed starter.yaml
var starter string

// Starter is the config `tui-builder init` writes, titled title.
func Starter(title string) []byte {
	return []byte(strings.Replace(starter, "__TITLE__", yamlScalar(title), 1))
}

// yamlScalar is s as a YAML value on one line, quoted only when it must
// be ("My: app", "123", "yes").
func yamlScalar(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		// A string always marshals.
		panic(err)
	}
	return strings.TrimSuffix(string(b), "\n")
}
