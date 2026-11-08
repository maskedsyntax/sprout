package buildfile

import (
	"fmt"
	"regexp"
	"strings"
)

// Blocks that are merged statement by statement, by parent context.
var gradleContainers = map[string]map[string]bool{
	"root":                 {"plugins": true, "dependencies": true, "repositories": true, "ext": true, "dependencyManagement": true},
	"dependencyManagement": {"imports": true},
}

type gradleParser struct {
	src string
}

func parseGradle(kind Kind, src string) (*Doc, error) {
	p := &gradleParser{src: src}
	children, err := p.block(0, len(src), "root")
	if err != nil {
		return nil, err
	}
	root := &Node{Key: "root", Start: 0, End: len(src), CloseStart: len(src), Container: true, Children: children}
	return &Doc{Kind: kind, Src: src, Root: root}, nil
}

// syntaxError reports a position as line:column.
func (p *gradleParser) syntaxError(off int, msg string) error {
	line := strings.Count(p.src[:off], "\n") + 1
	col := off - lineStart(p.src, off) + 1
	return fmt.Errorf("cannot parse build file at line %d, column %d: %s", line, col, msg)
}

// block parses the statements between from and to.
func (p *gradleParser) block(from, to int, ctx string) ([]*Node, error) {
	var nodes []*Node
	i := from
	for {
		var err error
		if i, err = p.skipSpace(i, to); err != nil {
			return nil, err
		}
		if i >= to {
			return nodes, nil
		}
		end, open, closeAt, err := p.statement(i, to)
		if err != nil {
			return nil, err
		}
		n := &Node{Start: i, End: end}
		text := p.src[i:end]
		if open >= 0 {
			head := strings.Join(strings.Fields(p.src[i:open]), "")
			if gradleContainers[ctx][head] {
				n.Container = true
				n.CloseStart = closeAt
				n.Key = "block:" + head
				if n.Children, err = p.block(open+1, closeAt, head); err != nil {
					return nil, err
				}
				nodes = append(nodes, n)
				i = end
				continue
			}
		}
		n.Key, n.KeyOnly = gradleKey(ctx, text, open >= 0, p.src[i:max(open, i)])
		nodes = append(nodes, n)
		i = end
	}
}

// skipSpace skips whitespace, semicolons and comments.
func (p *gradleParser) skipSpace(i, to int) (int, error) {
	for i < to {
		switch {
		case p.src[i] == ' ' || p.src[i] == '\t' || p.src[i] == '\n' || p.src[i] == '\r' || p.src[i] == ';':
			i++
		case strings.HasPrefix(p.src[i:], "//"):
			i = lineEnd(p.src, i)
		case strings.HasPrefix(p.src[i:], "/*"):
			j := strings.Index(p.src[i+2:], "*/")
			if j < 0 {
				return 0, p.syntaxError(i, "unterminated comment")
			}
			i += j + 4
		default:
			return i, nil
		}
	}
	return i, nil
}

// statement scans one statement starting at i. It returns the end offset and,
// when the statement contains a top-level block, the offsets of its first
// opening brace and the matching closing brace.
func (p *gradleParser) statement(i, to int) (end, open, closeAt int, err error) {
	open, closeAt = -1, -1
	depth := 0
	j := i
	for j < to {
		c := p.src[j]
		switch {
		case c == '"' || c == '\'':
			if j, err = p.skipString(j); err != nil {
				return
			}
			continue
		case strings.HasPrefix(p.src[j:], "//"):
			j = lineEnd(p.src, j)
			continue
		case strings.HasPrefix(p.src[j:], "/*"):
			k := strings.Index(p.src[j+2:], "*/")
			if k < 0 {
				err = p.syntaxError(j, "unterminated comment")
				return
			}
			j += k + 4
			continue
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == '{':
			k, e := p.matchBrace(j)
			if e != nil {
				err = e
				return
			}
			if depth == 0 && open < 0 {
				open, closeAt = j, k
			}
			j = k + 1
			continue
		case c == ';' && depth == 0:
			return j, open, closeAt, nil
		case c == '\n' && depth == 0 && !p.continues(i, j):
			return i + len(strings.TrimRight(p.src[i:j], " \t\r")), open, closeAt, nil
		}
		j++
	}
	if depth != 0 {
		err = p.syntaxError(i, "unbalanced parentheses")
		return
	}
	return i + len(strings.TrimRight(p.src[i:to], " \t\r\n")), open, closeAt, nil
}

// continues reports whether the statement started at i continues past the
// newline at j (trailing operator, or the next line starts with '.' or '?.').
func (p *gradleParser) continues(i, j int) bool {
	line := strings.TrimRight(p.src[lineStart(p.src, j-1):j], " \t\r")
	if k := strings.Index(line, "//"); k >= 0 && !strings.ContainsAny(line[:k], `"'`) {
		line = strings.TrimRight(line[:k], " \t")
	}
	if line != "" && strings.ContainsRune(",.=+-*/&|?:(", rune(line[len(line)-1])) && !strings.HasSuffix(line, "++") && !strings.HasSuffix(line, "--") {
		return true
	}
	k := j + 1
	for k < len(p.src) && (p.src[k] == ' ' || p.src[k] == '\t' || p.src[k] == '\r' || p.src[k] == '\n') {
		k++
	}
	return k < len(p.src) && (p.src[k] == '.' && !strings.HasPrefix(p.src[k:], "..") || strings.HasPrefix(p.src[k:], "?."))
}

// matchBrace returns the offset of the '}' matching the '{' at i.
func (p *gradleParser) matchBrace(i int) (int, error) {
	depth := 0
	for j := i; j < len(p.src); {
		c := p.src[j]
		switch {
		case c == '"' || c == '\'':
			k, err := p.skipString(j)
			if err != nil {
				return 0, err
			}
			j = k
			continue
		case strings.HasPrefix(p.src[j:], "//"):
			j = lineEnd(p.src, j)
			continue
		case strings.HasPrefix(p.src[j:], "/*"):
			k := strings.Index(p.src[j+2:], "*/")
			if k < 0 {
				return 0, p.syntaxError(j, "unterminated comment")
			}
			j += k + 4
			continue
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return j, nil
			}
		}
		j++
	}
	return 0, p.syntaxError(i, "unmatched '{'")
}

// skipString returns the offset just past the string literal starting at i.
// Double-quoted strings may contain ${...} templates with nested strings.
func (p *gradleParser) skipString(i int) (int, error) {
	q := p.src[i]
	triple := strings.Repeat(string(q), 3)
	if strings.HasPrefix(p.src[i:], triple) {
		j := strings.Index(p.src[i+3:], triple)
		if j < 0 {
			return 0, p.syntaxError(i, "unterminated string")
		}
		return i + 3 + j + 3, nil
	}
	for j := i + 1; j < len(p.src); j++ {
		switch c := p.src[j]; {
		case c == '\\':
			j++
		case c == q:
			return j + 1, nil
		case c == '\n':
			return 0, p.syntaxError(i, "unterminated string")
		case q == '"' && c == '$' && j+1 < len(p.src) && p.src[j+1] == '{':
			k, err := p.matchBrace(j + 1)
			if err != nil {
				return 0, err
			}
			j = k
		}
	}
	return 0, p.syntaxError(i, "unterminated string")
}

var (
	pluginIDRE     = regexp.MustCompile(`\bid\s*\(?\s*["']([^"']+)["']`)
	kotlinPluginRE = regexp.MustCompile(`^kotlin\s*\(\s*"([^"]+)"`)
	bareWordRE     = regexp.MustCompile(`^([A-Za-z_][\w-]*)\s*$`)
	configRE       = regexp.MustCompile(`^([A-Za-z_]\w*)`)
	coordRE        = regexp.MustCompile(`["']([^"':\s$]+):([^"':\s$]+)`)
	groupNameRE    = regexp.MustCompile(`group\s*[:=]\s*["']([^"']+)["']\s*,\s*name\s*[:=]\s*["']([^"']+)["']`)
	repoURLRE      = regexp.MustCompile(`url\s*[=(]?\s*(?:uri\s*\(\s*)?["']([^"']+)["']`)
	setPropRE      = regexp.MustCompile(`^set\s*\(\s*["']([^"']+)["']`)
	assignRE       = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s*=[^=]`)
	extraRE        = regexp.MustCompile(`^extra\s*\[\s*"([^"]+)"\s*\]\s*=`)
	valByExtraRE   = regexp.MustCompile(`^val\s+(\w+)\s+by\s+extra`)
)

// gradleKey derives a stable key for a statement in a given block context.
func gradleKey(ctx, text string, isBlock bool, head string) (string, bool) {
	norm := strings.ReplaceAll(strings.Join(strings.Fields(text), ""), "'", `"`)
	switch ctx {
	case "plugins":
		if m := pluginIDRE.FindStringSubmatch(text); m != nil {
			return "plugin:" + m[1], true
		}
		if m := kotlinPluginRE.FindStringSubmatch(text); m != nil {
			return "plugin:org.jetbrains.kotlin." + m[1], true
		}
		if m := bareWordRE.FindStringSubmatch(text); m != nil {
			return "plugin:" + m[1], true
		}
	case "dependencies":
		config := ""
		if m := configRE.FindStringSubmatch(text); m != nil {
			config = m[1]
		}
		if m := coordRE.FindStringSubmatch(text); m != nil {
			return "dep:" + config + " " + m[1] + ":" + m[2], true
		}
		if m := groupNameRE.FindStringSubmatch(text); m != nil {
			return "dep:" + config + " " + m[1] + ":" + m[2], true
		}
	case "repositories":
		if m := repoURLRE.FindStringSubmatch(text); m != nil {
			return "repo:" + m[1], true
		}
	case "ext":
		if m := setPropRE.FindStringSubmatch(text); m != nil {
			return "prop:" + m[1], true
		}
		if m := assignRE.FindStringSubmatch(text); m != nil {
			return "prop:" + m[1], true
		}
	case "imports":
		if m := coordRE.FindStringSubmatch(text); m != nil {
			return "bom:" + m[1] + ":" + m[2], true
		}
	case "root":
		if m := extraRE.FindStringSubmatch(text); m != nil {
			return "prop:" + m[1], true
		}
		if m := valByExtraRE.FindStringSubmatch(text); m != nil {
			return "prop:" + m[1], true
		}
		if isBlock {
			return "block:" + strings.ReplaceAll(strings.Join(strings.Fields(head), ""), "'", `"`), false
		}
		if m := assignRE.FindStringSubmatch(text); m != nil {
			return "assign:" + m[1], false
		}
	}
	return "stmt:" + norm, false
}

// gradleCoordinates returns group:artifact pairs referenced by dependency
// statements.
func (d *Doc) gradleCoordinates() []string {
	var out []string
	deps := d.Root.Child("block:dependencies")
	if deps == nil {
		return nil
	}
	for _, c := range deps.Children {
		for _, m := range coordRE.FindAllStringSubmatch(d.Text(c), -1) {
			out = append(out, m[1]+":"+m[2])
		}
		for _, m := range groupNameRE.FindAllStringSubmatch(d.Text(c), -1) {
			out = append(out, m[1]+":"+m[2])
		}
	}
	return out
}

// Coordinates returns the group:artifact of every declared dependency.
func (d *Doc) Coordinates() []string {
	if d.Kind == Maven {
		return d.mavenCoordinates()
	}
	return d.gradleCoordinates()
}
