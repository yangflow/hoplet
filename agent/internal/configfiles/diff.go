package configfiles

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines shown around a change.
const diffContext = 3

// maxDiffCells bounds the table the line comparison builds; past it the diff replaces every line that differs
// between the common start and the common end.
const maxDiffCells = 4_000_000

type diffOp struct {
	kind byte // ' ', '-' or '+'
	line string
}

// unifiedDiff is a unified diff of before and after (nil: the file does not exist) with the path in its header.
// Equal texts give an empty diff.
func unifiedDiff(rel string, before, after *string) string {
	oldText, newText := "", ""
	if before != nil {
		oldText = *before
	}
	if after != nil {
		newText = *after
	}
	if before != nil && after != nil && oldText == newText {
		return ""
	}
	oldName, newName := "a/"+rel, "b/"+rel
	if before == nil {
		oldName = "/dev/null"
	}
	if after == nil {
		newName = "/dev/null"
	}
	ops := diffLines(splitLines(oldText), splitLines(newText))
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	writeHunks(&b, ops)
	return b.String()
}

// splitLines cuts text into lines that keep their line end; the last one may have none.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func diffLines(a, b []string) []diffOp {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	var ops []diffOp
	for _, l := range a[:prefix] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, middle(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix])...)
	for _, l := range a[len(a)-suffix:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

// middle compares the part between the common start and end by their longest common subsequence.
func middle(a, b []string) []diffOp {
	n, m := len(a), len(b)
	var ops []diffOp
	if n == 0 || m == 0 || n*m > maxDiffCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// lcs[i][j] is the longest common subsequence of a[i:] and b[j:]; min(n, m) stays below 2000 here.
	w := m + 1
	lcs := make([]uint16, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// writeHunks writes the changed runs of ops with diffContext lines around each.
func writeHunks(b *strings.Builder, ops []diffOp) {
	for start := 0; start < len(ops); {
		first := start
		for first < len(ops) && ops[first].kind == ' ' {
			first++
		}
		if first == len(ops) {
			return
		}
		from := max(first-diffContext, 0)
		// The hunk ends after a run of more than twice the context of unchanged lines, or at the end.
		end := first
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*diffContext {
				end = min(end+diffContext, len(ops))
				break
			}
			end = run
		}
		oldStart, newStart := 1, 1
		for _, op := range ops[:from] {
			if op.kind != '+' {
				oldStart++
			}
			if op.kind != '-' {
				newStart++
			}
		}
		oldLen, newLen := 0, 0
		for _, op := range ops[from:end] {
			if op.kind != '+' {
				oldLen++
			}
			if op.kind != '-' {
				newLen++
			}
		}
		if oldLen == 0 {
			oldStart--
		}
		if newLen == 0 {
			newStart--
		}
		fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldLen, newStart, newLen)
		for _, op := range ops[from:end] {
			b.WriteByte(op.kind)
			b.WriteString(op.line)
			if !strings.HasSuffix(op.line, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
		start = end
	}
}
