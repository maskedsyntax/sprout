package buildfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Project describes an existing Spring Boot project on disk.
type Project struct {
	Dir         string
	File        string // absolute path of the build file
	Kind        Kind
	Doc         *Doc
	BootVersion string // "" when it could not be detected
	JavaVersion string
	Language    string // java, kotlin or groovy
	Packaging   string // jar or war
	Warnings    []string

	raw  string // file content as read
	crlf bool   // the file uses Windows line endings
}

// Encode converts edited content (always LF) back to the file's original
// line-ending style.
func (p *Project) Encode(content string) string {
	if p.crlf {
		return strings.ReplaceAll(content, "\n", "\r\n")
	}
	return content
}

// Unchanged reports whether data is still the content that was loaded.
func (p *Project) Unchanged(data []byte) bool { return string(data) == p.raw }

// ErrNoProject means no build file was found.
var ErrNoProject = errors.New("no pom.xml, build.gradle or build.gradle.kts found")

// Find locates the project containing dir by walking up to the filesystem
// root, and parses its build file.
func Find(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		var found []Kind
		for _, k := range []Kind{Maven, GradleKotlin, GradleGroovy} {
			if fi, err := os.Stat(filepath.Join(d, k.FileName())); err == nil && fi.Mode().IsRegular() {
				found = append(found, k)
			}
		}
		switch {
		case len(found) == 1:
			return Load(d, found[0])
		case len(found) > 1:
			return nil, fmt.Errorf("%s contains more than one build file (%s and %s); remove the unused one", d, found[0].FileName(), found[1].FileName())
		}
		if parent := filepath.Dir(d); parent == d {
			return nil, fmt.Errorf("%w in %s or its parent directories", ErrNoProject, abs)
		}
	}
}

// Load parses the build file of kind in dir.
func Load(dir string, kind Kind) (*Project, error) {
	path := filepath.Join(dir, kind.FileName())
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := string(data)
	// Work on LF internally; CRLF is restored when writing (see Encode).
	crlf := strings.Contains(raw, "\r\n")
	doc, err := Parse(kind, strings.ReplaceAll(raw, "\r\n", "\n"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p := &Project{Dir: dir, File: path, Kind: kind, Doc: doc, Language: "java", Packaging: "jar", raw: raw, crlf: crlf}
	if kind == Maven {
		err = p.inspectMaven()
	} else {
		err = p.inspectGradle()
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Project) inspectMaven() error {
	root := p.Doc.maven
	if root.child("modules") != nil {
		return fmt.Errorf("%s is a multi-module parent POM; run Sprout in the module that contains your Spring Boot application", p.File)
	}
	if parent := root.child("parent"); parent != nil && parent.value("artifactId") == "spring-boot-starter-parent" {
		p.BootVersion = parent.value("version")
	}
	if p.BootVersion == "" {
		p.BootVersion = p.Doc.property("${spring-boot.version}")
	}
	if p.BootVersion == "" {
		for _, dep := range root.child("dependencyManagement").child("dependencies").childrenNamed("dependency") {
			if dep.value("artifactId") == "spring-boot-dependencies" {
				p.BootVersion = p.Doc.property(dep.value("version"))
			}
		}
	}
	p.JavaVersion = p.Doc.property("${java.version}")
	if pk := root.value("packaging"); pk != "" {
		p.Packaging = pk
	}
	for _, pl := range root.child("build").child("plugins").childrenNamed("plugin") {
		switch pl.value("artifactId") {
		case "kotlin-maven-plugin":
			p.Language = "kotlin"
		case "gmavenplus-plugin":
			p.Language = "groovy"
		}
	}
	return nil
}

var (
	bootPluginRE   = regexp.MustCompile(`id\s*\(?\s*["']org\.springframework\.boot["']\s*\)?\s*version\s*\(?\s*["']([^"']+)["']`)
	catalogBootRE  = regexp.MustCompile(`(?m)^\s*(?:spring-?boot|springBoot|spring_boot)\s*=\s*["']([^"']+)["']`)
	toolchainRE    = regexp.MustCompile(`JavaLanguageVersion\.of\(\s*(\d+)\s*\)|jvmToolchain\(\s*(\d+)\s*\)`)
	sourceCompatRE = regexp.MustCompile(`sourceCompatibility\s*=\s*['"]?(?:JavaVersion\.VERSION_)?(\d+)`)
	kotlinJvmRE    = regexp.MustCompile(`org\.jetbrains\.kotlin\.jvm|kotlin\s*\(\s*"jvm"\s*\)`)
)

func (p *Project) inspectGradle() error {
	src := p.Doc.Src
	if m := bootPluginRE.FindStringSubmatch(src); m != nil {
		p.BootVersion = m[1]
	} else if data, err := os.ReadFile(filepath.Join(p.Dir, "gradle", "libs.versions.toml")); err == nil {
		if m := catalogBootRE.FindStringSubmatch(string(data)); m != nil {
			p.BootVersion = m[1]
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if p.BootVersion == "" && !strings.Contains(src, "org.springframework.boot") {
		return fmt.Errorf("%s does not apply the Spring Boot plugin; run Sprout in the module that contains your Spring Boot application", p.File)
	}
	if m := toolchainRE.FindStringSubmatch(src); m != nil {
		p.JavaVersion = m[1] + m[2]
	} else if m := sourceCompatRE.FindStringSubmatch(src); m != nil {
		p.JavaVersion = m[1]
	}
	plugins := p.Doc.Root.Child("block:plugins")
	if kotlinJvmRE.MatchString(src) {
		p.Language = "kotlin"
	} else if plugins != nil && plugins.Child("plugin:groovy") != nil {
		p.Language = "groovy"
	}
	if plugins != nil && plugins.Child("plugin:war") != nil {
		p.Packaging = "war"
	}
	if deps := p.Doc.Root.Child("block:dependencies"); deps != nil {
		for _, c := range deps.Children {
			if strings.Contains(p.Doc.Text(c), "libs.") {
				p.Warnings = append(p.Warnings, "some dependencies come from a version catalog (libs.*); Sprout cannot see those and writes plain coordinates")
				break
			}
		}
	}
	return nil
}
