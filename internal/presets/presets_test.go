package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/project"
)

func TestSaveListGetDelete(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), FileName)}
	list, err := s.List()
	if err != nil || len(list) != len(Builtins()) {
		t.Fatalf("initial list: %v %v", list, err)
	}
	opts := project.Options{Language: "java", Type: "maven-project", Packaging: "jar", GroupID: "com.example", JavaVersion: "21",
		Dependencies: []string{"web", "data-jpa", "postgresql"}}
	if err := s.Save("rest-api", opts); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path)
	for _, want := range []string{`[presets.rest-api]`, `language = "java"`, `type = "maven-project"`, `java = "21"`, `dependencies = ["web", "data-jpa", "postgresql"]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file missing %q:\n%s", want, data)
		}
	}
	p, err := s.Get("rest-api")
	if err != nil || p.Builtin || p.Options.JavaVersion != "21" {
		t.Errorf("get: %+v %v", p, err)
	}
	if err := s.Save("mine", project.Options{BootVersion: "4.1.x"}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if len(list) != len(Builtins())+1 {
		t.Errorf("list len = %d", len(list))
	}

	reverted, err := s.Delete("rest-api")
	if err != nil || !reverted {
		t.Errorf("delete customized builtin: %v %v", reverted, err)
	}
	if p, _ := s.Get("rest-api"); !p.Builtin {
		t.Error("builtin not restored")
	}
	if _, err := s.Delete("rest-api"); err == nil {
		t.Error("deleting a builtin should fail")
	}
	if _, err := s.Delete("mine"); err != nil {
		t.Error(err)
	}
	if _, err := s.Get("mine"); err == nil {
		t.Error("deleted preset still present")
	}
}

func TestParseUserFile(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), FileName)}
	os.WriteFile(s.Path, []byte(`
[presets.rest-api]
language = "java"
type = "maven-project"
packaging = "jar"
group = "com.example"
java = "21"
dependencies = ["web", "data-jpa", "postgresql"]

[presets.custom.extra]
flavor = "vanilla"
`), 0o644)
	p, err := s.Get("rest-api")
	if err != nil || p.Options.GroupID != "com.example" || len(p.Options.Dependencies) != 3 {
		t.Errorf("rest-api = %+v %v", p, err)
	}
	if p, _ := s.Get("custom"); p.Options.Extra["flavor"] != "vanilla" {
		t.Errorf("extra = %+v", p.Options)
	}
	os.WriteFile(s.Path, []byte("[presets.x]\njav = \"21\"\n"), 0o644)
	if _, err := s.List(); err == nil || !strings.Contains(err.Error(), "jav") {
		t.Errorf("unknown key error = %v", err)
	}
}

func TestMaterializeAndNames(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), FileName)}
	if err := s.Materialize("postgres"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path)
	if !strings.Contains(string(data), "[presets.postgres]") {
		t.Errorf("not materialized:\n%s", data)
	}
	for _, bad := range []string{"", "a b", "../x", strings.Repeat("a", 70)} {
		if ValidateName(bad) == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}
