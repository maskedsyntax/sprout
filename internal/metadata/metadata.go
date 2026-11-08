// Package metadata models Spring Initializr service metadata.
//
// The model is intentionally generic: every capability advertised by the
// server becomes a Field, so new single-select or text capabilities appear
// without code changes. Well-known field IDs are exposed as constants.
package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Well-known capability identifiers used by Spring Initializr.
const (
	FieldDependencies = "dependencies"
	FieldType         = "type"
	FieldPackaging    = "packaging"
	FieldJavaVersion  = "javaVersion"
	FieldLanguage     = "language"
	FieldBootVersion  = "bootVersion"
	FieldGroupID      = "groupId"
	FieldArtifactID   = "artifactId"
	FieldVersion      = "version"
	FieldName         = "name"
	FieldDescription  = "description"
	FieldPackageName  = "packageName"
	FieldConfigFormat = "configurationFileFormat"
)

// FieldKind is the capability type advertised by the server.
type FieldKind string

const (
	KindText         FieldKind = "text"
	KindSingleSelect FieldKind = "single-select"
	KindAction       FieldKind = "action"
	KindMultiSelect  FieldKind = "hierarchical-multi-select"
)

// Link is a HAL link.
type Link struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated,omitempty"`
	Title     string `json:"title,omitempty"`
}

// Option is one value of a select or action capability.
type Option struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Action      string            `json:"action,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// Label returns the human-readable name, falling back to the ID.
func (o Option) Label() string {
	if o.Name != "" {
		return o.Name
	}
	return o.ID
}

// Field is a single capability (text input, single-select or action).
type Field struct {
	ID      string    `json:"id"`
	Kind    FieldKind `json:"type"`
	Default string    `json:"default,omitempty"`
	Options []Option  `json:"values,omitempty"`
}

// Option returns the option matching id (case-insensitive), also matching
// the option name when no ID matches.
func (f *Field) Option(id string) (Option, bool) {
	id = strings.TrimSpace(id)
	for _, o := range f.Options {
		if strings.EqualFold(o.ID, id) {
			return o, true
		}
	}
	for _, o := range f.Options {
		if o.Name != "" && strings.EqualFold(o.Name, id) {
			return o, true
		}
	}
	return Option{}, false
}

// OptionIDs returns the IDs of all options.
func (f *Field) OptionIDs() []string {
	ids := make([]string, len(f.Options))
	for i, o := range f.Options {
		ids[i] = o.ID
	}
	return ids
}

// Dependency is a selectable project dependency.
type Dependency struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Group        string          `json:"group"`
	VersionRange string          `json:"versionRange,omitempty"`
	Links        map[string]Link `json:"links,omitempty"`
	Range        Range           `json:"-"`
}

// CompatibleWith reports whether the dependency supports the Boot version.
func (d *Dependency) CompatibleWith(boot Version) bool { return d.Range.Contains(boot) }

// Group is a dependency category.
type Group struct {
	Name         string        `json:"name"`
	Dependencies []*Dependency `json:"dependencies"`
}

// Metadata is the parsed service metadata.
type Metadata struct {
	Fields          []*Field        `json:"fields"`
	Groups          []*Group        `json:"groups"`
	Links           map[string]Link `json:"links,omitempty"`
	HasDependencies bool            `json:"-"`

	byID  map[string]*Field
	byDep map[string]*Dependency
}

// Field returns the capability with the given ID or nil.
func (m *Metadata) Field(id string) *Field { return m.byID[id] }

// Dependency looks up a dependency by ID (case-insensitive).
func (m *Metadata) Dependency(id string) (*Dependency, bool) {
	d, ok := m.byDep[strings.ToLower(strings.TrimSpace(id))]
	return d, ok
}

// Dependencies returns all dependencies in server order.
func (m *Metadata) Dependencies() []*Dependency {
	var out []*Dependency
	for _, g := range m.Groups {
		out = append(out, g.Dependencies...)
	}
	return out
}

// Group returns the dependency group with the given name (case-insensitive).
func (m *Metadata) Group(name string) (*Group, bool) {
	for _, g := range m.Groups {
		if strings.EqualFold(g.Name, strings.TrimSpace(name)) {
			return g, true
		}
	}
	return nil, false
}

type rawField struct {
	Type    FieldKind       `json:"type"`
	Default json.RawMessage `json:"default"`
	Values  json.RawMessage `json:"values"`
}

type rawDependency struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	VersionRange string          `json:"versionRange"`
	Links        json.RawMessage `json:"_links"`
}

type rawGroup struct {
	Name   string          `json:"name"`
	Values []rawDependency `json:"values"`
}

// Parse decodes Spring Initializr metadata (v2.1, v2.2 or v2.3 JSON).
func Parse(data []byte) (*Metadata, error) {
	keys, values, err := orderedObject(data)
	if err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	m := &Metadata{byID: map[string]*Field{}, byDep: map[string]*Dependency{}}
	for i, key := range keys {
		raw := values[i]
		if key == "_links" {
			if err := json.Unmarshal(raw, &m.Links); err != nil {
				return nil, fmt.Errorf("parse metadata links: %w", err)
			}
			continue
		}
		var rf rawField
		if err := json.Unmarshal(raw, &rf); err != nil || rf.Type == "" {
			// Not a capability object; ignore unknown top-level members.
			continue
		}
		if rf.Type == KindMultiSelect || key == FieldDependencies {
			if err := m.parseDependencies(rf.Values); err != nil {
				return nil, err
			}
			continue
		}
		f := &Field{ID: key, Kind: rf.Type, Default: scalarString(rf.Default)}
		if len(rf.Values) > 0 && string(rf.Values) != "null" {
			if err := json.Unmarshal(rf.Values, &f.Options); err != nil {
				return nil, fmt.Errorf("parse metadata field %q: %w", key, err)
			}
		}
		m.Fields = append(m.Fields, f)
		m.byID[key] = f
	}
	if m.Field(FieldType) == nil || len(m.Field(FieldType).Options) == 0 {
		return nil, errors.New("parse metadata: no project types advertised (is this a Spring Initializr service?)")
	}
	return m, nil
}

func (m *Metadata) parseDependencies(raw json.RawMessage) error {
	m.HasDependencies = true
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var groups []rawGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return fmt.Errorf("parse metadata dependencies: %w", err)
	}
	for _, rg := range groups {
		g := &Group{Name: rg.Name}
		for _, rd := range rg.Values {
			if rd.ID == "" {
				continue
			}
			rng, err := ParseRange(rd.VersionRange)
			if err != nil {
				return fmt.Errorf("parse dependency %q: %w", rd.ID, err)
			}
			d := &Dependency{ID: rd.ID, Name: rd.Name, Description: rd.Description, Group: rg.Name, VersionRange: rd.VersionRange, Range: rng}
			if d.Name == "" {
				d.Name = d.ID
			}
			d.Links = parseLinks(rd.Links)
			g.Dependencies = append(g.Dependencies, d)
			m.byDep[strings.ToLower(d.ID)] = d
		}
		m.Groups = append(m.Groups, g)
	}
	return nil
}

// parseLinks accepts both single links and arrays of links, keeping the first.
func parseLinks(raw json.RawMessage) map[string]Link {
	if len(raw) == 0 {
		return nil
	}
	var generic map[string]json.RawMessage
	if json.Unmarshal(raw, &generic) != nil {
		return nil
	}
	out := map[string]Link{}
	for rel, v := range generic {
		var l Link
		if json.Unmarshal(v, &l) == nil && l.Href != "" {
			out[rel] = l
			continue
		}
		var ls []Link
		if json.Unmarshal(v, &ls) == nil && len(ls) > 0 {
			out[rel] = ls[0]
		}
	}
	return out
}

func scalarString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return fmt.Sprint(b)
	}
	return ""
}

// orderedObject decodes a JSON object preserving key order.
func orderedObject(data []byte) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, errors.New("expected a JSON object")
	}
	var keys []string
	var values []json.RawMessage
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, nil, errors.New("expected object key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		values = append(values, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	return keys, values, nil
}

// BootVersions returns the parsed Spring Boot versions advertised by the
// server, in server order. Unparseable IDs are skipped.
func (m *Metadata) BootVersions() []BootVersion {
	f := m.Field(FieldBootVersion)
	if f == nil {
		return nil
	}
	var out []BootVersion
	for _, o := range f.Options {
		v, err := ParseVersion(o.ID)
		if err != nil {
			continue
		}
		out = append(out, BootVersion{Option: o, Version: v, Default: o.ID == f.Default})
	}
	return out
}

// BootVersion pairs a bootVersion option with its parsed version.
type BootVersion struct {
	Option  Option
	Version Version
	Default bool
}

// ResolveBootVersion matches user input against the advertised versions.
// Accepted forms: an exact ID or name ("4.1.2", "4.2.0 (RC2)"), a legacy
// spelling ("4.1.2.RELEASE"), a version line ("4.1", "4.1.x", "4.x"), or
// "latest". Version lines resolve to the newest GA release in that line,
// falling back to the newest milestone/RC, then the newest snapshot.
func (m *Metadata) ResolveBootVersion(input string) (Option, error) {
	f := m.Field(FieldBootVersion)
	if f == nil {
		return Option{}, errors.New("server does not advertise Spring Boot versions")
	}
	input = strings.TrimSpace(input)
	if o, ok := f.Option(input); ok {
		return o, nil
	}
	versions := m.BootVersions()
	if strings.EqualFold(input, "latest") {
		input = "x.x.x"
	}
	want, err := parseVersionPattern(input)
	if err != nil {
		return Option{}, fmt.Errorf("invalid Spring Boot version %q", input)
	}
	var candidates []BootVersion
	for _, bv := range versions {
		if want.any || want.v.Matches(bv.Version) {
			candidates = append(candidates, bv)
		}
	}
	if len(candidates) == 0 {
		return Option{}, fmt.Errorf("no Spring Boot version matches %q", input)
	}
	// Prefer GA, then milestones/RCs, then snapshots; newest first.
	pref := func(v Version) int {
		switch v.Kind() {
		case KindGA:
			return 3
		case KindSnapshot:
			return 1
		}
		return 2
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Version, candidates[j].Version
		if pref(a) != pref(b) {
			return pref(a) > pref(b)
		}
		return a.Compare(b) > 0
	})
	return candidates[0].Option, nil
}

type versionPattern struct {
	v   Version
	any bool
}

func parseVersionPattern(s string) (versionPattern, error) {
	if s == "x.x.x" || s == "x" {
		return versionPattern{any: true}, nil
	}
	switch strings.Count(s, ".") {
	case 0:
		s += ".x.x"
	case 1:
		s += ".x"
	}
	v, err := ParseVersion(s)
	return versionPattern{v: v}, err
}
