package generator

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
	"github.com/maskedsyntax/sprout/internal/testutil"
)

func setup(t *testing.T, opts project.Options) (*testutil.Server, *initializr.Client, *metadata.Metadata, *project.Request) {
	t.Helper()
	srv := testutil.NewServer(t)
	c, err := initializr.New(srv.URL, "sprout-test")
	if err != nil {
		t.Fatal(err)
	}
	m, err := metadata.Parse(srv.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	req, err := project.Resolve(m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, c, m, req
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sprout-tmp-") {
			t.Errorf("staging directory left behind: %s", e.Name())
		}
	}
}

func TestGenerate(t *testing.T) {
	srv, c, m, req := setup(t, project.Options{ArtifactID: "demo", Type: "maven", Dependencies: []string{"web"}})
	base := t.TempDir()
	dest := filepath.Join(base, "demo")
	res, err := Generate(context.Background(), c, m, req, Options{Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dir != dest || len(res.Files) != 5 {
		t.Errorf("result = %+v", res)
	}
	q := srv.LastQuery()
	if q.Get("type") != "maven-project" || q.Get("dependencies") != "web" || q.Get("artifactId") != "demo" {
		t.Errorf("query = %v", q)
	}
	if _, err := os.Stat(filepath.Join(dest, "src", "main", "App.java")); err != nil {
		t.Error(err)
	}
	assertNoLeftovers(t, base)

	// A second run must refuse the now non-empty destination.
	if _, err := Generate(context.Background(), c, m, req, Options{Dest: dest}); !errors.Is(err, ErrDestinationExists) {
		t.Errorf("expected ErrDestinationExists, got %v", err)
	}
}

func TestGenerateSingleBuildFile(t *testing.T) {
	_, c, m, req := setup(t, project.Options{ArtifactID: "demo", Type: "maven-build"})
	dest := filepath.Join(t.TempDir(), "out")
	res, err := Generate(context.Background(), c, m, req, Options{Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || !strings.Contains(read(t, filepath.Join(dest, "pom.xml")), "<artifactId>demo</artifactId>") {
		t.Errorf("result = %+v", res)
	}
}

func TestGenerateFailuresLeaveNothing(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message":"Dependency 'x' is not compatible with Spring Boot 4.0.9"}`))
		},
		"truncated download": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Length", "100000")
			w.Write(testutil.BuildZip(map[string]string{"a": "b"}, nil)[:50])
		},
		"not a zip": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>maintenance</html>"))
		},
		"zip slip": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			w.Write(testutil.BuildZip(map[string]string{"ok.txt": "x", "../../escape.txt": "x"}, nil))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv, c, m, req := setup(t, project.Options{ArtifactID: "demo", Type: "maven"})
			srv.Starter = h
			base := t.TempDir()
			dest := filepath.Join(base, "demo")
			_, err := Generate(context.Background(), c, m, req, Options{Dest: dest})
			if err == nil {
				t.Fatal("expected error")
			}
			if _, err := os.Stat(dest); err == nil {
				t.Error("destination created despite failure")
			}
			assertNoLeftovers(t, base)
			if name == "server error" && !strings.Contains(err.Error(), "not compatible") {
				t.Errorf("server message lost: %v", err)
			}
		})
	}
}

func TestGenerateCancellation(t *testing.T) {
	srv, c, m, req := setup(t, project.Options{ArtifactID: "demo"})
	ctx, cancel := context.WithCancel(context.Background())
	srv.Starter = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", "1000000")
		w.Write([]byte("PK\x03\x04"))
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}
	base := t.TempDir()
	dest := filepath.Join(base, "demo")
	_, err := Generate(ctx, c, m, req, Options{Dest: dest})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("destination created after cancellation")
	}
	assertNoLeftovers(t, base)
}

func TestGenerateForceMerge(t *testing.T) {
	_, c, m, req := setup(t, project.Options{ArtifactID: "demo"})
	dest := t.TempDir()
	tree(t, dest, map[string]string{"HELP.md": "mine", ".git/HEAD": "ref"})
	res, err := Generate(context.Background(), c, m, req, Options{Dest: dest, Force: true, Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Merged || len(res.BackedUp) != 1 || read(t, filepath.Join(res.BackupDir, "HELP.md")) != "mine" {
		t.Errorf("result = %+v", res)
	}
	if read(t, filepath.Join(dest, ".git", "HEAD")) != "ref" {
		t.Error(".git modified")
	}
}

func TestEndpoint(t *testing.T) {
	m := &metadata.Metadata{Links: map[string]metadata.Link{"x": {Href: "https://h/gen.zip?type=x{&dependencies}", Templated: true}}}
	if got := Endpoint(m, metadata.Option{ID: "x"}); got != "https://h/gen.zip?type=x" {
		t.Errorf("link endpoint = %q", got)
	}
	if got := Endpoint(m, metadata.Option{ID: "y", Action: "/starter.zip"}); got != "/starter.zip" {
		t.Errorf("action endpoint = %q", got)
	}
}
