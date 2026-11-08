// Package buildfile reads and edits Maven and Gradle build files without
// reformatting them.
//
// A build file is parsed into a shallow tree of keyed nodes (a Maven
// <dependency>, a Gradle dependency statement, a property, a plugin...). Each
// node remembers its exact byte span, so edits splice text into the original
// file and everything else (comments, ordering, formatting) is preserved.
//
// Changes are computed by comparing two reference build files produced by
// Spring Initializr for the same project, one without and one with the
// requested dependency change (see Merge).
package buildfile

import (
	"fmt"
	"regexp"
	"strings"
)

// Kind identifies a build system and DSL.
type Kind string

const (
	Maven        Kind = "maven"
	GradleGroovy Kind = "gradle"
	GradleKotlin Kind = "gradle-kotlin"
)

// FileName returns the conventional build file name.
func (k Kind) FileName() string {
	switch k {
	case Maven:
		return "pom.xml"
	case GradleKotlin:
		return "build.gradle.kts"
	}
	return "build.gradle"
}

// String returns a human-readable name.
func (k Kind) String() string {
	switch k {
	case Maven:
		return "Maven"
	case GradleKotlin:
		return "Gradle (Kotlin DSL)"
	}
	return "Gradle (Groovy DSL)"
}

// Node is a keyed element of a build file.
type Node struct {
	Key string
	// Start and End delimit the node's text in the source (End exclusive).
	Start, End int
	// Container nodes are merged child by child.
	Container bool
	// CloseStart is the offset of a container's closing token.
	CloseStart int
	// KeyOnly nodes are considered present when the key matches, even if the
	// content differs (e.g. a dependency with an explicit version).
	KeyOnly  bool
	Children []*Node
}

// Child returns the first child with the given key.
func (n *Node) Child(key string) *Node {
	for _, c := range n.Children {
		if c.Key == key {
			return c
		}
	}
	return nil
}

func (n *Node) childrenWithKey(key string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Key == key {
			out = append(out, c)
		}
	}
	return out
}

// Doc is a parsed build file.
type Doc struct {
	Kind Kind
	Src  string
	Root *Node

	maven *element // Maven only: element tree for value lookups
}

// Parse parses a build file of the given kind.
func Parse(kind Kind, src string) (*Doc, error) {
	switch kind {
	case Maven:
		return parseMaven(src)
	case GradleGroovy, GradleKotlin:
		return parseGradle(kind, src)
	}
	return nil, fmt.Errorf("unsupported build kind %q", kind)
}

// Text returns the source text of a node.
func (d *Doc) Text(n *Node) string { return d.Src[n.Start:n.End] }

// lineStart returns the offset of the start of the line containing off.
func lineStart(src string, off int) int {
	return strings.LastIndexByte(src[:off], '\n') + 1
}

// lineEnd returns the offset of the newline ending the line containing off
// (or len(src)).
func lineEnd(src string, off int) int {
	if i := strings.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i
	}
	return len(src)
}

// indentOf returns the whitespace before a node on its line, or "" when other
// text precedes it.
func (d *Doc) indentOf(n *Node) string {
	ls := lineStart(d.Src, n.Start)
	pre := d.Src[ls:n.Start]
	if strings.TrimSpace(pre) != "" {
		return ""
	}
	return pre
}

// ownsLines reports whether a node occupies whole lines.
func (d *Doc) ownsLines(n *Node) bool {
	ls := lineStart(d.Src, n.Start)
	le := lineEnd(d.Src, n.End)
	return strings.TrimSpace(d.Src[ls:n.Start]) == "" && strings.TrimSpace(d.Src[n.End:le]) == ""
}

var indentRE = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// indentUnit detects the file's indentation unit (a tab or N spaces).
func (d *Doc) indentUnit() string {
	minSpaces := 0
	for _, m := range indentRE.FindAllStringSubmatch(d.Src, -1) {
		ws := m[1]
		if ws[0] == '\t' {
			return "\t"
		}
		if n := len(ws); minSpaces == 0 || n < minSpaces {
			minSpaces = n
		}
	}
	if minSpaces == 0 {
		return "\t"
	}
	return strings.Repeat(" ", minSpaces)
}

// reindent moves text written at refBase indentation with refUnit levels to
// userBase/userUnit. The first line is returned without leading indentation.
func reindent(text, refBase, refUnit, userBase, userUnit string) string {
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		rest := strings.TrimPrefix(line, refBase)
		levels := 0
		for refUnit != "" && strings.HasPrefix(rest, refUnit) {
			rest = rest[len(refUnit):]
			levels++
		}
		lines[i] = userBase + strings.Repeat(userUnit, levels) + rest
	}
	return strings.Join(lines, "\n")
}

var (
	xmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)
	xmlGapRE     = regexp.MustCompile(`>\s+|\s+<`)
	spaceRE      = regexp.MustCompile(`\s+`)
)

// normalize returns a whitespace-insensitive form of node text for equality.
func (d *Doc) normalize(n *Node) string {
	t := d.Text(n)
	if d.Kind == Maven {
		t = xmlCommentRE.ReplaceAllString(t, "")
		t = xmlGapRE.ReplaceAllStringFunc(t, strings.TrimSpace)
		return spaceRE.ReplaceAllString(strings.TrimSpace(t), " ")
	}
	t = strings.ReplaceAll(t, "'", `"`)
	return spaceRE.ReplaceAllString(t, "")
}

// attachedStart returns the start of the line where a node begins, moved up
// over comment lines directly above it, so text inserted "before" a node does
// not separate it from its comment.
func (d *Doc) attachedStart(n *Node) int {
	pos := lineStart(d.Src, n.Start)
	for pos > 0 {
		prev := lineStart(d.Src, pos-1)
		line := strings.TrimSpace(d.Src[prev : pos-1])
		if !isCommentLine(d.Kind, line) {
			break
		}
		pos = prev
	}
	return pos
}

func isCommentLine(k Kind, line string) bool {
	if k == Maven {
		return strings.HasPrefix(line, "<!--") && strings.HasSuffix(line, "-->")
	}
	return strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*")
}
