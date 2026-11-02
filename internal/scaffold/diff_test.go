package scaffold

import (
	"fmt"
	"strings"
)

// UnifiedDiff is a unified diff of two texts, with three lines of
// context, labelled with name. "" when they're the same.
func UnifiedDiff(name string, a, b []byte) string {
	x, y := splitLines(string(a)), splitLines(string(b))
	ops := diffOps(x, y)
	const context = 3
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", name, name)
	changed := false
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		changed = true
		// A hunk: from context lines before this change to context
		// lines after the last change within 2*context of the next.
		start := max(i-context, 0)
		end := i
		for j := i; j < len(ops); j++ {
			if ops[j].kind != ' ' {
				end = j
			} else if j-end > 2*context {
				break
			}
		}
		end = min(end+context+1, len(ops))
		aStart, bStart, aLen, bLen := ops[start].ai, ops[start].bi, 0, 0
		var body strings.Builder
		for _, op := range ops[start:end] {
			body.WriteByte(op.kind)
			body.WriteString(op.line)
			body.WriteByte('\n')
			if op.kind != '+' {
				aLen++
			}
			if op.kind != '-' {
				bLen++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n%s", hunkRange(aStart, aLen), hunkRange(bStart, bLen), body.String())
		i = end
	}
	if !changed {
		return ""
	}
	return out.String()
}

func hunkRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type diffOp struct {
	kind   byte // ' ', '-' or '+'
	line   string
	ai, bi int // positions in a and b before this line
}

// diffOps is a line diff by longest common subsequence: plenty for a
// config file, where only a few lines change.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i], i, j})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j], i, j})
			j++
		}
	}
	return ops
}
