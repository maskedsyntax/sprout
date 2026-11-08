// Package cache stores Spring Initializr metadata on disk and decides when
// to refresh it.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/platform"
)

// DefaultTTL is how long cached metadata is considered fresh.
const DefaultTTL = 12 * time.Hour

// Entry is cached metadata for one server.
type Entry struct {
	URL       string          `json:"url"`
	FetchedAt time.Time       `json:"fetchedAt"`
	ETag      string          `json:"etag,omitempty"`
	Metadata  json.RawMessage `json:"metadata"`
	Path      string          `json:"-"`
	Size      int64           `json:"-"`
}

// Age returns how long ago the entry was fetched.
func (e *Entry) Age(now time.Time) time.Duration { return now.Sub(e.FetchedAt) }

// Store is a directory of cached metadata files.
type Store struct {
	Dir string
}

func (s Store) path(serverURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(serverURL, "/")))
	return filepath.Join(s.Dir, "metadata-"+hex.EncodeToString(sum[:8])+".json")
}

// Load returns the cached entry for a server or an error wrapping
// fs.ErrNotExist.
func (s Store) Load(serverURL string) (*Entry, error) {
	p := s.path(serverURL)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil || len(e.Metadata) == 0 {
		return nil, fmt.Errorf("cache file %s is corrupt; run 'sprout cache clear': %w", p, errors.Join(err, fs.ErrInvalid))
	}
	e.Path, e.Size = p, int64(len(data))
	return &e, nil
}

// Save writes an entry atomically.
func (s Store) Save(e *Entry) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	p := s.path(e.URL)
	if err := platform.WriteFileAtomic(p, data, 0o644); err != nil {
		return fmt.Errorf("write metadata cache: %w", err)
	}
	e.Path, e.Size = p, int64(len(data))
	return nil
}

// List returns every cached entry.
func (s Store) List() ([]*Entry, error) {
	matches, err := filepath.Glob(filepath.Join(s.Dir, "metadata-*.json"))
	if err != nil {
		return nil, err
	}
	var out []*Entry
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var e Entry
		if json.Unmarshal(data, &e) != nil {
			e = Entry{URL: "(corrupt)"}
		}
		e.Path, e.Size = m, int64(len(data))
		out = append(out, &e)
	}
	return out, nil
}

// Clear removes all cached metadata files and returns how many were removed.
func (s Store) Clear() (int, error) {
	matches, err := filepath.Glob(filepath.Join(s.Dir, "metadata-*.json"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, fmt.Errorf("remove %s: %w", m, err)
		}
		n++
	}
	return n, nil
}

// Source describes where loaded metadata came from.
type Source string

const (
	FromNetwork Source = "network"
	FromCache   Source = "cache"
)

// Result is loaded metadata plus provenance.
type Result struct {
	Metadata  *metadata.Metadata
	Source    Source
	FetchedAt time.Time
	// Stale is set when cached data past its TTL was used because the server
	// could not be reached (or offline mode was requested).
	Stale bool
	// RefreshErr is the network error that forced stale data to be used.
	RefreshErr error
}

// Loader combines the HTTP client and the cache.
type Loader struct {
	Client  *initializr.Client
	Store   Store
	TTL     time.Duration
	Offline bool
	Now     func() time.Time
}

func (l *Loader) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Load returns metadata, using fresh cache when possible. When refresh is
// true the cache is revalidated with the server. If the server is
// unavailable, stale cached data is returned with RefreshErr set.
func (l *Loader) Load(ctx context.Context, refresh bool) (*Result, error) {
	serverURL := l.Client.BaseURL.String()
	entry, cacheErr := l.Store.Load(serverURL)
	if cacheErr != nil && !errors.Is(cacheErr, fs.ErrNotExist) {
		entry = nil // corrupt cache: treat as missing and overwrite
	}
	ttl := l.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if entry != nil && (l.Offline || (!refresh && entry.Age(l.now()) < ttl)) {
		return l.fromEntry(entry, l.Offline && entry.Age(l.now()) >= ttl, nil)
	}
	if l.Offline {
		return nil, fmt.Errorf("offline mode: no cached metadata for %s; run 'sprout cache refresh' while online", serverURL)
	}

	etag := ""
	if entry != nil {
		etag = entry.ETag
	}
	resp, err := l.Client.FetchMetadata(ctx, etag)
	if err != nil {
		if entry != nil && !errors.Is(err, context.Canceled) {
			return l.fromEntry(entry, true, err)
		}
		return nil, fmt.Errorf("load metadata from %s: %w", serverURL, err)
	}
	if resp.NotModified && entry != nil {
		entry.FetchedAt = l.now()
		if err := l.Store.Save(entry); err != nil {
			return nil, err
		}
		res, err := l.fromEntry(entry, false, nil)
		if res != nil {
			res.Source = FromNetwork
		}
		return res, err
	}
	meta, err := metadata.Parse(resp.Body)
	if err != nil {
		if entry != nil {
			return l.fromEntry(entry, true, err)
		}
		return nil, fmt.Errorf("metadata from %s: %w", serverURL, err)
	}
	ne := &Entry{URL: serverURL, FetchedAt: l.now(), ETag: resp.ETag, Metadata: resp.Body}
	if err := l.Store.Save(ne); err != nil {
		return nil, err
	}
	return &Result{Metadata: meta, Source: FromNetwork, FetchedAt: ne.FetchedAt}, nil
}

func (l *Loader) fromEntry(e *Entry, stale bool, refreshErr error) (*Result, error) {
	meta, err := metadata.Parse(e.Metadata)
	if err != nil {
		return nil, fmt.Errorf("cached metadata is invalid; run 'sprout cache refresh': %w", errors.Join(err, refreshErr))
	}
	return &Result{Metadata: meta, Source: FromCache, FetchedAt: e.FetchedAt, Stale: stale, RefreshErr: refreshErr}, nil
}
