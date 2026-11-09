package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func javaProject(t *testing.T, e *env, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(e.work, "demo")
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNewJavaTypes(t *testing.T) {
	e := newEnv(t)
	dir := javaProject(t, e, map[string]string{
		"pom.xml": "<project/>",
		"src/main/java/com/example/demo/DemoApplication.java": "package com.example.demo;\n@SpringBootApplication\nclass DemoApplication {}\n",
	})
	src := filepath.Join(dir, "src/main/java/com/example/demo")

	r := e.mustRun("new", "class", "User", "--in", "model", "-C", dir)
	if !strings.Contains(r.stdout, "Created class com.example.demo.model.User") {
		t.Errorf("stdout = %s", r.stdout)
	}
	e.mustRun("new", "record", "dto.UserResponse", "-C", filepath.Join(dir, "src"))
	e.mustRun("new", "interface", "UserService", "--in", "com.example.demo.service", "-C", dir)
	e.mustRun("new", "package", "repository", "-C", dir)
	// From inside a package folder, a plain name goes into that package.
	e.mustRun("new", "enum", "UserStatus", "-C", filepath.Join(src, "model"))

	for file, want := range map[string]string{
		"model/User.java":          "package com.example.demo.model;\n\npublic class User {\n}\n",
		"dto/UserResponse.java":    "package com.example.demo.dto;\n\npublic record UserResponse() {\n}\n",
		"service/UserService.java": "package com.example.demo.service;\n\npublic interface UserService {\n}\n",
		"model/UserStatus.java":    "package com.example.demo.model;\n\npublic enum UserStatus {\n}\n",
	} {
		if got, err := os.ReadFile(filepath.Join(src, file)); err != nil || string(got) != want {
			t.Errorf("%s: %q %v", file, got, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(src, "repository")); err != nil || !fi.IsDir() {
		t.Errorf("package directory: %v", err)
	}

	r = e.mustRun("new", "class", "Order", "--in", "model", "-C", dir, "--dry-run", "--json")
	var rep newTypeReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Created || rep.Package != "com.example.demo.model" || !strings.Contains(rep.Source, "public class Order") {
		t.Errorf("report = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(src, "model/Order.java")); err == nil {
		t.Error("dry run created the file")
	}
}

func TestNewJavaTypeErrors(t *testing.T) {
	e := newEnv(t)
	dir := javaProject(t, e, map[string]string{
		"pom.xml": "<project/>",
		"src/main/java/com/example/demo/DemoApplication.java": "package com.example.demo;\n@SpringBootApplication\nclass DemoApplication {}\n",
		"src/main/java/com/example/demo/model/User.java":      "// mine\n",
	})
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"new", "class", "-C", dir}, ExitUsage, "name the class to create"},
		{[]string{"new", "class", "User", "--in", "model", "-C", dir}, ExitError, "already exists"},
		{[]string{"new", "class", "../../Evil", "-C", dir}, ExitError, "separate packages"},
		{[]string{"new", "class", "Foo", "--in", "../x", "-C", dir}, ExitError, "separate packages"},
		{[]string{"new", "class", "public", "-C", dir}, ExitError, "not a valid Java type name"},
		{[]string{"new", "class", "Foo", "-C", e.work}, ExitError, "no pom.xml, build.gradle or build.gradle.kts found"},
		{[]string{"new", "klass", "User"}, ExitUsage, `unknown kind "klass"`},
	}
	for _, c := range cases {
		r := e.run(c.args...)
		if r.code != c.code || !strings.Contains(r.stderr, c.want) {
			t.Errorf("sprout %s: code=%d stderr=%s", strings.Join(c.args, " "), r.code, r.stderr)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "src/main/java/com/example/demo/model/User.java")); string(got) != "// mine\n" {
		t.Error("existing file was modified")
	}
}

func TestNewJavaTypeMultiModule(t *testing.T) {
	e := newEnv(t)
	dir := javaProject(t, e, map[string]string{
		"pom.xml":     "<project><modules><module>api</module><module>core</module></modules></project>",
		"api/pom.xml": "<project/>",
		"api/src/main/java/com/acme/api/App.java": "package com.acme.api;\n@SpringBootApplication\nclass App {}\n",
		"core/pom.xml": "<project/>",
		"core/src/main/java/com/acme/core/Core.java": "package com.acme.core;\n",
	})
	r := e.run("new", "class", "Foo", "-C", dir)
	if r.code != ExitError || !strings.Contains(r.stderr, "2 modules with Java sources (api, core)") {
		t.Errorf("ambiguous: %+v", r)
	}
	e.mustRun("new", "class", "Foo", "-C", dir, "--module", "core")
	if _, err := os.Stat(filepath.Join(dir, "core/src/main/java/com/acme/core/Foo.java")); err != nil {
		t.Error(err)
	}
}
