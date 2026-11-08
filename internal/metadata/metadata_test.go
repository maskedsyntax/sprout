package metadata

import (
	"os"
	"strings"
	"testing"
)

func loadFixture(t *testing.T) *Metadata {
	t.Helper()
	data, err := os.ReadFile("testdata/start-spring-io-v2.3.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseFixture(t *testing.T) {
	m := loadFixture(t)

	typ := m.Field(FieldType)
	if typ == nil || typ.Kind != KindAction || typ.Default != "gradle-project" {
		t.Fatalf("type field = %+v", typ)
	}
	mp, ok := typ.Option("maven-project")
	if !ok || mp.Action != "/starter.zip" || mp.Tags["build"] != "maven" || mp.Tags["format"] != "project" {
		t.Errorf("maven-project = %+v", mp)
	}
	for id, def := range map[string]string{
		FieldPackaging: "jar", FieldJavaVersion: "17", FieldLanguage: "java", FieldBootVersion: "4.1.2",
		FieldConfigFormat: "properties", FieldGroupID: "com.example", FieldArtifactID: "demo",
		FieldVersion: "0.0.1-SNAPSHOT", FieldPackageName: "com.example.demo",
	} {
		f := m.Field(id)
		if f == nil {
			t.Errorf("missing field %s", id)
			continue
		}
		if f.Default != def {
			t.Errorf("%s default = %q, want %q", id, f.Default, def)
		}
	}
	if m.Field(FieldName).Default != "" {
		t.Errorf("name should have no default")
	}
	if !m.HasDependencies || len(m.Groups) < 10 || len(m.Dependencies()) < 100 {
		t.Errorf("dependencies: %d groups, %d deps", len(m.Groups), len(m.Dependencies()))
	}
	web, ok := m.Dependency("WEB")
	if !ok || web.Name != "Spring Web" || web.Group != "Web" {
		t.Errorf("web = %+v", web)
	}
	vaadin, _ := m.Dependency("vaadin")
	if vaadin.CompatibleWith(mustVersion(t, "4.0.9")) || !vaadin.CompatibleWith(mustVersion(t, "4.1.2")) {
		t.Errorf("vaadin range %s handled incorrectly", vaadin.Range)
	}
	if _, ok := m.Links["maven-project"]; !ok {
		t.Error("missing HAL link for maven-project")
	}
	// Server order is preserved.
	if m.Fields[0].ID != FieldType {
		t.Errorf("first field = %s", m.Fields[0].ID)
	}
}

// Legacy (v2.1) metadata uses .RELEASE versions and differing ranges.
const legacyMetadata = `{
  "_links": {},
  "dependencies": {"type": "hierarchical-multi-select", "values": [
    {"name": "Web", "values": [
      {"id": "web", "name": "Spring Web", "description": "Web apps"},
      {"id": "old", "name": "Old Thing", "versionRange": "[1.5.0.RELEASE,2.0.0.M1)",
       "_links": {"guide": [{"href": "https://a"}, {"href": "https://b"}]}}
    ]}
  ]},
  "type": {"type": "action", "default": "maven-project", "values": [
    {"id": "maven-project", "name": "Maven Project", "action": "/starter.zip", "tags": {"build": "maven", "format": "project"}}
  ]},
  "bootVersion": {"type": "single-select", "default": "2.0.1.RELEASE", "values": [
    {"id": "2.1.0.BUILD-SNAPSHOT", "name": "2.1.0 (SNAPSHOT)"},
    {"id": "2.0.1.RELEASE", "name": "2.0.1"},
    {"id": "1.5.12.RELEASE", "name": "1.5.12"}
  ]},
  "javaVersion": {"type": "single-select", "default": 8, "values": [{"id": "8"}, {"id": "10"}]},
  "flavor": {"type": "single-select", "default": "vanilla", "values": [{"id": "vanilla"}, {"id": "chocolate"}]},
  "unknownThing": 42
}`

func TestParseLegacyAndExtras(t *testing.T) {
	m, err := Parse([]byte(legacyMetadata))
	if err != nil {
		t.Fatal(err)
	}
	if m.Field(FieldJavaVersion).Default != "8" {
		t.Errorf("numeric default not converted: %q", m.Field(FieldJavaVersion).Default)
	}
	if f := m.Field("flavor"); f == nil || f.Default != "vanilla" {
		t.Errorf("custom capability not parsed: %+v", f)
	}
	old, _ := m.Dependency("old")
	if !old.CompatibleWith(mustVersion(t, "1.5.12.RELEASE")) || old.CompatibleWith(mustVersion(t, "2.0.1.RELEASE")) {
		t.Errorf("legacy range %s", old.Range)
	}
	if old.Links["guide"].Href != "https://a" {
		t.Errorf("link arrays: %+v", old.Links)
	}
	if m.Field(FieldConfigFormat) != nil {
		t.Error("configurationFileFormat should be absent")
	}
}

func TestParseErrors(t *testing.T) {
	for name, in := range map[string]string{
		"not json":  "<html>hello</html>",
		"array":     "[]",
		"no types":  `{"bootVersion": {"type": "single-select", "values": []}}`,
		"bad range": `{"type": {"type":"action","values":[{"id":"x"}]}, "dependencies": {"type":"hierarchical-multi-select","values":[{"name":"g","values":[{"id":"a","versionRange":"[1,"}]}]}}`,
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestResolveBootVersion(t *testing.T) {
	m := loadFixture(t)
	tests := map[string]string{
		"4.1.2":            "4.1.2",
		"4.1.2.RELEASE":    "4.1.2",
		"4.2.0 (RC2)":      "4.2.0-RC2",
		"4.1.x":            "4.1.2",
		"4.1":              "4.1.2",
		"4.0.x":            "4.0.9",
		"4.2.x":            "4.2.0-RC2", // no GA yet: newest pre-release
		"4":                "4.1.2",
		"latest":           "4.1.2",
		"4.2.0-snapshot":   "4.2.0-SNAPSHOT",
		" 4.0.10-SNAPSHOT": "4.0.10-SNAPSHOT",
	}
	for in, want := range tests {
		o, err := m.ResolveBootVersion(in)
		if err != nil || o.ID != want {
			t.Errorf("ResolveBootVersion(%q) = %q, %v; want %q", in, o.ID, err, want)
		}
	}
	for _, bad := range []string{"3.5.x", "9.9.9", "banana"} {
		if _, err := m.ResolveBootVersion(bad); err == nil {
			t.Errorf("ResolveBootVersion(%q) succeeded", bad)
		}
	}
}

func TestSearchAndSuggest(t *testing.T) {
	m := loadFixture(t)
	res := m.Search("postgres")
	if len(res) == 0 || res[0].ID != "postgresql" {
		t.Fatalf("search postgres: first = %v", ids(res))
	}
	if res := m.Search("SECURITY"); len(res) == 0 || res[0].ID != "security" {
		t.Errorf("case-insensitive search: %v", ids(res))
	}
	if res := m.Search("data jpa"); len(res) == 0 || res[0].ID != "data-jpa" {
		t.Errorf("multi-term search: %v", ids(res))
	}
	if res := m.Search("zzzznotthere"); len(res) != 0 {
		t.Errorf("expected no results, got %v", ids(res))
	}
	if s := m.Suggest("data-jp", 3); len(s) == 0 || s[0] != "data-jpa" {
		t.Errorf("Suggest(data-jp) = %v", s)
	}
	if s := m.Suggest("qqqqqqqqqqqq", 3); len(s) != 0 {
		t.Errorf("Suggest(nonsense) = %v", s)
	}
}

func ids(ds []*Dependency) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return strings.Join(out, ",")
}
