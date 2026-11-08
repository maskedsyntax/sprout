// Package project turns user-supplied options into a validated Spring
// Initializr generation request.
package project

import (
	"sort"
	"strings"

	"github.com/maskedsyntax/sprout/internal/metadata"
)

// Options holds user-facing project settings. Every field is optional; empty
// values fall back to lower-priority layers and finally to server defaults.
//
// The TOML keys are used by presets and the global configuration.
type Options struct {
	Type         string            `toml:"type,omitempty" json:"type,omitempty"`
	Language     string            `toml:"language,omitempty" json:"language,omitempty"`
	BootVersion  string            `toml:"boot,omitempty" json:"boot,omitempty"`
	GroupID      string            `toml:"group,omitempty" json:"group,omitempty"`
	ArtifactID   string            `toml:"artifact,omitempty" json:"artifact,omitempty"`
	Name         string            `toml:"name,omitempty" json:"name,omitempty"`
	Description  string            `toml:"description,omitempty" json:"description,omitempty"`
	PackageName  string            `toml:"package,omitempty" json:"package,omitempty"`
	Version      string            `toml:"version,omitempty" json:"version,omitempty"`
	Packaging    string            `toml:"packaging,omitempty" json:"packaging,omitempty"`
	JavaVersion  string            `toml:"java,omitempty" json:"java,omitempty"`
	ConfigFormat string            `toml:"config-format,omitempty" json:"configFormat,omitempty"`
	Dependencies []string          `toml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Extra        map[string]string `toml:"extra,omitempty" json:"extra,omitempty"`
}

// knownFields maps Initializr capability IDs to Options struct fields.
var knownFields = []struct {
	id  string
	key string // user-facing key (TOML / config)
	get func(*Options) *string
}{
	{metadata.FieldType, "type", func(o *Options) *string { return &o.Type }},
	{metadata.FieldLanguage, "language", func(o *Options) *string { return &o.Language }},
	{metadata.FieldBootVersion, "boot", func(o *Options) *string { return &o.BootVersion }},
	{metadata.FieldGroupID, "group", func(o *Options) *string { return &o.GroupID }},
	{metadata.FieldArtifactID, "artifact", func(o *Options) *string { return &o.ArtifactID }},
	{metadata.FieldName, "name", func(o *Options) *string { return &o.Name }},
	{metadata.FieldDescription, "description", func(o *Options) *string { return &o.Description }},
	{metadata.FieldPackageName, "package", func(o *Options) *string { return &o.PackageName }},
	{metadata.FieldVersion, "version", func(o *Options) *string { return &o.Version }},
	{metadata.FieldPackaging, "packaging", func(o *Options) *string { return &o.Packaging }},
	{metadata.FieldJavaVersion, "java", func(o *Options) *string { return &o.JavaVersion }},
	{metadata.FieldConfigFormat, "config-format", func(o *Options) *string { return &o.ConfigFormat }},
}

// KnownFieldIDs lists the Initializr capability IDs with dedicated options.
func KnownFieldIDs() []string {
	ids := make([]string, len(knownFields))
	for i, f := range knownFields {
		ids[i] = f.id
	}
	return ids
}

// IsKnownField reports whether id has a dedicated Options field.
func IsKnownField(id string) bool {
	for _, f := range knownFields {
		if f.id == id {
			return true
		}
	}
	return false
}

// KeyForField returns the user-facing key (e.g. "java") for a capability ID
// (e.g. "javaVersion"). Unknown IDs are returned unchanged.
func KeyForField(id string) string {
	for _, f := range knownFields {
		if f.id == id {
			return f.key
		}
	}
	return id
}

// FieldForKey maps a user-facing key or capability ID to a capability ID.
func FieldForKey(key string) (string, bool) {
	for _, f := range knownFields {
		if strings.EqualFold(f.key, key) || strings.EqualFold(f.id, key) {
			return f.id, true
		}
	}
	return "", false
}

// Get returns the value for an Initializr capability ID.
func (o *Options) Get(id string) string {
	for _, f := range knownFields {
		if f.id == id {
			return *f.get(o)
		}
	}
	return o.Extra[id]
}

// Set assigns the value for an Initializr capability ID.
func (o *Options) Set(id, value string) {
	for _, f := range knownFields {
		if f.id == id {
			*f.get(o) = value
			return
		}
	}
	if o.Extra == nil {
		o.Extra = map[string]string{}
	}
	if value == "" {
		delete(o.Extra, id)
		return
	}
	o.Extra[id] = value
}

// SetIDs returns the capability IDs that have a non-empty value, in a stable
// order (known fields first, then extras sorted).
func (o *Options) SetIDs() []string {
	var ids []string
	for _, f := range knownFields {
		if *f.get(o) != "" {
			ids = append(ids, f.id)
		}
	}
	var extra []string
	for k, v := range o.Extra {
		if v != "" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(ids, extra...)
}

// IsZero reports whether no option is set.
func (o *Options) IsZero() bool { return len(o.SetIDs()) == 0 && len(o.Dependencies) == 0 }

// Layer is a named set of options, such as "server default", "config",
// "preset rest-api" or "flags".
type Layer struct {
	Source  string
	Options Options
}

// Sources records which layer supplied each value.
type Sources map[string]string

// Merge combines layers from lowest to highest priority. Scalar values are
// overridden by later layers; dependencies are accumulated (deduplicated,
// order preserved).
func Merge(layers ...Layer) (Options, Sources) {
	var out Options
	src := Sources{}
	seen := map[string]bool{}
	for _, l := range layers {
		opts := l.Options
		for _, id := range opts.SetIDs() {
			out.Set(id, opts.Get(id))
			src[id] = l.Source
		}
		for _, d := range opts.Dependencies {
			key := strings.ToLower(strings.TrimSpace(d))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out.Dependencies = append(out.Dependencies, strings.TrimSpace(d))
			src["dep:"+key] = l.Source
		}
	}
	return out, src
}

// SplitList splits comma- or whitespace-separated values, dropping empties.
func SplitList(values ...string) []string {
	var out []string
	for _, v := range values {
		out = append(out, strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })...)
	}
	return out
}
