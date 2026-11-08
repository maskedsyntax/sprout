package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetGetSaveLoad(t *testing.T) {
	c := &Config{}
	sets := map[string]string{
		"default-language": "kotlin", "default-build": "maven-project", "default-editor": "code --wait",
		"default-group": "com.acme", "server": "https://start.example.com/", "default-java": "21",
		"default-boot": "4.1.x", "output-dir": "~/code", "cache-ttl": "7d", "git": "true",
		"default-deps": "devtools, lombok", "default-config-format": "yaml", "default-extra.flavor": "vanilla",
	}
	for k, v := range sets {
		if err := c.Set(k, v); err != nil {
			t.Fatalf("Set(%s): %v", k, err)
		}
	}
	if c.Defaults.Type != "maven-project" || c.Editor != "code --wait" || c.Server != "https://start.example.com" || !c.Git {
		t.Errorf("config = %+v", c)
	}
	if v, _ := c.Get("default-dependencies"); v != "devtools,lombok" {
		t.Errorf("deps = %q", v)
	}
	if c.TTL() != 7*24*time.Hour {
		t.Errorf("ttl = %v", c.TTL())
	}

	p := filepath.Join(t.TempDir(), "sub", FileName)
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "[defaults]") || !strings.Contains(string(data), `language = "kotlin"`) {
		t.Errorf("file:\n%s", data)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Defaults.JavaVersion != "21" || loaded.Defaults.Extra["flavor"] != "vanilla" || loaded.OutputDir != "~/code" {
		t.Errorf("loaded = %+v", loaded)
	}

	if err := loaded.Unset("server"); err != nil || loaded.ServerURL() != "https://start.spring.io" {
		t.Errorf("unset server: %v %s", err, loaded.ServerURL())
	}
	if err := loaded.Unset("default-language"); err != nil || loaded.Defaults.Language != "" {
		t.Errorf("unset language: %v", err)
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	c := &Config{}
	for k, v := range map[string]string{
		"nope": "x", "server": "ftp://x", "cache-ttl": "soon", "git": "maybe",
		"default-group": "com..x", "editor": `"unterminated`, "default-name": "x",
	} {
		if err := c.Set(k, v); err == nil {
			t.Errorf("Set(%s, %s) succeeded", k, v)
		}
	}
}

func TestLoadMissingAndInvalid(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || c.Server != "" {
		t.Errorf("missing: %+v %v", c, err)
	}
	p := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(p, []byte("servr = 'typo'\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown keys: servr") {
		t.Errorf("unknown key: %v", err)
	}
	os.WriteFile(p, []byte("server = \n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Error("invalid TOML accepted")
	}
}
