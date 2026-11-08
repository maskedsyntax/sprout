package buildfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGradleParserTrickySyntax(t *testing.T) {
	src := `/* header { with braces } */
plugins {
    id 'java' // trailing { comment
    id("org.springframework.boot") version "4.1.2"
}

def banner = """
  multi { line } "quoted"
"""

dependencies {
    implementation("com.example:lib:1.0") {
        exclude(group = "org.unwanted", module = "x")
    }
    implementation "org.x:y:${versions["y"]}"
    testImplementation(platform("org.junit:junit-bom:5.10.0"))
    implementation group: 'org.apache', name: 'commons', version: '1'
    runtimeOnly files('libs/a.jar',
        'libs/b.jar')
}

tasks.named('test') {
    useJUnitPlatform()
}

tasks.register("hello")
    .configure { doLast { println('}') } }
`
	d, err := Parse(GradleGroovy, src)
	if err != nil {
		t.Fatal(err)
	}
	var top []string
	for _, c := range d.Root.Children {
		top = append(top, c.Key)
	}
	want := []string{"block:plugins", "assign:defbanner", "block:dependencies", `block:tasks.named("test")`, `block:tasks.register("hello").configure`}
	if strings.Join(top, "|") != strings.Join(want, "|") {
		// "def banner = ..." is not a plain assignment; accept stmt form too.
		if len(top) != 5 || top[0] != want[0] || top[2] != want[2] || top[3] != want[3] {
			t.Errorf("top-level keys = %q", top)
		}
	}
	deps := d.Root.Child("block:dependencies")
	var dk []string
	for _, c := range deps.Children {
		dk = append(dk, c.Key)
	}
	for _, k := range []string{"dep:implementation com.example:lib", "dep:testImplementation org.junit:junit-bom", "dep:implementation org.apache:commons"} {
		if !strings.Contains(strings.Join(dk, "|"), k) {
			t.Errorf("missing %s in %q", k, dk)
		}
	}
	if len(deps.Children) != 5 {
		t.Errorf("dependency statements = %d: %q", len(deps.Children), dk)
	}
	if !strings.HasSuffix(d.Text(deps.Children[4]), "'libs/b.jar')") {
		t.Errorf("multi-line statement split: %q", d.Text(deps.Children[4]))
	}
}

func TestGradleParserErrors(t *testing.T) {
	for name, src := range map[string]string{
		"unclosed block":   "dependencies {\n implementation 'a:b'\n",
		"unclosed string":  "dependencies {\n implementation 'a:b\n}",
		"unclosed comment": "/* never ends",
		"unbalanced paren": "dependencies {\n implementation('a:b'\n}",
	} {
		if _, err := Parse(GradleGroovy, src); err == nil {
			t.Errorf("%s: expected error", name)
		} else if !strings.Contains(err.Error(), "line") {
			t.Errorf("%s: error lacks position: %v", name, err)
		}
	}
}

func TestMavenParserErrors(t *testing.T) {
	for _, src := range []string{"<project><dependencies></project>", "<notproject/>", ""} {
		if _, err := Parse(Maven, src); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindProject(t *testing.T) {
	read := func(n string) string { b, _ := os.ReadFile("testdata/" + n); return string(b) }

	dir := t.TempDir()
	writeFile(t, dir, "pom.xml", read("web.xml"))
	os.MkdirAll(filepath.Join(dir, "src", "main", "java"), 0o755)
	p, err := Find(filepath.Join(dir, "src", "main", "java"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Maven || p.BootVersion != "4.1.2" || p.JavaVersion != "21" || p.Language != "java" || p.Packaging != "jar" || p.Dir != dir {
		t.Errorf("maven project = %+v", p)
	}

	dir = t.TempDir()
	kts := strings.Replace(read("web.kts"), "plugins {\n", "plugins {\n\tkotlin(\"jvm\") version \"2.2.0\"\n\twar\n", 1)
	writeFile(t, dir, "build.gradle.kts", kts)
	p, err = Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != GradleKotlin || p.BootVersion != "4.1.2" || p.JavaVersion != "21" || p.Language != "kotlin" || p.Packaging != "war" {
		t.Errorf("kts project = %+v", p)
	}

	dir = t.TempDir()
	writeFile(t, dir, "build.gradle", "plugins {\n  alias(libs.plugins.spring.boot)\n}\njava { sourceCompatibility = JavaVersion.VERSION_17 }\ndependencies {\n  implementation(libs.spring.web)\n}\n// org.springframework.boot via catalog\n")
	writeFile(t, dir, "gradle/libs.versions.toml", "[versions]\nspring-boot = \"4.0.9\"\n")
	p, err = Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.BootVersion != "4.0.9" || p.JavaVersion != "17" || len(p.Warnings) != 1 {
		t.Errorf("catalog project = %+v", p)
	}

	dir = t.TempDir()
	writeFile(t, dir, "pom.xml", "<project><modules><module>a</module></modules></project>")
	if _, err := Find(dir); err == nil || !strings.Contains(err.Error(), "multi-module") {
		t.Errorf("multi-module: %v", err)
	}
	dir = t.TempDir()
	writeFile(t, dir, "pom.xml", "<project/>")
	writeFile(t, dir, "build.gradle", "")
	if _, err := Find(dir); err == nil || !strings.Contains(err.Error(), "more than one build file") {
		t.Errorf("ambiguous: %v", err)
	}
	dir = t.TempDir()
	writeFile(t, dir, "build.gradle", "plugins { id 'java' }\n")
	if _, err := Find(dir); err == nil || !strings.Contains(err.Error(), "Spring Boot plugin") {
		t.Errorf("non-boot gradle: %v", err)
	}
}

func TestCoordinates(t *testing.T) {
	for ext := range exts {
		got := strings.Join(ref(t, "web-jpa-pg", ext).Coordinates(), ",")
		for _, want := range []string{"org.springframework.boot:spring-boot-starter-data-jpa", "org.postgresql:postgresql"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: coordinates %s lack %s", ext, got, want)
			}
		}
	}
}

func TestDiff(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\nten\n"
	var got []string
	for _, l := range Diff(a, b, 1) {
		got = append(got, string(l.Op)+l.Text)
	}
	want := "@@@ -2,3 +2,3 @@| 2|-3|+three| 4|@@@ -9,1 +9,2 @@| 9|+ten"
	if strings.Join(got, "|") != want {
		t.Errorf("diff = %q", strings.Join(got, "|"))
	}
	if len(Diff(a, a, 3)) != 0 {
		t.Error("identical inputs produced a diff")
	}
}
