package gen

import (
	"strings"
	"unicode"
)

// snake turns a title or path into a snake_case name: "Things API" is
// things_api, "/users/me" is users_me.
func snake(s string) string {
	var b strings.Builder
	under := false
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// camelCase splits: userName → user_name.
			if unicode.IsUpper(r) && i > 0 && unicode.IsLower(rs[i-1]) {
				under = true
			}
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(unicode.ToLower(r))
		default:
			under = true
		}
	}
	out := b.String()
	if out == "" {
		return "api"
	}
	if unicode.IsDigit(rune(out[0])) {
		out = "x" + out
	}
	return out
}

// envName is a name as an environment variable prefix: THINGS_API.
func envName(name string) string { return strings.ToUpper(name) }

// kebab is a name for a file path or a password-store entry: things-api.
func kebab(name string) string { return strings.ReplaceAll(name, "_", "-") }

// titled turns a snake name into words: users_me → "Users me".
func titled(name string) string {
	s := strings.ReplaceAll(name, "_", " ")
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}
