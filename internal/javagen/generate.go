package javagen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maskedsyntax/sprout/internal/project"
)

// Kind is what to create.
type Kind string

const (
	Class     Kind = "class"
	Interface Kind = "interface"
	Enum      Kind = "enum"
	Record    Kind = "record"
	Package   Kind = "package"
)

// Kinds lists every kind in menu order.
var Kinds = []Kind{Class, Interface, Enum, Record, Package}

// Label is the human-readable name of a kind.
func (k Kind) Label() string {
	switch k {
	case Class:
		return "Java Class"
	case Interface:
		return "Interface"
	case Enum:
		return "Enum"
	case Record:
		return "Record"
	}
	return "Package"
}

// recordsSince is the first Java release with records.
const recordsSince = 16

// restrictedTypeNames cannot be used as type names (JLS 3.9).
var restrictedTypeNames = map[string]bool{"var": true, "yield": true, "record": true, "sealed": true, "permits": true}

// ValidateTypeName checks a simple Java type name.
func ValidateTypeName(name string) error {
	switch {
	case name == "":
		return errors.New("type name must not be empty")
	case !project.IsJavaIdentifier(name):
		return fmt.Errorf("%q is not a valid Java type name (use letters, digits, '_' or '$', not starting with a digit, and not a keyword)", name)
	case restrictedTypeNames[name]:
		return fmt.Errorf("%q cannot be used as a Java type name", name)
	}
	return nil
}

// ResolvePackage turns a package typed by the user into a fully qualified
// package. A name that already starts with the base package, or that names
// an existing package, is used as is; anything else is relative to the base
// package ("controller" -> "com.example.demo.controller"). An empty name is
// the base package.
func (m *Module) ResolvePackage(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return m.BasePackage, nil
	}
	if err := validatePackage(name); err != nil {
		return "", err
	}
	if m.BasePackage == "" || name == m.BasePackage || strings.HasPrefix(name, m.BasePackage+".") ||
		m.hasPackageDir(name) || m.isTopLevel(name) {
		return name, nil
	}
	return m.BasePackage + "." + name, nil
}

// isTopLevel reports whether name looks fully qualified: it starts with the
// first segment of the base package, with an existing top-level package
// folder, or with a common top-level domain (org.acme.x).
func (m *Module) isTopLevel(name string) bool {
	segs := strings.Split(name, ".")
	switch {
	case m.BasePackage != "" && segs[0] == strings.Split(m.BasePackage, ".")[0]:
		return true
	case strings.Contains(m.BasePackage, ".") && m.hasPackageDir(segs[0]):
		return true
	}
	return len(segs) >= 2 && reverseDomains[segs[0]]
}

// reverseDomains are top-level domains that start most fully qualified
// package names (org.acme.billing).
var reverseDomains = map[string]bool{}

func init() {
	for d := range strings.FieldsSeq("com org net io dev edu gov mil int app co me info biz eu uk de fr nl be ch at it es pl se no dk fi cz ru ua in jp cn kr au nz br ca us ai tech") {
		reverseDomains[d] = true
	}
}

func validatePackage(name string) error {
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid package %q: separate packages with dots, e.g. com.example.app.dto", name)
	}
	return project.ValidatePackageName(name)
}

func (m *Module) hasPackageDir(pkg string) bool {
	return isDir(m.packageDir(pkg))
}

func (m *Module) packageDir(pkg string) string {
	return filepath.Join(append([]string{m.SourceRoot}, strings.Split(pkg, ".")...)...)
}

// Target is a planned file (or package directory).
type Target struct {
	Kind    Kind   `json:"kind"`
	Package string `json:"package"`
	Name    string `json:"name,omitempty"` // simple type name; empty for packages
	// Path is the Java file, or the directory for a package.
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
	// NewDirs are the directories that will be created, outermost first.
	NewDirs []string `json:"newDirs,omitempty"`
}

// Plan validates a request and computes what would be created. name is the
// type name, optionally prefixed with a package ("dto.UserResponse"); for
// packages it is the package name. in, when set, is the destination package
// (relative or fully qualified); a package prefix in name is then relative
// to it.
func (m *Module) Plan(kind Kind, name, in string) (*Target, error) {
	name = strings.TrimSpace(name)
	if kind != Package {
		name = strings.TrimSuffix(name, ".java")
	}
	if name == "" {
		return nil, fmt.Errorf("%s name must not be empty", kind)
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid name %q: separate packages with single dots, e.g. controller.UserController", name)
	}
	pkgPart, simple := name, ""
	if kind != Package {
		pkgPart, simple = "", name
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			pkgPart, simple = name[:i], name[i+1:]
		}
		if err := ValidateTypeName(simple); err != nil {
			return nil, err
		}
	}
	if kind == Record && m.JavaVersion > 0 && m.JavaVersion < recordsSince {
		return nil, fmt.Errorf("records need Java %d or newer, but this project uses Java %d", recordsSince, m.JavaVersion)
	}

	var pkg string
	var err error
	switch {
	case in != "":
		if pkg, err = m.ResolvePackage(in); err != nil {
			return nil, err
		}
		if pkgPart != "" {
			if err := validatePackage(pkgPart); err != nil {
				return nil, err
			}
			pkg = strings.TrimPrefix(pkg+"."+pkgPart, ".")
		}
	default:
		if pkg, err = m.ResolvePackage(pkgPart); err != nil {
			return nil, err
		}
	}
	if pkg == "" {
		return nil, errors.New("no base package found (there is no @SpringBootApplication class); give a fully qualified package, e.g. --in com.example.app")
	}

	dir := m.packageDir(pkg)
	if rel, err := filepath.Rel(m.SourceRoot, dir); err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("package %q is outside the source folder", pkg)
	}
	t := &Target{Kind: kind, Package: pkg, Path: dir}
	for d := dir; d != m.SourceRoot; d = filepath.Dir(d) {
		_, err := os.Lstat(d)
		if err == nil {
			if !isDir(d) {
				return nil, fmt.Errorf("%s exists and is not a directory", d)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		t.NewDirs = append([]string{d}, t.NewDirs...)
	}
	if kind == Package {
		if len(t.NewDirs) == 0 {
			return nil, fmt.Errorf("package %s already exists (%s)", pkg, dir)
		}
		return t, nil
	}
	t.Name = simple
	t.Path = filepath.Join(dir, simple+".java")
	if _, err := os.Lstat(t.Path); err == nil {
		return nil, fmt.Errorf("%s already exists; Sprout never overwrites files", t.Path)
	}
	t.Source = Source(kind, pkg, simple)
	return t, nil
}

// Source returns a minimal source file for a type.
func Source(kind Kind, pkg, name string) string {
	decl := string(kind) + " " + name
	if kind == Record {
		decl += "()"
	}
	return fmt.Sprintf("package %s;\n\npublic %s {\n}\n", pkg, decl)
}

// Create writes a planned target. It never overwrites an existing file and
// removes any directory it created if writing fails.
func Create(t *Target) (err error) {
	var created []string
	defer func() {
		if err != nil {
			for i := len(created) - 1; i >= 0; i-- {
				_ = os.Remove(created[i])
			}
		}
	}()
	for _, d := range t.NewDirs {
		if err := os.Mkdir(d, 0o755); err != nil {
			if errors.Is(err, fs.ErrExist) && isDir(d) {
				continue
			}
			return err
		}
		created = append(created, d)
	}
	if t.Kind == Package {
		return nil
	}
	f, err := os.OpenFile(t.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists; Sprout never overwrites files", t.Path)
		}
		return err
	}
	_, err = f.WriteString(t.Source)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(t.Path)
		return err
	}
	return nil
}
