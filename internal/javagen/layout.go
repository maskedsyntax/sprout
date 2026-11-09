// Package javagen creates Java types (classes, interfaces, enums, records)
// and packages in an existing project. It locates the project's Java source
// roots, discovers packages and the application's base package, resolves
// package names and generates minimal source files. It has no UI; the CLI
// adds prompts on top.
package javagen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maskedsyntax/sprout/internal/project"
)

// Project is a build root and the Java source roots found under it.
type Project struct {
	Root    string // directory of the nearest build file
	Modules []*Module
}

// Module is one Java source root (normally <module>/src/main/java).
type Module struct {
	Dir        string // module directory
	Name       string // Dir relative to the project root ("." for the root)
	SourceRoot string // absolute path of src/main/java
	// BasePackage is the package of the @SpringBootApplication class, or the
	// common prefix of existing packages; "" when unknown.
	BasePackage string
	// Packages lists packages worth offering as destinations: those
	// containing Java files, empty leaf packages and the base package.
	Packages []string
	// JavaVersion is the project's Java release (0 when unknown).
	JavaVersion int
}

var buildFileNames = []string{"pom.xml", "build.gradle.kts", "build.gradle"}

// ErrNoProject means no build file was found.
var ErrNoProject = errors.New("no pom.xml, build.gradle or build.gradle.kts found")

// maxDepth limits how deep Find looks for module source roots.
const maxDepth = 5

// skipDirs are never searched for modules.
var skipDirs = map[string]bool{
	"target": true, "build": true, "out": true, "bin": true, "node_modules": true, "buildSrc": true, "src": true,
}

func hasBuildFile(dir string) bool {
	for _, n := range buildFileNames {
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Find locates the project containing dir (walking up to the nearest build
// file) and every Java source root below it.
func Find(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root := ""
	for d := abs; ; d = filepath.Dir(d) {
		if hasBuildFile(d) {
			root = d
			break
		}
		if filepath.Dir(d) == d {
			return nil, fmt.Errorf("%w in %s or its parent directories", ErrNoProject, abs)
		}
	}
	p := &Project{Root: root}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return fs.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
			return fs.SkipDir
		}
		if rel != "." && strings.Count(rel, string(filepath.Separator))+1 > maxDepth {
			return fs.SkipDir
		}
		if src := filepath.Join(path, "src", "main", "java"); isDir(src) {
			p.Modules = append(p.Modules, &Module{Dir: path, Name: filepath.ToSlash(rel), SourceRoot: src})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(p.Modules) == 0 {
		return nil, fmt.Errorf("no Java source folder (src/main/java) found in %s", root)
	}
	rootJava := javaVersion(root)
	for _, m := range p.Modules {
		if err := m.scan(); err != nil {
			return nil, err
		}
		if m.JavaVersion = javaVersion(m.Dir); m.JavaVersion == 0 {
			m.JavaVersion = rootJava
		}
	}
	return p, nil
}

// ModuleAt returns the module containing path, or nil when path does not
// identify one (for example the root of a multi-module build).
func (p *Project) ModuleAt(path string) *Module {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	var best *Module
	for _, m := range p.Modules {
		if within(abs, m.Dir) && (best == nil || len(m.Dir) > len(best.Dir)) {
			best = m
		}
	}
	if best == nil {
		return nil
	}
	// At the root of a multi-module build, only paths inside the root's
	// own src folder pick the root module.
	if best.Dir == p.Root && len(p.Modules) > 1 && !within(abs, filepath.Join(p.Root, "src")) {
		return nil
	}
	return best
}

// Module returns the module with the given name (its path relative to the
// project root, or its directory name when unique).
func (p *Project) Module(name string) (*Module, error) {
	name = strings.Trim(filepath.ToSlash(name), "/")
	if name == "" {
		name = "."
	}
	var byBase []*Module
	for _, m := range p.Modules {
		if m.Name == name {
			return m, nil
		}
		if filepath.Base(m.Dir) == name {
			byBase = append(byBase, m)
		}
	}
	if len(byBase) == 1 {
		return byBase[0], nil
	}
	return nil, fmt.Errorf("no module %q; modules: %s", name, strings.Join(p.ModuleNames(), ", "))
}

// ModuleNames lists the modules' names.
func (p *Project) ModuleNames() []string {
	var out []string
	for _, m := range p.Modules {
		out = append(out, m.Name)
	}
	return out
}

// PackageAt returns the package for a directory inside the source root, or
// "" when path is outside it (or is the source root itself).
func (m *Module) PackageAt(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(m.SourceRoot, abs)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return ""
	}
	pkg := strings.ReplaceAll(filepath.ToSlash(rel), "/", ".")
	if project.ValidatePackageName(pkg) != nil {
		return ""
	}
	return pkg
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

var (
	packageDeclRE = regexp.MustCompile(`(?m)^\s*package\s+([\p{L}\p{N}_$.]+)\s*;`)
	bootAppRE     = regexp.MustCompile(`@(?:org\.springframework\.boot\.autoconfigure\.)?SpringBootApplication\b`)
)

// scan discovers packages and the base package of a module.
func (m *Module) scan() error {
	type info struct{ java, subdirs bool }
	dirs := map[string]*info{}
	var apps, withJava []string
	err := filepath.WalkDir(m.SourceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == m.SourceRoot {
				return err
			}
			return nil
		}
		rel, _ := filepath.Rel(m.SourceRoot, path)
		pkg := strings.ReplaceAll(filepath.ToSlash(rel), "/", ".")
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if project.ValidatePackageName(pkg) != nil {
				return fs.SkipDir
			}
			dirs[pkg] = &info{}
			if parent := pkg[:max(strings.LastIndexByte(pkg, '.'), 0)]; dirs[parent] != nil {
				dirs[parent].subdirs = true
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".java") {
			return nil
		}
		dir := strings.ReplaceAll(filepath.ToSlash(filepath.Dir(rel)), "/", ".")
		if in := dirs[dir]; in != nil && !in.java {
			in.java = true
			withJava = append(withJava, dir)
		}
		if data, err := os.ReadFile(path); err == nil && bootAppRE.Match(data) {
			if mm := packageDeclRE.FindSubmatch(data); mm != nil {
				apps = append(apps, string(mm[1]))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	switch {
	case len(apps) > 0:
		m.BasePackage = commonPrefix(apps)
	case len(withJava) > 0:
		m.BasePackage = commonPrefix(withJava)
	}
	seen := map[string]bool{}
	for pkg, in := range dirs {
		if in.java || !in.subdirs || pkg == m.BasePackage {
			m.Packages = append(m.Packages, pkg)
			seen[pkg] = true
		}
	}
	if m.BasePackage != "" && !seen[m.BasePackage] {
		m.Packages = append(m.Packages, m.BasePackage)
	}
	sort.Strings(m.Packages)
	return nil
}

// commonPrefix returns the longest common package prefix (by segment).
func commonPrefix(pkgs []string) string {
	prefix := strings.Split(pkgs[0], ".")
	for _, p := range pkgs[1:] {
		segs := strings.Split(p, ".")
		n := 0
		for n < len(prefix) && n < len(segs) && prefix[n] == segs[n] {
			n++
		}
		prefix = prefix[:n]
	}
	return strings.Join(prefix, ".")
}

var javaVersionREs = []*regexp.Regexp{
	regexp.MustCompile(`<java\.version>\s*(?:1\.)?(\d+)`),
	regexp.MustCompile(`<maven\.compiler\.release>\s*(\d+)`),
	regexp.MustCompile(`<maven\.compiler\.source>\s*(?:1\.)?(\d+)`),
	regexp.MustCompile(`JavaLanguageVersion\.of\(\s*(\d+)\s*\)`),
	regexp.MustCompile(`jvmToolchain\(\s*(\d+)\s*\)`),
	regexp.MustCompile(`sourceCompatibility\s*=\s*['"]?(?:JavaVersion\.VERSION_)?(?:1[._])?(\d+)`),
}

// javaVersion reads the Java release from the build file in dir (0 when it
// cannot be found).
func javaVersion(dir string) int {
	for _, n := range buildFileNames {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		for _, re := range javaVersionREs {
			if m := re.FindSubmatch(data); m != nil {
				if v, err := strconv.Atoi(string(m[1])); err == nil {
					return v
				}
			}
		}
	}
	return 0
}
