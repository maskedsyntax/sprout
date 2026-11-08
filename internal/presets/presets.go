// Package presets stores reusable project configurations (presets.toml).
package presets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/project"
)

// FileName is the presets file inside the config directory.
const FileName = "presets.toml"

// Preset is a named set of project options.
type Preset struct {
	Name    string          `json:"name"`
	Options project.Options `json:"options"`
	// Builtin is true for the bundled examples that have not been
	// customized in presets.toml.
	Builtin bool `json:"builtin"`
}

// Builtins are example presets. They are ordinary options, validated
// against current metadata like any user preset, and can be overridden by
// saving a preset with the same name.
func Builtins() []Preset {
	return []Preset{
		{Name: "minimal", Builtin: true, Options: project.Options{}},
		{Name: "rest-api", Builtin: true, Options: project.Options{Dependencies: []string{"web", "validation", "actuator"}}},
		{Name: "web-jpa", Builtin: true, Options: project.Options{Dependencies: []string{"web", "data-jpa", "h2"}}},
		{Name: "postgres", Builtin: true, Options: project.Options{Dependencies: []string{"web", "data-jpa", "postgresql", "flyway"}}},
		{Name: "secure-api", Builtin: true, Options: project.Options{Dependencies: []string{"web", "security", "oauth2-resource-server", "validation"}}},
	}
}

type file struct {
	Presets map[string]project.Options `toml:"presets"`
}

// Store reads and writes presets.toml.
type Store struct {
	Path string
}

// DefaultStore returns the store in the standard config directory.
func DefaultStore() (Store, error) {
	dir, err := platform.ConfigDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Path: filepath.Join(dir, FileName)}, nil
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateName checks a preset name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid preset name %q: use letters, digits, '.', '-' or '_' (max 64)", name)
	}
	return nil
}

func (s Store) load() (*file, error) {
	f := &file{Presets: map[string]project.Options{}}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read presets: %w", err)
	}
	md, err := toml.Decode(string(data), f)
	if err != nil {
		return nil, fmt.Errorf("parse presets %s: %w", s.Path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("parse presets %s: unknown keys: %s", s.Path, strings.Join(keys, ", "))
	}
	if f.Presets == nil {
		f.Presets = map[string]project.Options{}
	}
	return f, nil
}

func (s Store) save(f *file) error {
	var b strings.Builder
	b.WriteString("# Sprout presets. Use with 'sprout new <name> --preset <preset>'.\n")
	b.WriteString("# Keys: type, language, boot, group, artifact, name, description, package,\n")
	b.WriteString("#       version, packaging, java, config-format, dependencies, [extra].\n\n")
	if err := toml.NewEncoder(&b).Encode(f); err != nil {
		return fmt.Errorf("encode presets: %w", err)
	}
	if err := platform.WriteFileAtomic(s.Path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write presets: %w", err)
	}
	return nil
}

// List returns user presets plus built-in examples not overridden, sorted.
func (s Store) List() ([]Preset, error) {
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []Preset
	for name, o := range f.Presets {
		out = append(out, Preset{Name: name, Options: o})
	}
	for _, b := range Builtins() {
		if _, ok := f.Presets[b.Name]; !ok {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns a preset by name.
func (s Store) Get(name string) (Preset, error) {
	f, err := s.load()
	if err != nil {
		return Preset{}, err
	}
	if o, ok := f.Presets[name]; ok {
		return Preset{Name: name, Options: o}, nil
	}
	for _, b := range Builtins() {
		if b.Name == name {
			return b, nil
		}
	}
	return Preset{}, fmt.Errorf("preset %q not found; run 'sprout presets list'", name)
}

// Save creates or replaces a preset.
func (s Store) Save(name string, o project.Options) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	f, err := s.load()
	if err != nil {
		return err
	}
	f.Presets[name] = o
	return s.save(f)
}

// Delete removes a user preset. Built-in examples cannot be deleted, but a
// customized built-in reverts to the bundled example.
func (s Store) Delete(name string) (reverted bool, err error) {
	f, err := s.load()
	if err != nil {
		return false, err
	}
	_, isUser := f.Presets[name]
	isBuiltin := false
	for _, b := range Builtins() {
		isBuiltin = isBuiltin || b.Name == name
	}
	if !isUser {
		if isBuiltin {
			return false, fmt.Errorf("preset %q is a built-in example and cannot be deleted", name)
		}
		return false, fmt.Errorf("preset %q not found", name)
	}
	delete(f.Presets, name)
	return isBuiltin, s.save(f)
}

// Materialize ensures a preset exists in presets.toml (copying a built-in
// example if needed) so it can be edited by hand.
func (s Store) Materialize(name string) error {
	f, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := f.Presets[name]; ok {
		return nil
	}
	p, err := s.Get(name)
	if err != nil {
		return err
	}
	f.Presets[name] = p.Options
	return s.save(f)
}

// Check reloads the file to verify it still parses (used after editing).
func (s Store) Check() error {
	_, err := s.load()
	return err
}
