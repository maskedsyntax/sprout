package buildfile

import (
	"fmt"
	"slices"
	"strings"
)

// DiffLine is one line of a unified diff.
type DiffLine struct {
	Op   byte // ' ', '+', '-' or '@' for hunk headers
	Text string
}

// Diff returns a unified diff of a -> b with the given context lines.
func Diff(a, b string, context int) []DiffLine {
	ops := lcsOps(splitLines(a), splitLines(b))
	var changed []int
	for i, o := range ops {
		if o.op != ' ' {
			changed = append(changed, i)
		}
	}
	var out []DiffLine
	for k := 0; k < len(changed); {
		first, last := changed[k], changed[k]
		k++
		for k < len(changed) && changed[k]-last-1 <= 2*context {
			last = changed[k]
			k++
		}
		start, end := max(0, first-context), min(len(ops), last+context+1)
		aLen, bLen := 0, 0
		for _, o := range ops[start:end] {
			if o.op != '+' {
				aLen++
			}
			if o.op != '-' {
				bLen++
			}
		}
		out = append(out, DiffLine{'@', fmt.Sprintf("@@ -%d,%d +%d,%d @@", ops[start].ai+1, aLen, ops[start].bi+1, bLen)})
		for _, o := range ops[start:end] {
			out = append(out, DiffLine{o.op, o.text})
		}
	}
	return out
}

type diffOp struct {
	op     byte
	text   string
	ai, bi int // line index in a and b at this point
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func lcsOps(a, b []string) []diffOp {
	// Trim common prefix/suffix to keep the table small.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	t := make([][]int, len(ma)+1)
	for i := range t {
		t[i] = make([]int, len(mb)+1)
	}
	for i, m := range slices.Backward(ma) {
		for j := len(mb) - 1; j >= 0; j-- {
			if m == mb[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var ops []diffOp
	for k := 0; k < pre; k++ {
		ops = append(ops, diffOp{' ', a[k], k, k})
	}
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		switch {
		case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
			ops = append(ops, diffOp{' ', ma[i], pre + i, pre + j})
			i++
			j++
		case i < len(ma) && (j == len(mb) || t[i+1][j] >= t[i][j+1]):
			ops = append(ops, diffOp{'-', ma[i], pre + i, pre + j})
			i++
		default:
			ops = append(ops, diffOp{'+', mb[j], pre + i, pre + j})
			j++
		}
	}
	for k := 0; k < suf; k++ {
		ops = append(ops, diffOp{' ', a[len(a)-suf+k], len(a) - suf + k, len(b) - suf + k})
	}
	return ops
}
