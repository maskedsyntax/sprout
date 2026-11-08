package wizard

import (
	"os"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
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

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"my-api": "my-api", "My API": "my-api", "  Orders  Service ": "orders-service",
		"café!": "caf", "a.b_c": "a.b_c", "---": "", "Ünïcode Name": "n-code-name",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInitialStateUsesDefaultsAndOptions(t *testing.T) {
	m := fixture(t)
	st := initialState(Input{Meta: m, Initial: project.Options{
		Name: "Orders", BootVersion: "4.0.x", Type: "maven", JavaVersion: "21",
		Dependencies: []string{"WEB", "not-a-dep"},
	}})
	if st.Type != "maven-project" || st.Boot != "4.0.9" || st.Java != "21" || st.Language != "java" {
		t.Errorf("state = %+v", st)
	}
	if st.Group != "com.example" || st.Version != "0.0.1-SNAPSHOT" {
		t.Errorf("text defaults = %q %q", st.Group, st.Version)
	}
	if len(st.Deps) != 1 || st.Deps[0] != "web" {
		t.Errorf("deps = %v", st.Deps)
	}
	o := st.options()
	if o.ArtifactID != "orders" || o.Name != "Orders" {
		t.Errorf("options = %+v", o)
	}
	req, err := project.Resolve(m, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Get(metadata.FieldPackageName) != "com.example.orders" {
		t.Errorf("package = %s", req.Get(metadata.FieldPackageName))
	}
}

func TestInitialStateInvalidValuesFallBack(t *testing.T) {
	st := initialState(Input{Meta: fixture(t), Initial: project.Options{JavaVersion: "8", BootVersion: "1.0.x"}})
	if st.Java != "17" || st.Boot != "4.1.2" {
		t.Errorf("fallbacks: java=%s boot=%s", st.Java, st.Boot)
	}
}

func TestReviewShowsProblems(t *testing.T) {
	m := fixture(t)
	w := &builder{in: Input{Meta: m}, st: initialState(Input{Meta: m, Initial: project.Options{Name: "x"}})}
	w.st.Boot = "4.0.9"
	w.st.Deps = []string{"vaadin"}
	if got := w.review(); !strings.Contains(got, "Problems:") || !strings.Contains(got, "vaadin") {
		t.Errorf("review = %s", got)
	}
	w.st.Deps = []string{"web"}
	if got := w.review(); strings.Contains(got, "Problems") || !strings.Contains(got, "output: x") {
		t.Errorf("review = %s", got)
	}
}

func TestEscapeNote(t *testing.T) {
	if got := escapeNote(`com.example.my_api C:\x *b* ` + "`c`"); got != `com.example.my\_api C:\\x \*b\* \`+"`c\\`" {
		t.Errorf("escapeNote = %s", got)
	}
}
