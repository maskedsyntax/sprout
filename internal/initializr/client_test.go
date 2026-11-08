package initializr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetchMetadataHeaders(t *testing.T) {
	var gotAccept, gotUA, gotINM string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotUA, gotINM = r.Header.Get("Accept"), r.Header.Get("User-Agent"), r.Header.Get("If-None-Match")
		if gotINM == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.initializr.v2.3+json")
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte(`{"type":{}}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL+"/", "Sprout/test")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.FetchMetadata(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotAccept, "application/vnd.initializr.v2.3+json") || gotUA != "Sprout/test" {
		t.Errorf("headers: accept=%q ua=%q", gotAccept, gotUA)
	}
	if resp.ETag != `"v1"` || string(resp.Body) != `{"type":{}}` {
		t.Errorf("resp = %+v", resp)
	}
	resp, err = c.FetchMetadata(context.Background(), `"v1"`)
	if err != nil || !resp.NotModified {
		t.Errorf("conditional request: %+v %v", resp, err)
	}
}

func TestFetchMetadataErrors(t *testing.T) {
	cases := map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"http 500": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"status":500,"error":"Internal Server Error","message":"boom"}`))
		}, "server returned 500: boom"},
		"html page": {func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html></html>"))
		}, "did not return Initializr metadata"},
		"404 plain": {func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}, "server returned 404"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			c, _ := New(srv.URL, "x")
			_, err := c.FetchMetadata(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchMetadataTimeoutAndUnreachable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	c, _ := New(srv.URL, "x")
	c.MetadataTimeout = 50 * time.Millisecond
	_, err := c.FetchMetadata(context.Background(), "")
	var ne *NetworkError
	if !errors.As(err, &ne) || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout err = %v", err)
	}

	c, _ = New("http://127.0.0.1:1", "x")
	_, err = c.FetchMetadata(context.Background(), "")
	if !errors.As(err, &ne) || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("unreachable err = %v", err)
	}
}

func TestGenerateAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Unknown dependency 'nope' check project metadata"}`))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "x")
	_, err := c.Generate(context.Background(), "/starter.zip", url.Values{"dependencies": {"nope"}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadRequest || !strings.Contains(ae.Message, "Unknown dependency") {
		t.Errorf("err = %v", err)
	}
}

func TestActionURL(t *testing.T) {
	c, _ := New("https://example.com/initializr/", "x")
	got, err := c.ActionURL("/starter.zip", url.Values{"type": {"maven-project"}, "dependencies": {"web,data-jpa"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/initializr/starter.zip?dependencies=web%2Cdata-jpa&type=maven-project" {
		t.Errorf("ActionURL = %s", got)
	}
	got, _ = c.ActionURL("https://other.example/gen.zip?type=x", url.Values{"a": {"b"}})
	if got != "https://other.example/gen.zip?a=b&type=x" {
		t.Errorf("absolute ActionURL = %s", got)
	}
}

func TestParseBaseURL(t *testing.T) {
	for _, bad := range []string{"ftp://x", "start.spring.io", "http://", "::"} {
		if _, err := ParseBaseURL(bad); err == nil {
			t.Errorf("ParseBaseURL(%q) succeeded", bad)
		}
	}
	u, err := ParseBaseURL("")
	if err != nil || u.String() != DefaultBaseURL {
		t.Errorf("default = %v %v", u, err)
	}
}
