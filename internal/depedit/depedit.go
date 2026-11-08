// Package depedit adds and removes Spring Initializr dependencies in an
// existing project. Rather than guessing coordinates, it asks the server to
// generate the project's build file with and without the change and applies
// the difference (see buildfile.Merge).
package depedit

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path"
	"strings"

	"github.com/maskedsyntax/sprout/internal/buildfile"
	"github.com/maskedsyntax/sprout/internal/generator"
	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

const maxReferenceBytes = 32 << 20

// Editor computes dependency changes for projects.
type Editor struct {
	Client *initializr.Client
	Meta   *metadata.Metadata
}

// Target holds the server-side settings matching a project.
type Target struct {
	Project *buildfile.Project
	// Boot is the project's own Spring Boot version (used for compatibility).
	Boot metadata.Version
	// ServerBoot is the server-offered version used to generate references:
	// the same version, or the newest one in the same line.
	ServerBoot string
	Type       metadata.Option
	params     url.Values
}

// Target matches a project with the server's capabilities. bootOverride
// replaces the detected Spring Boot version when non-empty.
func (e *Editor) Target(p *buildfile.Project, bootOverride string) (*Target, error) {
	boot := bootOverride
	if boot == "" {
		boot = p.BootVersion
	}
	if boot == "" {
		return nil, fmt.Errorf("could not detect the Spring Boot version of %s; pass --boot <version>", p.File)
	}
	v, err := metadata.ParseVersion(boot)
	if err != nil || v.IsWildcard() {
		return nil, fmt.Errorf("unrecognized Spring Boot version %q in %s; pass --boot <version>", boot, p.File)
	}
	t := &Target{Project: p, Boot: v, params: url.Values{}}

	opt, err := e.Meta.ResolveBootVersion(boot)
	if err != nil {
		opt, err = e.Meta.ResolveBootVersion(fmt.Sprintf("%d.%d.x", v.Major, v.Minor))
	}
	if err != nil {
		return nil, fmt.Errorf("the project uses Spring Boot %s, which %s no longer offers (available: %s), so Sprout cannot ask it for the right build changes; upgrade the project, or pass --boot with a supported version at your own risk",
			boot, e.Client.BaseURL.Host, strings.Join(bootLines(e.Meta), ", "))
	}
	t.ServerBoot = opt.ID

	shorthand := map[buildfile.Kind]string{buildfile.Maven: "maven", buildfile.GradleGroovy: "gradle-groovy", buildfile.GradleKotlin: "gradle-kotlin"}[p.Kind]
	typ, ok := project.ResolveType(e.Meta.Field(metadata.FieldType), shorthand)
	if !ok {
		return nil, fmt.Errorf("%s does not offer %s projects", e.Client.BaseURL.Host, p.Kind)
	}
	t.Type = typ
	t.params.Set(metadata.FieldType, typ.ID)
	t.params.Set(metadata.FieldBootVersion, t.ServerBoot)

	// Language changes the generated dependencies (e.g. Kotlin adds
	// jackson-module-kotlin), so it must be supported. Java version and
	// packaging fall back to server defaults when not offered: they do not
	// differ between the two references.
	if f := e.Meta.Field(metadata.FieldLanguage); f != nil {
		o, ok := f.Option(p.Language)
		if !ok {
			return nil, fmt.Errorf("%s does not support %s projects", e.Client.BaseURL.Host, p.Language)
		}
		t.params.Set(metadata.FieldLanguage, o.ID)
	}
	for field, value := range map[string]string{metadata.FieldPackaging: p.Packaging, metadata.FieldJavaVersion: p.JavaVersion} {
		if f := e.Meta.Field(field); f != nil && value != "" {
			if o, ok := f.Option(value); ok {
				t.params.Set(field, o.ID)
			}
		}
	}
	for k, v := range map[string]string{"groupId": "com.example", "artifactId": "demo", "name": "demo", "packageName": "com.example.demo"} {
		if e.Meta.Field(k) != nil {
			t.params.Set(k, v)
		}
	}
	return t, nil
}

func bootLines(m *metadata.Metadata) []string {
	var lines []string
	seen := map[string]bool{}
	for _, bv := range m.BootVersions() {
		l := fmt.Sprintf("%d.%d", bv.Version.Major, bv.Version.Minor)
		if !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
	}
	return lines
}

// Detection is the set of Spring Initializr dependencies found in a project.
type Detection struct {
	// Present lists IDs the project declares, in server order.
	Present []string
	// Ambiguous lists IDs whose coordinates are shared with other IDs (such
	// as a dependency that only adds a build plugin); their presence cannot
	// be determined from the build file.
	Ambiguous map[string]bool
}

// Detect finds the dependencies a project already declares, by matching the
// main coordinates the server reports for each dependency.
func (e *Editor) Detect(ctx context.Context, t *Target) (*Detection, error) {
	coords, err := e.Client.DependencyCoordinates(ctx, t.ServerBoot)
	if err != nil {
		return nil, fmt.Errorf("look up dependency coordinates: %w", err)
	}
	owners := map[string]int{}
	for _, c := range coords {
		owners[c.GroupID+":"+c.ArtifactID]++
	}
	declared := map[string]bool{}
	for _, ga := range t.Project.Doc.Coordinates() {
		declared[ga] = true
	}
	det := &Detection{Ambiguous: map[string]bool{}}
	for _, d := range e.Meta.Dependencies() {
		c, ok := coords[d.ID]
		if !ok {
			det.Ambiguous[d.ID] = true
			continue
		}
		ga := c.GroupID + ":" + c.ArtifactID
		switch {
		case owners[ga] > 1:
			det.Ambiguous[d.ID] = true
		case declared[ga]:
			det.Present = append(det.Present, d.ID)
		}
	}
	return det, nil
}

// Plan computes the edit that adds and removes the given dependency IDs.
// present should be Detection.Present.
func (e *Editor) Plan(ctx context.Context, t *Target, present, add, remove []string) (*buildfile.Plan, error) {
	before := union(present, remove)
	after := minus(union(present, add), remove)
	a, err := e.reference(ctx, t, before)
	if err != nil {
		return nil, err
	}
	b, err := e.reference(ctx, t, after)
	if err != nil {
		return nil, err
	}
	return buildfile.Merge(t.Project.Doc, a, b)
}

// reference downloads the build file Spring Initializr generates for the
// project settings with the given dependencies.
func (e *Editor) reference(ctx context.Context, t *Target, deps []string) (*buildfile.Doc, error) {
	q := url.Values{}
	maps.Copy(q, t.params)
	if len(deps) > 0 {
		q.Set(metadata.FieldDependencies, strings.Join(deps, ","))
	}
	dl, err := e.Client.Generate(ctx, generator.Endpoint(e.Meta, t.Type), q)
	if err != nil {
		return nil, fmt.Errorf("ask %s for the build file: %w", e.Client.BaseURL.Host, err)
	}
	defer dl.Body.Close()
	data, err := io.ReadAll(io.LimitReader(dl.Body, maxReferenceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download reference project: %w", err)
	}
	if len(data) > maxReferenceBytes {
		return nil, errors.New("reference project is unexpectedly large")
	}
	src, err := buildFileFromZip(data, t.Project.Kind.FileName())
	if err != nil {
		return nil, err
	}
	doc, err := buildfile.Parse(t.Project.Kind, src)
	if err != nil {
		return nil, fmt.Errorf("parse reference %s: %w", t.Project.Kind.FileName(), err)
	}
	return doc, nil
}

func buildFileFromZip(data []byte, name string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("reference project is not a ZIP archive: %w", err)
	}
	for _, f := range zr.File {
		clean := path.Clean(f.Name)
		// The build file is at the root, or inside a single base directory.
		if clean != name && (path.Base(clean) != name || strings.Count(clean, "/") != 1) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", fmt.Errorf("reference project has no %s", name)
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func minus(a, b []string) []string {
	drop := map[string]bool{}
	for _, s := range b {
		drop[s] = true
	}
	var out []string
	for _, s := range a {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}
