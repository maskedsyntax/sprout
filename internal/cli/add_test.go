package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/buildfile"
	"github.com/maskedsyntax/sprout/internal/testutil"
)

func fixtureBuild(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "buildfile", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// depEnv prepares a fake server that can produce reference build files and
// a project directory containing the given build file.
func depEnv(t *testing.T, projectType, file, ext, initial string) (*env, string) {
	t.Helper()
	e := newEnv(t)
	e.srv.Coordinates = map[string]string{
		"web":                 "org.springframework.boot:spring-boot-starter-webmvc",
		"data-jpa":            "org.springframework.boot:spring-boot-starter-data-jpa",
		"postgresql":          "org.postgresql:postgresql",
		"lombok":              "org.projectlombok:lombok",
		"cloud-config-client": "org.springframework.cloud:spring-cloud-starter-config",
		"native":              "org.springframework.boot:spring-boot",
		"actuator":            "org.springframework.boot:spring-boot",
	}
	e.srv.BuildFiles = map[string]string{
		testutil.BuildFileKey(projectType):                                                   fixtureBuild(t, "none."+ext),
		testutil.BuildFileKey(projectType, "web"):                                            fixtureBuild(t, "web."+ext),
		testutil.BuildFileKey(projectType, "web", "data-jpa", "postgresql"):                  fixtureBuild(t, "web-jpa-pg."+ext),
		testutil.BuildFileKey(projectType, "web", "lombok", "cloud-config-client", "native"): fixtureBuild(t, "web-lombok-cloud-native."+ext),
	}
	dir := filepath.Join(e.work, "app")
	os.MkdirAll(filepath.Join(dir, "src", "main"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, file), []byte(fixtureBuild(t, initial+"."+ext)), 0o644); err != nil {
		t.Fatal(err)
	}
	return e, dir
}

func TestAddAndRemoveMaven(t *testing.T) {
	e, dir := depEnv(t, "maven-project", "pom.xml", "xml", "web")
	pom := filepath.Join(dir, "pom.xml")

	// Typo-tolerant, run from a subdirectory.
	r := e.mustRun("add", "datajpa", "postgresql", "-C", filepath.Join(dir, "src", "main"))
	if !strings.Contains(r.stderr, `Using data-jpa for "datajpa"`) || !strings.Contains(r.stdout, "Added data-jpa, postgresql to pom.xml") {
		t.Errorf("stdout=%s\nstderr=%s", r.stdout, r.stderr)
	}
	content, _ := os.ReadFile(pom)
	assertSameIgnoringBlankLines(t, string(content), fixtureBuild(t, "web-jpa-pg.xml"))

	r = e.mustRun("add", "data-jpa", "-C", dir)
	if !strings.Contains(r.stderr, "already in the project") || !strings.Contains(r.stdout, "Nothing to change") {
		t.Errorf("re-add: %+v", r)
	}

	r = e.mustRun("remove", "postgresql", "data-jpa", "-C", dir, "--dry-run", "--json")
	var rep depEditReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	if rep.Applied || len(rep.Removed) != 2 || !strings.Contains(rep.Diff, "-\t\t\t<artifactId>postgresql</artifactId>") {
		t.Errorf("dry run report = %+v", rep)
	}
	if after, _ := os.ReadFile(pom); string(after) != string(content) {
		t.Error("dry run modified the file")
	}

	e.mustRun("remove", "postgresql", "data-jpa", "-C", dir, "--yes")
	after, _ := os.ReadFile(pom)
	assertSameIgnoringBlankLines(t, string(after), fixtureBuild(t, "web.xml"))

	r = e.mustRun("remove", "lombok", "-C", dir)
	if !strings.Contains(r.stderr, "lombok is not in the project") {
		t.Errorf("remove absent: %+v", r)
	}
}

func TestAddGradleKotlinWithBOMAndPlugin(t *testing.T) {
	e, dir := depEnv(t, "gradle-project-kotlin", "build.gradle.kts", "kts", "web")
	e.mustRun("add", "lombok", "Config Client", "native", "-C", dir)
	got, _ := os.ReadFile(filepath.Join(dir, "build.gradle.kts"))
	assertSameIgnoringBlankLines(t, string(got), fixtureBuild(t, "web-lombok-cloud-native.kts"))
}

func TestAddErrors(t *testing.T) {
	e, dir := depEnv(t, "maven-project", "pom.xml", "xml", "web")
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"add", "-C", dir}, ExitUsage, "name the dependencies to add"},
		{[]string{"add", "postgres", "-C", dir}, ExitError, "did you mean postgresql"},
		{[]string{"add", "netflix-dgs", "-C", dir}, ExitError, "requires Spring Boot >=4.0.0 and <4.1.0-M1, but the project uses 4.1.2"},
		{[]string{"add", "web", "-C", e.work}, ExitError, "no pom.xml, build.gradle or build.gradle.kts found"},
		{[]string{"--offline", "add", "web", "-C", dir}, ExitError, "requires the Spring Initializr server"},
		{[]string{"add", "web", "-C", dir, "--boot", "3.5.0"}, ExitError, "no longer offers"},
	}
	for _, c := range cases {
		r := e.run(c.args...)
		if r.code != c.code || !strings.Contains(r.stderr, c.want) {
			t.Errorf("sprout %s: code=%d stderr=%s", strings.Join(c.args, " "), r.code, r.stderr)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "pom.xml")); string(b) != fixtureBuild(t, "web.xml") {
		t.Error("failed commands modified pom.xml")
	}
}

func TestNewCorrectsDependencyTypos(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("new", "x", "--deps", "datajpa,WEB", "--dry-run", "--json", "--output", filepath.Join(e.work, "x"))
	if !strings.Contains(r.stdout, `"data-jpa"`) || !strings.Contains(r.stdout, `"web"`) {
		t.Errorf("stdout = %s", r.stdout)
	}
	if !strings.Contains(r.stderr, `Using data-jpa for "datajpa"`) {
		t.Errorf("stderr = %s", r.stderr)
	}
}

// assertSameIgnoringBlankLines compares build files, ignoring blank lines
// (Initializr separates dependency groups with them; their placement is
// cosmetic).
func assertSameIgnoringBlankLines(t *testing.T, got, want string) {
	t.Helper()
	strip := func(s string) string {
		var out []string
		for l := range strings.SplitSeq(s, "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	if strip(got) != strip(want) {
		for _, l := range buildfile.Diff(want, got, 1) {
			t.Logf("%c%s", l.Op, l.Text)
		}
		t.Error("build file differs from the reference")
	}
}
