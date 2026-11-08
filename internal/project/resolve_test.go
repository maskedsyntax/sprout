package project

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/metadata"
)

func fixture(t *testing.T) *metadata.Metadata {
	t.Helper()
	data, err := os.ReadFile("../metadata/testdata/start-spring-io-v2.3.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := metadata.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolveDefaults(t *testing.T) {
	m := fixture(t)
	req, err := Resolve(m, Options{ArtifactID: "my-api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"type": "gradle-project", "language": "java", "bootVersion": "4.1.2", "groupId": "com.example",
		"artifactId": "my-api", "name": "my-api", "packageName": "com.example.my_api", "version": "0.0.1-SNAPSHOT",
		"packaging": "jar", "javaVersion": "17", "configurationFileFormat": "properties",
	}
	q := req.Query()
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("description") || q.Has("dependencies") {
		t.Errorf("unexpected params: %v", q)
	}
	if !req.IsArchive() {
		t.Error("gradle-project should be an archive")
	}
	// Known fields come out in a logical order.
	if req.Values[0].Field != "language" {
		t.Errorf("first value = %s", req.Values[0].Field)
	}
}

func TestResolveExplicitValues(t *testing.T) {
	m := fixture(t)
	req, err := Resolve(m, Options{
		Type: "gradle-kotlin", Language: "Kotlin", BootVersion: "4.0.x", GroupID: "io.acme", ArtifactID: "svc",
		Name: "Service", Description: "My service", Version: "1.0.0", Packaging: "WAR", JavaVersion: "21",
		ConfigFormat: "yaml", Dependencies: []string{"web", "WEB", "data-jpa"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := req.Query()
	for k, v := range map[string]string{
		"type": "gradle-project-kotlin", "language": "kotlin", "bootVersion": "4.0.9", "packageName": "io.acme.svc",
		"packaging": "war", "configurationFileFormat": "yaml", "dependencies": "web,data-jpa", "description": "My service",
		"name": "Service",
	} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
}

func TestResolveType(t *testing.T) {
	f := fixture(t).Field(metadata.FieldType)
	for in, want := range map[string]string{
		"maven": "maven-project", "gradle": "gradle-project", "gradle-groovy": "gradle-project",
		"gradle-kotlin": "gradle-project-kotlin", "Maven POM": "maven-build", "maven-build": "maven-build",
	} {
		o, ok := ResolveType(f, in)
		if !ok || o.ID != want {
			t.Errorf("ResolveType(%q) = %q, want %q", in, o.ID, want)
		}
	}
	if _, ok := ResolveType(f, "ant"); ok {
		t.Error("ant should not resolve")
	}
	req, err := Resolve(fixture(t), Options{ArtifactID: "x", Type: "maven-build"}, nil)
	if err != nil || req.IsArchive() {
		t.Errorf("maven-build should be a single file: %v", err)
	}
}

func TestResolveProblems(t *testing.T) {
	m := fixture(t)
	opts := Options{
		ArtifactID: "x", JavaVersion: "8", Language: "cobol", BootVersion: "4.0.x",
		Dependencies: []string{"vaadin", "data-jp", "web"},
		Extra:        map[string]string{"flavor": "vanilla"},
	}
	_, src := Merge(Layer{Source: `preset "old"`, Options: opts})
	_, err := Resolve(m, opts, src)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`java "8" (from preset "old") is not available; choose one of: 27, 25, 21, 17`,
		`language "cobol"`,
		`flavor "vanilla" (from preset "old") is not supported by this Spring Initializr server`,
		`dependency "vaadin" (Vaadin) (from preset "old") requires Spring Boot >=4.1.0 and <4.2.0-M1, but 4.0.9 is selected`,
		`unknown dependency "data-jp" (from preset "old"); did you mean data-jpa`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	if len(ve.Problems) != 5 {
		t.Errorf("got %d problems: %s", len(ve.Problems), msg)
	}
}

func TestResolveTextValidation(t *testing.T) {
	m := fixture(t)
	cases := map[string]Options{
		"invalid group ID":     {ArtifactID: "x", GroupID: "com..example"},
		"invalid artifact ID":  {ArtifactID: "my api"},
		"invalid package name": {ArtifactID: "x", PackageName: "com.example.my-api"},
		"whitespace":           {ArtifactID: "x", Version: "1 0"},
	}
	for want, o := range cases {
		_, err := Resolve(m, o, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", o, err, want)
		}
	}
	_, err := Resolve(m, Options{ArtifactID: "x", PackageName: "com.example.my-api"}, nil)
	if !strings.Contains(err.Error(), `did you mean "com.example.my_api"`) {
		t.Errorf("missing suggestion: %v", err)
	}
}

func TestResolveExtraCapability(t *testing.T) {
	m, err := metadata.Parse([]byte(`{
	  "type": {"type":"action","default":"maven-project","values":[{"id":"maven-project","action":"/starter.zip","tags":{"build":"maven","format":"project"}}]},
	  "flavor": {"type":"single-select","default":"vanilla","values":[{"id":"vanilla"},{"id":"chocolate"}]},
	  "motto": {"type":"text","default":"hi"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := Resolve(m, Options{Extra: map[string]string{"flavor": "Chocolate"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q := req.Query(); q.Get("flavor") != "chocolate" || q.Get("motto") != "hi" {
		t.Errorf("query = %v", q)
	}
	// Fields the server does not advertise are rejected, not ignored.
	_, err = Resolve(m, Options{ConfigFormat: "yaml", JavaVersion: "21"}, nil)
	if err == nil || !strings.Contains(err.Error(), "config-format") || !strings.Contains(err.Error(), "java") {
		t.Errorf("expected unsupported errors, got %v", err)
	}
	_, err = Resolve(m, Options{Dependencies: []string{"web"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support dependencies") {
		t.Errorf("expected dependency support error, got %v", err)
	}
}

func TestMerge(t *testing.T) {
	o, src := Merge(
		Layer{Source: "config", Options: Options{JavaVersion: "17", GroupID: "com.a", Dependencies: []string{"devtools"}}},
		Layer{Source: "preset", Options: Options{JavaVersion: "21", Dependencies: []string{"web", "DevTools"}}},
		Layer{Source: "flags", Options: Options{GroupID: "com.b", Dependencies: []string{"security"}}},
	)
	if o.JavaVersion != "21" || o.GroupID != "com.b" {
		t.Errorf("merged = %+v", o)
	}
	if strings.Join(o.Dependencies, ",") != "devtools,web,security" {
		t.Errorf("deps = %v", o.Dependencies)
	}
	if src["javaVersion"] != "preset" || src["groupId"] != "flags" || src["dep:web"] != "preset" {
		t.Errorf("sources = %v", src)
	}
}

func TestRequestOptionsRoundTrip(t *testing.T) {
	m := fixture(t)
	req, err := Resolve(m, Options{ArtifactID: "a", Dependencies: []string{"web"}, JavaVersion: "21"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Resolve(m, req.Options(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Query().Encode() != again.Query().Encode() {
		t.Errorf("round trip changed request:\n%s\n%s", req.Query().Encode(), again.Query().Encode())
	}
}
