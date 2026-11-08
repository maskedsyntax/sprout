package buildfile

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// element is a lightweight XML element with source offsets.
type element struct {
	Name       string
	Start, End int // whole element
	InnerStart int // after the start tag
	CloseStart int // start of the end tag (== End for self-closing)
	Text       string
	Children   []*element
}

func (e *element) child(name string) *element {
	if e == nil {
		return nil
	}
	for _, c := range e.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (e *element) value(name string) string {
	if c := e.child(name); c != nil {
		return strings.TrimSpace(c.Text)
	}
	return ""
}

func parseXML(src string) (*element, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	var stack []*element
	var root *element
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e := &element{Name: t.Name.Local, Start: before, InnerStart: int(dec.InputOffset())}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, e)
			} else if root == nil {
				root = e
			}
			stack = append(stack, e)
		case xml.EndElement:
			e := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			e.CloseStart, e.End = before, int(dec.InputOffset())
			if e.CloseStart < e.InnerStart { // self-closing
				e.CloseStart = e.End
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("invalid XML: no root element")
	}
	return root, nil
}

var mavenContainers = map[string]bool{
	"project":                                   true,
	"project/properties":                        true,
	"project/dependencies":                      true,
	"project/dependencyManagement":              true,
	"project/dependencyManagement/dependencies": true,
	"project/repositories":                      true,
	"project/pluginRepositories":                true,
	"project/build":                             true,
	"project/build/plugins":                     true,
}

func parseMaven(src string) (*Doc, error) {
	root, err := parseXML(src)
	if err != nil {
		return nil, err
	}
	if root.Name != "project" {
		return nil, fmt.Errorf("pom.xml: root element is <%s>, expected <project>", root.Name)
	}
	d := &Doc{Kind: Maven, Src: src, maven: root}
	d.Root = mavenNode(root, "project", "")
	return d, nil
}

func mavenNode(e *element, path, parent string) *Node {
	n := &Node{Start: e.Start, End: e.End, CloseStart: e.CloseStart}
	n.Key, n.KeyOnly = mavenKey(e, parent)
	if mavenContainers[path] && e.CloseStart > e.InnerStart {
		n.Container = true
		for _, c := range e.Children {
			n.Children = append(n.Children, mavenNode(c, path+"/"+c.Name, path))
		}
	}
	return n
}

func mavenKey(e *element, parentPath string) (string, bool) {
	parent := parentPath[strings.LastIndex(parentPath, "/")+1:]
	switch parent {
	case "dependencies":
		key := "dep:" + e.value("groupId") + ":" + e.value("artifactId")
		if t := e.value("type"); t != "" && t != "jar" {
			key += ":" + t
		}
		if c := e.value("classifier"); c != "" {
			key += ":" + c
		}
		return key, true
	case "plugins":
		g := e.value("groupId")
		if g == "" {
			g = "org.apache.maven.plugins"
		}
		return "plugin:" + g + ":" + e.value("artifactId"), false
	case "repositories", "pluginRepositories":
		return "repo:" + e.value("id"), true
	case "properties":
		return "prop:" + e.Name, true
	}
	return "el:" + e.Name, false
}

// MavenCoordinates returns the groupId:artifactId of every project dependency.
func (d *Doc) mavenCoordinates() []string {
	var out []string
	for _, dep := range d.maven.child("dependencies").childrenNamed("dependency") {
		out = append(out, dep.value("groupId")+":"+dep.value("artifactId"))
	}
	return out
}

func (e *element) childrenNamed(name string) []*element {
	if e == nil {
		return nil
	}
	var out []*element
	for _, c := range e.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// property resolves ${name} references against <properties>.
func (d *Doc) property(v string) string {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return d.maven.child("properties").value(v[2 : len(v)-1])
	}
	return v
}
