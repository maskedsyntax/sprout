//go:build integration

// Live tests against a real Spring Initializr instance. Run with:
//
//	go test -tags integration ./internal/generator/
//
// SPROUT_IT_SERVER overrides the server (default https://start.spring.io).
package generator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maskedsyntax/sprout/internal/buildinfo"
	"github.com/maskedsyntax/sprout/internal/generator"
	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

func TestLiveGeneration(t *testing.T) {
	server := os.Getenv("SPROUT_IT_SERVER")
	if server == "" {
		server = initializr.DefaultBaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := initializr.New(server, buildinfo.UserAgent())
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.FetchMetadata(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	m, err := metadata.Parse(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.BootVersions()) == 0 || len(m.Dependencies()) == 0 {
		t.Fatal("metadata has no versions or dependencies")
	}

	for _, typ := range []string{"maven", "gradle-kotlin"} {
		t.Run(typ, func(t *testing.T) {
			req, err := project.Resolve(m, project.Options{ArtifactID: "live-demo", Type: typ, Dependencies: []string{"web"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "live-demo")
			res, err := generator.Generate(ctx, c, m, req, generator.Options{Dest: dest})
			if err != nil {
				t.Fatal(err)
			}
			want := "pom.xml"
			if typ != "maven" {
				want = "build.gradle.kts"
			}
			if _, err := os.Stat(filepath.Join(res.Dir, want)); err != nil {
				t.Errorf("missing %s: %v", want, err)
			}
			if _, err := os.Stat(filepath.Join(res.Dir, "src", "main", "java", "com", "example", "live_demo")); err != nil {
				t.Errorf("package directory: %v", err)
			}
		})
	}
}
