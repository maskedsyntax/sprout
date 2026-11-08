package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/testutil"
)

type env struct {
	t      *testing.T
	srv    *testutil.Server
	config string
	cache  string
	state  string
	work   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t, srv: testutil.NewServer(t), config: filepath.Join(base, "config"), cache: filepath.Join(base, "cache"),
		state: filepath.Join(base, "state"), work: filepath.Join(base, "work")}
	os.MkdirAll(e.work, 0o755)
	t.Setenv("SPROUT_SERVER", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	return e
}

type result struct {
	stdout, stderr string
	code           int
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	var out, errb bytes.Buffer
	no := false
	app := &App{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, ConfigDir: e.config, CacheDir: e.cache,
		StateDir: e.state, Interactive: &no}
	full := append([]string{"--server", e.srv.URL}, args...)
	code := Execute(context.Background(), app, full)
	return result{out.String(), errb.String(), code}
}

func (e *env) mustRun(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	if r.code != 0 {
		e.t.Fatalf("sprout %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func TestNewGeneratesProjectFromFlags(t *testing.T) {
	e := newEnv(t)
	dest := filepath.Join(e.work, "my-api")
	r := e.mustRun("new", "my-api", "--deps", "web,data-jpa", "--deps", "postgresql", "--language", "kotlin",
		"--build", "gradle-kotlin", "--java", "21", "--boot", "4.1.x", "--group", "com.acme", "--output", dest)
	if !strings.Contains(r.stdout, "Created my-api") {
		t.Errorf("stdout = %s", r.stdout)
	}
	q := e.srv.LastQuery()
	want := map[string]string{"type": "gradle-project-kotlin", "language": "kotlin", "javaVersion": "21", "bootVersion": "4.1.2",
		"groupId": "com.acme", "artifactId": "my-api", "name": "my-api", "packageName": "com.acme.my_api",
		"dependencies": "web,data-jpa,postgresql"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "request.json")); err != nil {
		t.Error(err)
	}
	// History records the configuration; --last reuses it.
	h := e.mustRun("history", "--json")
	if !strings.Contains(h.stdout, `"artifact": "my-api"`) {
		t.Errorf("history = %s", h.stdout)
	}
	e.mustRun("new", "second", "--last", "--output", filepath.Join(e.work, "second"))
	q = e.srv.LastQuery()
	if q.Get("artifactId") != "second" || q.Get("language") != "kotlin" || q.Get("packageName") != "com.acme.second" {
		t.Errorf("--last query = %v", q)
	}
}

func TestNewRefusesExistingDirectory(t *testing.T) {
	e := newEnv(t)
	dest := filepath.Join(e.work, "app")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "HELP.md"), []byte("mine"), 0o644)
	r := e.run("new", "app", "--output", dest)
	if r.code != ExitError || !strings.Contains(r.stderr, "not empty") {
		t.Fatalf("expected refusal: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "HELP.md")); string(b) != "mine" {
		t.Error("existing file modified")
	}
	r = e.mustRun("new", "app", "--output", dest, "--force")
	if !strings.Contains(r.stdout, "originals saved") {
		t.Errorf("stdout = %s", r.stdout)
	}
}

func TestNewValidationErrors(t *testing.T) {
	e := newEnv(t)
	r := e.run("new", "x", "--deps", "vaadin,data-jp", "--boot", "4.0.x", "--java", "8", "--output", filepath.Join(e.work, "x"))
	if r.code != ExitError {
		t.Fatalf("code = %d", r.code)
	}
	for _, want := range []string{"vaadin", "requires Spring Boot", "did you mean data-jpa", `java "8"`} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, r.stderr)
		}
	}
	if len(e.srv.Requests()) != 1 { // only the metadata request
		t.Errorf("generation was attempted despite invalid config")
	}
	if _, err := os.Stat(filepath.Join(e.work, "x")); err == nil {
		t.Error("directory created")
	}

	r = e.run("new")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "project name is required") {
		t.Errorf("missing name: %+v", r)
	}
	r = e.run("new", "x", "--interactive")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "requires a terminal") {
		t.Errorf("--interactive without tty: %+v", r)
	}
	r = e.run("new", "x", "--format", "yaml")
	if r.code != ExitUsage {
		t.Errorf("bad format: %+v", r)
	}
	r = e.run("new", "x", "--set", "flavor=vanilla", "--dry-run")
	if r.code != ExitError || !strings.Contains(r.stderr, "not supported") {
		t.Errorf("unknown capability: %+v", r)
	}
	r = e.run("new", "a/b")
	if r.code != ExitUsage {
		t.Errorf("path in name: %+v", r)
	}
}

func TestNewServerSideError(t *testing.T) {
	e := newEnv(t)
	// Make the server reject a dependency the metadata knows about.
	e.srv.Metadata = []byte(strings.Replace(string(e.srv.Metadata), `"id": "web",`, `"id": "unknown-on-server",`, 1))
	r := e.run("new", "x", "--deps", "unknown-on-server", "--output", filepath.Join(e.work, "x"))
	if r.code != ExitError || !strings.Contains(r.stderr, "Unknown dependency 'unknown-on-server'") {
		t.Errorf("result = %+v", r)
	}
}

func TestNewDryRun(t *testing.T) {
	e := newEnv(t)
	dest := filepath.Join(e.work, "dry")
	r := e.mustRun("new", "dry", "--deps", "web", "--dry-run", "--json", "--output", dest)
	var v struct {
		DryRun  bool
		Request struct {
			Type         string
			Values       map[string]string
			Dependencies []string
			URL          string
			Destination  string
		}
	}
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	if !v.DryRun || v.Request.Type != "gradle-project" || v.Request.Values["artifactId"] != "dry" ||
		!strings.Contains(v.Request.URL, "/starter.zip?") || v.Request.Destination != dest {
		t.Errorf("dry run = %+v", v)
	}
	for _, req := range e.srv.Requests() {
		if req.URL.Path != "/" {
			t.Error("dry run contacted the generation endpoint")
		}
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("dry run wrote files")
	}
	r = e.mustRun("new", "dry", "--dry-run", "--output", dest)
	if !strings.Contains(r.stdout, "Dry run") || !strings.Contains(r.stdout, "will be created") {
		t.Errorf("text dry run = %s", r.stdout)
	}
}

func TestOfflineBehaviour(t *testing.T) {
	e := newEnv(t)
	r := e.run("--offline", "deps")
	if r.code != ExitError || !strings.Contains(r.stderr, "no cached metadata") {
		t.Errorf("offline without cache: %+v", r)
	}
	e.mustRun("cache", "refresh")
	e.srv.Close()
	r = e.mustRun("--offline", "versions", "--json")
	if !strings.Contains(r.stdout, "4.1.2") {
		t.Errorf("offline versions = %s", r.stdout)
	}
	r = e.run("--offline", "new", "x")
	if r.code != ExitError || !strings.Contains(r.stderr, "requires a connection") {
		t.Errorf("offline new: %+v", r)
	}
}

func TestDepsCommands(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("deps", "search", "postgres")
	if !strings.HasPrefix(strings.TrimSpace(r.stdout), "postgresql") {
		t.Errorf("search = %s", r.stdout)
	}
	r = e.mustRun("deps", "list", "--category", "sql", "--json")
	var list []depView
	if err := json.Unmarshal([]byte(r.stdout), &list); err != nil || len(list) == 0 {
		t.Fatalf("json: %v %s", err, r.stdout)
	}
	for _, d := range list {
		if d.Category != "SQL" {
			t.Errorf("category filter leaked %s", d.ID)
		}
	}
	r = e.mustRun("deps", "--boot", "4.0.x", "--json")
	if strings.Contains(r.stdout, `"id": "vaadin"`) {
		t.Error("incompatible dependency listed with --boot")
	}
	r = e.mustRun("deps", "--boot", "4.0.x", "--all", "--json")
	if !strings.Contains(r.stdout, `"id": "vaadin"`) || !strings.Contains(r.stdout, `"compatible": false`) {
		t.Error("--all should include incompatible dependencies")
	}
	r = e.mustRun("deps", "info", "data-jpa")
	if !strings.Contains(r.stdout, "Spring Data JPA") || !strings.Contains(r.stdout, "spring-boot/4.1.2/reference") {
		t.Errorf("info = %s", r.stdout)
	}
	r = e.run("deps", "info", "data-jp")
	if r.code != ExitError || !strings.Contains(r.stderr, "did you mean data-jpa") {
		t.Errorf("unknown info = %+v", r)
	}
	r = e.run("deps", "--category", "nope")
	if r.code != ExitError || !strings.Contains(r.stderr, "unknown category") {
		t.Errorf("unknown category = %+v", r)
	}
}

func TestVersionsAndOptions(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("versions", "--stable", "--json")
	var vs []struct {
		ID     string
		Stable bool
	}
	json.Unmarshal([]byte(r.stdout), &vs)
	if len(vs) != 2 || vs[0].ID != "4.1.2" || vs[1].ID != "4.0.9" {
		t.Errorf("stable versions = %+v", vs)
	}
	r = e.mustRun("versions")
	if !strings.Contains(r.stdout, "release candidate") || !strings.Contains(r.stdout, "(default)") {
		t.Errorf("versions = %s", r.stdout)
	}
	r = e.mustRun("options")
	for _, want := range []string{"Project type", "maven-project", "Config format", "--config-format", "* = server default"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("options missing %q", want)
		}
	}
}

func TestPresetsWorkflow(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("presets", "list")
	if !strings.Contains(r.stdout, "rest-api") || !strings.Contains(r.stdout, "(example)") {
		t.Errorf("list = %s", r.stdout)
	}
	e.mustRun("presets", "save", "kt", "--language", "kotlin", "--java", "21", "--deps", "web,actuator")
	r = e.run("presets", "save", "kt", "--java", "17")
	if r.code == 0 {
		t.Error("overwriting a preset without --force should fail")
	}
	r = e.mustRun("presets", "show", "kt")
	if !strings.Contains(r.stdout, `java = "21"`) || !strings.Contains(r.stdout, "valid for the current server metadata") {
		t.Errorf("show = %s", r.stdout)
	}
	e.mustRun("presets", "edit", "kt", "--java", "25")
	e.mustRun("presets", "use", "kt", "svc", "--deps", "security", "--output", filepath.Join(e.work, "svc"))
	q := e.srv.LastQuery()
	if q.Get("language") != "kotlin" || q.Get("javaVersion") != "25" || q.Get("dependencies") != "web,actuator,security" {
		t.Errorf("preset query = %v", q)
	}
	e.mustRun("new", "svc2", "--preset", "rest-api", "--output", filepath.Join(e.work, "svc2"))
	if q := e.srv.LastQuery(); q.Get("dependencies") != "web,validation,actuator" {
		t.Errorf("builtin preset deps = %v", q.Get("dependencies"))
	}

	// Outdated presets are explained, not silently ignored.
	e.mustRun("presets", "save", "old", "--java", "11", "--deps", "vaadin", "--boot", "4.0.x")
	r = e.run("new", "o", "--preset", "old", "--output", filepath.Join(e.work, "o"))
	if r.code != ExitError || !strings.Contains(r.stderr, `(from preset "old")`) {
		t.Errorf("outdated preset: %+v", r)
	}
	r = e.mustRun("presets", "show", "old", "--json")
	if !strings.Contains(r.stdout, `"valid": false`) {
		t.Errorf("show json = %s", r.stdout)
	}
	e.mustRun("presets", "save", "last", "--from-last")
	e.mustRun("presets", "delete", "kt")
	if r := e.run("presets", "show", "kt"); r.code == 0 {
		t.Error("deleted preset still shown")
	}
	if r := e.run("presets", "delete", "minimal"); r.code == 0 {
		t.Error("deleting a builtin should fail")
	}
}

func TestConfigCommands(t *testing.T) {
	e := newEnv(t)
	e.mustRun("config", "set", "default-language", "kotlin")
	e.mustRun("config", "set", "default-build", "maven-project")
	e.mustRun("config", "set", "default-group", "com.example")
	e.mustRun("config", "set", "default-editor", "hx")
	e.mustRun("config", "set", "output-dir", e.work)
	r := e.mustRun("config", "show")
	if !strings.Contains(r.stdout, "default-language") || !strings.Contains(r.stdout, "kotlin") {
		t.Errorf("show = %s", r.stdout)
	}
	if r := e.mustRun("config", "get", "default-type"); strings.TrimSpace(r.stdout) != "maven-project" {
		t.Errorf("get = %q", r.stdout)
	}
	if r := e.run("config", "set", "nope", "x"); r.code != ExitUsage {
		t.Errorf("unknown key: %+v", r)
	}
	// Config defaults apply to generation; output-dir is the parent.
	e.mustRun("new", "cfgd")
	q := e.srv.LastQuery()
	if q.Get("language") != "kotlin" || q.Get("type") != "maven-project" {
		t.Errorf("config defaults not applied: %v", q)
	}
	if _, err := os.Stat(filepath.Join(e.work, "cfgd", "pom.xml")); err != nil {
		t.Error(err)
	}
	if r := e.run("config", "reset"); r.code != ExitUsage {
		t.Errorf("reset without --yes in non-tty: %+v", r)
	}
	e.mustRun("config", "reset", "--yes")
	if r := e.mustRun("config", "get", "default-language"); strings.TrimSpace(r.stdout) != "" {
		t.Errorf("not reset: %q", r.stdout)
	}
}

func TestCacheCommands(t *testing.T) {
	e := newEnv(t)
	r := e.mustRun("cache", "status", "--json")
	if !strings.Contains(r.stdout, `"cached": false`) {
		t.Errorf("status = %s", r.stdout)
	}
	e.mustRun("cache", "refresh")
	r = e.mustRun("cache", "status")
	if !strings.Contains(r.stdout, "fresh") {
		t.Errorf("status = %s", r.stdout)
	}
	r = e.mustRun("cache", "clear")
	if !strings.Contains(r.stdout, "Removed 1") {
		t.Errorf("clear = %s", r.stdout)
	}
}

func TestDoctor(t *testing.T) {
	e := newEnv(t)
	r := e.run("doctor", "--json")
	var checks []check
	if err := json.Unmarshal([]byte(r.stdout), &checks); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	names := map[string]string{}
	for _, c := range checks {
		names[c.Name] = c.Status
	}
	if names["Server"] != "ok" || names["Config"] != "ok" {
		t.Errorf("checks = %+v", checks)
	}
	for _, n := range []string{"Java", "Git", "Maven", "Gradle", "Editor", "Cache"} {
		if _, ok := names[n]; !ok {
			t.Errorf("missing check %s", n)
		}
	}
	e.srv.Close()
	if r := e.run("doctor"); r.code != ExitError {
		t.Errorf("doctor with server down should fail: %+v", r)
	}
}

func TestCompletion(t *testing.T) {
	e := newEnv(t)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		r := e.mustRun("completion", sh)
		if len(r.stdout) < 100 {
			t.Errorf("%s completion too short", sh)
		}
	}
	r := e.mustRun("__complete", "new", "x", "--deps", "web,data-j")
	if !strings.Contains(r.stdout, "web,data-jpa") {
		t.Errorf("dependency completion = %s", r.stdout)
	}
	r = e.mustRun("__complete", "new", "--java", "")
	if !strings.Contains(r.stdout, "21") {
		t.Errorf("java completion = %s", r.stdout)
	}
	r = e.mustRun("__complete", "new", "--boot", "")
	if !strings.Contains(r.stdout, "4.1.x") {
		t.Errorf("boot completion = %s", r.stdout)
	}
	r = e.mustRun("__complete", "presets", "show", "")
	if !strings.Contains(r.stdout, "rest-api") {
		t.Errorf("preset completion = %s", r.stdout)
	}
	r = e.mustRun("__complete", "config", "set", "default-")
	if !strings.Contains(r.stdout, "default-java") {
		t.Errorf("config key completion = %s", r.stdout)
	}
}

func TestVersionCommand(t *testing.T) {
	e := newEnv(t)
	if r := e.mustRun("--version"); !strings.HasPrefix(r.stdout, "sprout ") {
		t.Errorf("--version = %q", r.stdout)
	}
	if r := e.mustRun("version", "--json"); !strings.Contains(r.stdout, `"version"`) {
		t.Errorf("version = %q", r.stdout)
	}
}
