// Package config manages Sprout's global preferences (config.toml).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/project"
)

// FileName is the configuration file name inside the config directory.
const FileName = "config.toml"

// Config is the global configuration.
type Config struct {
	Server    string          `toml:"server,omitempty" json:"server,omitempty"`
	CacheTTL  string          `toml:"cache-ttl,omitempty" json:"cacheTTL,omitempty"`
	Editor    string          `toml:"editor,omitempty" json:"editor,omitempty"`
	OutputDir string          `toml:"output-dir,omitempty" json:"outputDir,omitempty"`
	Git       bool            `toml:"git,omitempty" json:"git,omitempty"`
	Defaults  project.Options `toml:"defaults,omitempty" json:"defaults"`
}

// Path returns the default config file path.
func Path() (string, error) {
	dir, err := platform.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the configuration. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	c := &Config{}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	md, err := toml.Decode(string(data), c)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("parse config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if c.CacheTTL != "" {
		if _, err := ParseTTL(c.CacheTTL); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	}
	return c, nil
}

// Save writes the configuration atomically.
func Save(path string, c *Config) error {
	var b strings.Builder
	b.WriteString("# Sprout configuration. Manage with 'sprout config set <key> <value>'.\n")
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := platform.WriteFileAtomic(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// ServerURL returns the configured server or the public default.
func (c *Config) ServerURL() string {
	if c.Server != "" {
		return c.Server
	}
	return initializr.DefaultBaseURL
}

// TTL returns the cache TTL.
func (c *Config) TTL() time.Duration {
	if d, err := ParseTTL(c.CacheTTL); err == nil && c.CacheTTL != "" {
		return d
	}
	return 0
}

// ParseTTL parses durations like "12h", "30m" or "7d".
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid cache TTL %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid cache TTL %q (examples: 30m, 12h, 7d)", s)
	}
	return d, nil
}

// Key documents a settable configuration key.
type Key struct {
	Name        string
	Description string
	Aliases     []string
}

var projectDefaultKeys = []string{"type", "language", "boot", "java", "group", "packaging", "config-format", "version", "description"}

// Keys lists every supported key for 'sprout config set'.
func Keys() []Key {
	keys := []Key{
		{"server", "Spring Initializr base URL", nil},
		{"cache-ttl", "how long cached metadata stays fresh (e.g. 12h, 7d)", nil},
		{"editor", "command used by --open (falls back to $VISUAL/$EDITOR)", []string{"default-editor"}},
		{"output-dir", "directory in which new projects are created", []string{"default-output"}},
		{"git", "initialize a Git repository for new projects (true/false)", []string{"default-git"}},
		{"default-dependencies", "dependencies added to every project (comma-separated)", []string{"default-deps"}},
	}
	descriptions := map[string]string{
		"type":          "default project type (e.g. maven-project, gradle-project-kotlin)",
		"language":      "default language (e.g. java, kotlin)",
		"boot":          "default Spring Boot version or line (e.g. 4.1.x, latest)",
		"java":          "default Java version (e.g. 21)",
		"group":         "default group ID (e.g. com.example)",
		"packaging":     "default packaging (e.g. jar, war)",
		"config-format": "default configuration file format (e.g. properties, yaml)",
		"version":       "default project version (e.g. 0.0.1-SNAPSHOT)",
		"description":   "default project description",
	}
	for _, name := range projectDefaultKeys {
		k := Key{Name: "default-" + name, Description: descriptions[name]}
		if name == "type" {
			k.Aliases = []string{"default-build"}
		}
		keys = append(keys, k)
	}
	return keys
}

// KeyNames returns all key names and aliases (used for completion).
func KeyNames() []string {
	var out []string
	for _, k := range Keys() {
		out = append(out, k.Name)
		out = append(out, k.Aliases...)
	}
	sort.Strings(out)
	return out
}

func canonical(key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, k := range Keys() {
		if k.Name == key {
			return key, nil
		}
		if slices.Contains(k.Aliases, key) {
			return k.Name, nil
		}
	}
	if rest, ok := strings.CutPrefix(key, "default-extra."); ok && rest != "" {
		return key, nil
	}
	return "", fmt.Errorf("unknown config key %q; run 'sprout config set --help' for the list", key)
}

// Set assigns a value to a key.
func (c *Config) Set(key, value string) error {
	name, err := canonical(key)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	switch name {
	case "server":
		u, err := initializr.ParseBaseURL(value)
		if err != nil {
			return err
		}
		c.Server = u.String()
	case "cache-ttl":
		if _, err := ParseTTL(value); err != nil {
			return err
		}
		c.CacheTTL = value
	case "editor":
		if _, err := platform.SplitCommand(value); err != nil {
			return err
		}
		c.Editor = value
	case "output-dir":
		c.OutputDir = value
	case "git":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("git must be true or false")
		}
		c.Git = b
	case "default-dependencies":
		c.Defaults.Dependencies = project.SplitList(value)
	default:
		return c.setProjectDefault(name, value)
	}
	return nil
}

func (c *Config) setProjectDefault(name, value string) error {
	k := strings.TrimPrefix(name, "default-")
	if extra, ok := strings.CutPrefix(k, "extra."); ok {
		c.Defaults.Set(extra, value)
		return nil
	}
	id, ok := project.FieldForKey(k)
	if !ok {
		return fmt.Errorf("unknown config key %q", name)
	}
	if id == "groupId" && value != "" {
		if err := project.ValidateGroupID(value); err != nil {
			return err
		}
	}
	c.Defaults.Set(id, value)
	return nil
}

// Unset clears a key.
func (c *Config) Unset(key string) error {
	name, err := canonical(key)
	if err != nil {
		return err
	}
	switch name {
	case "server":
		c.Server = ""
	case "cache-ttl":
		c.CacheTTL = ""
	case "editor":
		c.Editor = ""
	case "output-dir":
		c.OutputDir = ""
	case "git":
		c.Git = false
	case "default-dependencies":
		c.Defaults.Dependencies = nil
	default:
		return c.setProjectDefault(name, "")
	}
	return nil
}

// Get returns the value of a key ("" when unset).
func (c *Config) Get(key string) (string, error) {
	name, err := canonical(key)
	if err != nil {
		return "", err
	}
	switch name {
	case "server":
		return c.Server, nil
	case "cache-ttl":
		return c.CacheTTL, nil
	case "editor":
		return c.Editor, nil
	case "output-dir":
		return c.OutputDir, nil
	case "git":
		return strconv.FormatBool(c.Git), nil
	case "default-dependencies":
		return strings.Join(c.Defaults.Dependencies, ","), nil
	}
	k := strings.TrimPrefix(name, "default-")
	if extra, ok := strings.CutPrefix(k, "extra."); ok {
		return c.Defaults.Get(extra), nil
	}
	id, _ := project.FieldForKey(k)
	return c.Defaults.Get(id), nil
}
