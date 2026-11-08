// Package initializr is an HTTP client for Spring Initializr compatible
// services. It is independent of any terminal UI.
package initializr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// DefaultBaseURL is the public Spring Initializr instance.
const DefaultBaseURL = "https://start.spring.io"

// MetadataAccept negotiates the newest metadata format first. v2.3 uses
// semantic version ranges; older formats are still understood.
const MetadataAccept = "application/vnd.initializr.v2.3+json, application/vnd.initializr.v2.2+json;q=0.9, application/vnd.initializr.v2.1+json;q=0.8"

const (
	maxMetadataBytes = 16 << 20
	maxErrorBytes    = 64 << 10
)

// Client talks to a Spring Initializr service.
type Client struct {
	BaseURL   *url.URL
	UserAgent string
	HTTP      *http.Client
	// MetadataTimeout bounds metadata requests. Zero means 20s.
	MetadataTimeout time.Duration
}

// New creates a client for baseURL.
func New(baseURL, userAgent string) (*Client, error) {
	u, err := ParseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{BaseURL: u, UserAgent: userAgent, HTTP: &http.Client{}}, nil
}

// ParseBaseURL validates and normalizes a server URL.
func ParseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid server URL %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q: missing host", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

// APIError is a non-2xx response from the server.
type APIError struct {
	StatusCode int
	Message    string
	URL        string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, msg)
}

// NetworkError wraps transport failures with a friendlier message.
type NetworkError struct {
	Host string
	Err  error
}

func (e *NetworkError) Error() string {
	var ne net.Error
	if errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &ne) && ne.Timeout()) {
		return fmt.Sprintf("request to %s timed out", e.Host)
	}
	return fmt.Sprintf("cannot reach %s: %v", e.Host, e.Err)
}

func (e *NetworkError) Unwrap() error { return e.Err }

// MetadataResponse is the raw metadata document with caching headers.
type MetadataResponse struct {
	Body        []byte
	ContentType string
	ETag        string
	NotModified bool
}

// FetchMetadata downloads the service metadata from the root endpoint. When
// etag is non-empty a conditional request is made and NotModified reports a
// 304 response.
func (c *Client) FetchMetadata(ctx context.Context, etag string) (*MetadataResponse, error) {
	timeout := c.MetadataTimeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := c.newRequest(ctx, c.BaseURL.String()+"/")
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", MetadataAccept)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return &MetadataResponse{NotModified: true, ETag: etag}, nil
	}
	if resp.StatusCode/100 != 2 {
		return nil, readAPIError(resp)
	}
	ct := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	if !strings.HasPrefix(mt, "application/vnd.initializr.") && mt != "application/json" && !strings.HasSuffix(mt, "+json") {
		return nil, fmt.Errorf("%s did not return Initializr metadata (content type %q); is this a Spring Initializr server?", c.BaseURL.Host, ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, &NetworkError{Host: c.BaseURL.Host, Err: err}
	}
	if len(body) > maxMetadataBytes {
		return nil, fmt.Errorf("metadata from %s exceeds %d MB", c.BaseURL.Host, maxMetadataBytes>>20)
	}
	return &MetadataResponse{Body: body, ContentType: ct, ETag: resp.Header.Get("ETag")}, nil
}

// Download is an in-flight generation response. The caller must close Body.
type Download struct {
	Body        io.ReadCloser
	ContentType string
	Filename    string
	Size        int64
}

// Generate requests a project from the generation endpoint (action) with
// the given query parameters. The action is a path such as "/starter.zip"
// supplied by the metadata, or an absolute URL.
func (c *Client) Generate(ctx context.Context, action string, query url.Values) (*Download, error) {
	target, err := c.ActionURL(action, query)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, target)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream, */*;q=0.5")
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		return nil, readAPIError(resp)
	}
	d := &Download{Body: resp.Body, ContentType: resp.Header.Get("Content-Type"), Size: resp.ContentLength}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		d.Filename = path.Base(strings.ReplaceAll(params["filename"], `\`, "/"))
	}
	return d, nil
}

// ActionURL resolves a generation action against the base URL.
func (c *Client) ActionURL(action string, query url.Values) (string, error) {
	if action == "" {
		action = "/starter.zip"
	}
	ref, err := url.Parse(action)
	if err != nil {
		return "", fmt.Errorf("invalid generation action %q: %w", action, err)
	}
	var u *url.URL
	if ref.IsAbs() {
		u = ref
	} else {
		cp := *c.BaseURL
		cp.Path = c.BaseURL.Path + "/" + strings.TrimLeft(ref.Path, "/")
		u = &cp
	}
	q := u.Query()
	maps.Copy(q, query)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) newRequest(ctx context.Context, target string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, &NetworkError{Host: req.URL.Host, Err: err}
	}
	return resp, nil
}

// readAPIError extracts the "message" from Spring's JSON error body.
func readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	e := &APIError{StatusCode: resp.StatusCode, URL: resp.Request.URL.String()}
	var payload struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		e.Message = payload.Message
		if e.Message == "" {
			e.Message = payload.Error
		}
	}
	if e.Message == "" {
		text := strings.TrimSpace(string(body))
		if len(text) > 0 && len(text) < 300 && !strings.HasPrefix(text, "<") {
			e.Message = text
		}
	}
	return e
}
