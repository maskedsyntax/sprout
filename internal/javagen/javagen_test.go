package javagen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// tree creates files under a temp directory; entries ending in "/" are
// directories.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const app = "package com.example.demo;\n\nimport org.springframework.boot.autoconfigure.SpringBootApplication;\n\n@SpringBootApplication\npublic class DemoApplication {}\n"

func demo(t *testing.T, buildFile, buildContent string) (string, *Module) {
	t.Helper()
	root := tree(t, map[string]string{
		buildFile: buildContent,
		"src/main/java/com/example/demo/DemoApplication.java":           app,
		"src/main/java/com/example/demo/controller/HomeController.java": "package com.example.demo.controller;\nclass HomeController {}\n",
		"src/main/java/com/example/demo/dto/":                           "",
		"src/main/java/com/example/demo/web/api/v1/Api.java":            "package com.example.demo.web.api.v1;\nclass Api {}\n",
		"src/test/java/com/example/demo/DemoApplicationTests.java":      "package com.example.demo;\n",
	})
	p, err := Find(filepath.Join(root, "src", "main", "java", "com", "example"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root || len(p.Modules) != 1 {
		t.Fatalf("root=%s modules=%d", p.Root, len(p.Modules))
	}
	return root, p.Modules[0]
}

func TestFindMavenAndGradle(t *testing.T) {
	for _, c := range []struct{ file, content string }{
		{"pom.xml", "<project><properties><java.version>21</java.version></properties></project>"},
		{"build.gradle", "java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }"},
		{"build.gradle.kts", "java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }"},
	} {
		t.Run(c.file, func(t *testing.T) {
			root, m := demo(t, c.file, c.content)
			if m.SourceRoot != filepath.Join(root, "src", "main", "java") || m.Name != "." {
				t.Errorf("source root = %s, name = %s", m.SourceRoot, m.Name)
			}
			if m.JavaVersion != 21 {
				t.Errorf("java = %d", m.JavaVersion)
			}
		})
	}
}

func TestFindErrors(t *testing.T) {
	if _, err := Find(t.TempDir()); !errors.Is(err, ErrNoProject) {
		t.Errorf("no build file: %v", err)
	}
	root := tree(t, map[string]string{"build.gradle.kts": "", "src/main/kotlin/App.kt": ""})
	if _, err := Find(root); err == nil || !strings.Contains(err.Error(), "src/main/java") {
		t.Errorf("no java sources: %v", err)
	}
}

func TestPackagesAndBasePackage(t *testing.T) {
	_, m := demo(t, "pom.xml", "<project/>")
	if m.BasePackage != "com.example.demo" {
		t.Errorf("base = %q", m.BasePackage)
	}
	// Intermediate folders (com, com.example, web, web.api) are not offered;
	// the empty dto package is.
	want := []string{"com.example.demo", "com.example.demo.controller", "com.example.demo.dto", "com.example.demo.web.api.v1"}
	if !reflect.DeepEqual(m.Packages, want) {
		t.Errorf("packages = %v", m.Packages)
	}
}

func TestBasePackageWithoutSpringBootApplication(t *testing.T) {
	root := tree(t, map[string]string{
		"pom.xml":                         "",
		"src/main/java/org/acme/a/A.java": "package org.acme.a;",
		"src/main/java/org/acme/b/B.java": "package org.acme.b;",
	})
	p, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Modules[0].BasePackage; b != "org.acme" {
		t.Errorf("base = %q", b)
	}
	empty := tree(t, map[string]string{"pom.xml": "", "src/main/java/": ""})
	p, _ = Find(empty)
	if _, err := p.Modules[0].Plan(Class, "User", ""); err == nil || !strings.Contains(err.Error(), "fully qualified") {
		t.Errorf("no base package: %v", err)
	}
	tg, err := p.Modules[0].Plan(Class, "User", "org.acme")
	if err != nil || tg.Package != "org.acme" {
		t.Errorf("explicit package: %+v %v", tg, err)
	}
}

func TestPlanResolvesPackages(t *testing.T) {
	root, m := demo(t, "pom.xml", "<project/>")
	src := filepath.Join(root, "src", "main", "java")
	cases := []struct {
		kind     Kind
		name, in string
		pkg, rel string
	}{
		{Class, "User", "", "com.example.demo", "com/example/demo/User.java"},
		{Class, "controller.UserController", "", "com.example.demo.controller", "com/example/demo/controller/UserController.java"},
		{Class, "UserController", "com.example.demo.controller", "com.example.demo.controller", "com/example/demo/controller/UserController.java"},
		{Class, "UserController", "controller", "com.example.demo.controller", "com/example/demo/controller/UserController.java"},
		{Class, "com.example.demo.model.User", "", "com.example.demo.model", "com/example/demo/model/User.java"},
		{Class, "admin.Panel", "controller", "com.example.demo.controller.admin", "com/example/demo/controller/admin/Panel.java"},
		{Class, "User.java", "model", "com.example.demo.model", "com/example/demo/model/User.java"},
		{Interface, "service.UserService", "", "com.example.demo.service", "com/example/demo/service/UserService.java"},
		{Record, "UserResponse", "dto", "com.example.demo.dto", "com/example/demo/dto/UserResponse.java"},
		{Class, "Foo", "org.other.lib", "org.other.lib", "org/other/lib/Foo.java"}, // reverse domain
		{Class, "Foo", "com.acme", "com.acme", "com/acme/Foo.java"},                // same top-level as base
		{Package, "repository", "", "com.example.demo.repository", "com/example/demo/repository"},
		{Package, "admin", "controller", "com.example.demo.controller.admin", "com/example/demo/controller/admin"},
		{Package, "com.example.demo.web.v2", "", "com.example.demo.web.v2", "com/example/demo/web/v2"},
	}
	for _, c := range cases {
		tg, err := m.Plan(c.kind, c.name, c.in)
		if err != nil {
			t.Errorf("%s %q --in %q: %v", c.kind, c.name, c.in, err)
			continue
		}
		if tg.Package != c.pkg || tg.Path != filepath.Join(src, filepath.FromSlash(c.rel)) {
			t.Errorf("%s %q --in %q: package %s, path %s", c.kind, c.name, c.in, tg.Package, tg.Path)
		}
	}
}

func TestPlanRejectsInvalidInput(t *testing.T) {
	_, m := demo(t, "pom.xml", "<project/>")
	cases := []struct {
		kind     Kind
		name, in string
		want     string
	}{
		{Class, "", "", "must not be empty"},
		{Class, "class", "", "not a valid Java type name"},
		{Class, "1User", "", "not a valid Java type name"},
		{Class, "User-Info", "", "not a valid Java type name"},
		{Class, "var", "", "cannot be used as a Java type name"},
		{Record, "record", "", "cannot be used"},
		{Class, "../Evil", "", "separate packages with single dots"},
		{Class, `..\Evil`, "", "separate packages with single dots"},
		{Class, ".User", "", "single dots"},
		{Class, "a..User", "", "single dots"},
		{Class, "User", "../../etc", "separate packages with dots"},
		{Class, "User", "..", "invalid package name"},
		{Class, "User", "model..x", "invalid package name"},
		{Class, "User", "int.model", "invalid package name"},
		{Class, "HomeController", "controller", "already exists"},
		{Package, "controller", "", "already exists"},
		{Package, "", "", "must not be empty"},
	}
	for _, c := range cases {
		_, err := m.Plan(c.kind, c.name, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %q --in %q: err = %v, want %q", c.kind, c.name, c.in, err, c.want)
		}
	}
}

func TestValidateTypeName(t *testing.T) {
	for _, ok := range []string{"User", "user", "_Internal", "$Proxy", "Ünïcode", "V2Api"} {
		if err := ValidateTypeName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "2Fast", "my-class", "has space", "enum", "null", "true", "_", "yield", "sealed"} {
		if ValidateTypeName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRecordsNeedJava16(t *testing.T) {
	_, m := demo(t, "pom.xml", "<project><properties><java.version>11</java.version></properties></project>")
	if _, err := m.Plan(Record, "Point", ""); err == nil || !strings.Contains(err.Error(), "Java 16") {
		t.Errorf("err = %v", err)
	}
	if _, err := m.Plan(Class, "Point", ""); err != nil {
		t.Error(err)
	}
}

func TestSource(t *testing.T) {
	want := map[Kind]string{
		Class:     "package com.example.demo.model;\n\npublic class User {\n}\n",
		Interface: "package com.example.demo.model;\n\npublic interface User {\n}\n",
		Enum:      "package com.example.demo.model;\n\npublic enum User {\n}\n",
		Record:    "package com.example.demo.model;\n\npublic record User() {\n}\n",
	}
	for k, w := range want {
		if got := Source(k, "com.example.demo.model", "User"); got != w {
			t.Errorf("%s:\n%s", k, got)
		}
	}
}

func TestCreate(t *testing.T) {
	root, m := demo(t, "pom.xml", "<project/>")
	tg, err := m.Plan(Enum, "model.status.UserStatus", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tg.NewDirs) != 2 {
		t.Errorf("new dirs = %v", tg.NewDirs)
	}
	if err := Create(tg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "src/main/java/com/example/demo/model/status/UserStatus.java"))
	if string(got) != Source(Enum, "com.example.demo.model.status", "UserStatus") {
		t.Errorf("content = %q", got)
	}
	// The file appeared after planning: Create must not overwrite it.
	if err := Create(tg); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second create: %v", err)
	}

	pk, err := m.Plan(Package, "repository", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Create(pk); err != nil || !isDir(pk.Path) {
		t.Errorf("package: %v", err)
	}
}

func TestCreateRollsBackDirectories(t *testing.T) {
	root, m := demo(t, "pom.xml", "<project/>")
	tg, err := m.Plan(Class, "a.b.Thing", "")
	if err != nil {
		t.Fatal(err)
	}
	// Make writing the file fail after the directories are created.
	tg.Path = filepath.Join(tg.Path, "missing", "Thing.java")
	if err := Create(tg); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(filepath.Join(root, "src/main/java/com/example/demo/a")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("created directories were not removed: %v", err)
	}
}

func TestMultiModule(t *testing.T) {
	root := tree(t, map[string]string{
		"pom.xml":     "<project><modules><module>api</module><module>core</module></modules></project>",
		"api/pom.xml": "<project/>",
		"api/src/main/java/com/acme/api/ApiApplication.java": "package com.acme.api;\n@SpringBootApplication\nclass ApiApplication {}",
		"core/build.gradle":                          "",
		"core/src/main/java/com/acme/core/Core.java": "package com.acme.core;",
		"target/src/main/java/":                      "", // build output: ignored
		"node_modules/x/src/main/java/":              "",
	})
	p, err := Find(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.ModuleNames(); !reflect.DeepEqual(got, []string{"api", "core"}) {
		t.Fatalf("modules = %v", got)
	}
	if p.ModuleAt(root) != nil {
		t.Error("the root of a multi-module build should be ambiguous")
	}
	if m := p.ModuleAt(filepath.Join(root, "core", "src", "main")); m == nil || m.Name != "core" || m.BasePackage != "com.acme.core" {
		t.Errorf("inside core: %+v", m)
	}
	if m, err := p.Module("api"); err != nil || m.BasePackage != "com.acme.api" {
		t.Errorf("Module(api) = %+v, %v", m, err)
	}
	if _, err := p.Module("web"); err == nil || !strings.Contains(err.Error(), "api, core") {
		t.Errorf("unknown module: %v", err)
	}
	// Run from inside a module, Find stops at that module's build file.
	p, err = Find(filepath.Join(root, "api", "src"))
	if err != nil || len(p.Modules) != 1 || p.Modules[0].Name != "." {
		t.Errorf("from api: %+v %v", p, err)
	}
}

func TestPackageAt(t *testing.T) {
	root, m := demo(t, "pom.xml", "<project/>")
	if got := m.PackageAt(filepath.Join(root, "src/main/java/com/example/demo/controller")); got != "com.example.demo.controller" {
		t.Errorf("got %q", got)
	}
	for _, p := range []string{root, filepath.Join(root, "src/main/java"), filepath.Join(root, "src/test")} {
		if got := m.PackageAt(p); got != "" {
			t.Errorf("%s: got %q", p, got)
		}
	}
}
