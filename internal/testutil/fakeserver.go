// Package testutil provides a fake Spring Initializr server for tests.
package testutil

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Fixture returns the recorded start.spring.io v2.3 metadata.
func Fixture(t testing.TB) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "metadata", "testdata", "start-spring-io-v2.3.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Server is a fake Initializr instance backed by httptest.
type Server struct {
	*httptest.Server
	Metadata []byte

	mu       sync.Mutex
	requests []*http.Request
	// Starter overrides the /starter.zip handler when set.
	Starter http.HandlerFunc
	// Coordinates is served by /dependencies (id -> groupId:artifactId).
	Coordinates map[string]string
	// BuildFiles maps "type|sorted,deps" to a build file returned (as the
	// only file of the archive) by /starter.zip, emulating reference
	// generation for 'sprout add/remove'.
	BuildFiles map[string]string
}

// BuildFileKey is the BuildFiles key for a project type and dependencies.
func BuildFileKey(projectType string, deps ...string) string {
	sorted := append([]string(nil), deps...)
	sort.Strings(sorted)
	return projectType + "|" + strings.Join(sorted, ",")
}

// NewServer starts a fake server that serves the fixture metadata and
// generates small ZIP archives describing the request.
func NewServer(t testing.TB) *Server {
	s := &Server{Metadata: Fixture(t)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleMetadata)
	mux.HandleFunc("/starter.zip", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if s.Starter != nil {
			s.Starter(w, r)
			return
		}
		s.handleStarter(w, r)
	})
	mux.HandleFunc("/dependencies", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		deps := map[string]map[string]string{}
		for id, ga := range s.Coordinates {
			g, a, _ := strings.Cut(ga, ":")
			deps[id] = map[string]string{"groupId": g, "artifactId": a, "scope": "compile"}
		}
		w.Header().Set("Content-Type", "application/vnd.initializr.v2.2+json")
		json.NewEncoder(w).Encode(map[string]any{"bootVersion": r.URL.Query().Get("bootVersion"), "dependencies": deps})
	})
	mux.HandleFunc("/pom.xml", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="pom.xml"`)
		fmt.Fprintf(w, "<project><artifactId>%s</artifactId></project>\n", r.URL.Query().Get("artifactId"))
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *Server) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
}

// Requests returns the requests received so far.
func (s *Server) Requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.requests...)
}

// LastQuery returns the query of the last generation request.
func (s *Server) LastQuery() url.Values {
	reqs := s.Requests()
	for _, req := range slices.Backward(reqs) {
		if req.URL.Path != "/" {
			return req.URL.Query()
		}
	}
	return nil
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.record(r)
	if !strings.Contains(r.Header.Get("Accept"), "application/vnd.initializr.v2.3+json") {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "curl help text")
		return
	}
	etag := `"fixture"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/vnd.initializr.v2.3+json")
	w.Write(s.Metadata)
}

func (s *Server) handleStarter(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if s.BuildFiles != nil {
		var deps []string
		if d := q.Get("dependencies"); d != "" {
			deps = strings.Split(d, ",")
		}
		content, ok := s.BuildFiles[BuildFileKey(q.Get("type"), deps...)]
		if !ok {
			jsonError(w, http.StatusBadRequest, "no reference build for "+BuildFileKey(q.Get("type"), deps...))
			return
		}
		name := map[string]string{"maven-project": "pom.xml", "gradle-project": "build.gradle", "gradle-project-kotlin": "build.gradle.kts"}[q.Get("type")]
		w.Header().Set("Content-Type", "application/zip")
		w.Write(BuildZip(map[string]string{name: content}, nil))
		return
	}
	if deps := q.Get("dependencies"); deps != "" {
		for d := range strings.SplitSeq(deps, ",") {
			if d == "unknown-on-server" {
				jsonError(w, http.StatusBadRequest, fmt.Sprintf("Unknown dependency '%s' check project metadata", d))
				return
			}
		}
	}
	files := map[string]string{
		"pom.xml":           "<project/>",
		"HELP.md":           "# Help",
		"src/main/App.java": "class App {}",
		"request.json":      mustJSON(q),
	}
	exec := map[string]bool{"mvnw": true}
	files["mvnw"] = "#!/bin/sh\necho mvnw\n"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="demo.zip"`)
	w.Write(BuildZip(files, exec))
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"status": status, "error": http.StatusText(status), "message": msg})
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// BuildZip creates an archive with directory entries for parents. Files
// listed in exec get mode 0755.
func BuildZip(files map[string]string, exec map[string]bool) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	dirs := map[string]bool{}
	for _, n := range names {
		for d := filepath.ToSlash(filepath.Dir(n)); d != "." && !dirs[d]; d = filepath.ToSlash(filepath.Dir(d)) {
			dirs[d] = true
			h := &zip.FileHeader{Name: d + "/"}
			h.SetMode(0o755 | os.ModeDir)
			zw.CreateHeader(h)
		}
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		mode := os.FileMode(0o644)
		if exec[n] {
			mode = 0o755
		}
		h.SetMode(mode)
		f, _ := zw.CreateHeader(h)
		f.Write([]byte(files[n]))
	}
	zw.Close()
	return buf.Bytes()
}
