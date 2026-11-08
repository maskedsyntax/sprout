package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/testutil"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newLoader(t *testing.T, url string, clk *clock) *Loader {
	t.Helper()
	c, err := initializr.New(url, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &Loader{Client: c, Store: Store{Dir: t.TempDir()}, TTL: time.Hour, Now: clk.now}
}

func countMetadataRequests(srv *testutil.Server) int {
	n := 0
	for _, r := range srv.Requests() {
		if r.URL.Path == "/" {
			n++
		}
	}
	return n
}

func TestLoaderCachesAndExpires(t *testing.T) {
	srv := testutil.NewServer(t)
	clk := &clock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := newLoader(t, srv.URL, clk)
	ctx := context.Background()

	res, err := l.Load(ctx, false)
	if err != nil || res.Source != FromNetwork {
		t.Fatalf("first load: %+v %v", res, err)
	}
	res, err = l.Load(ctx, false)
	if err != nil || res.Source != FromCache || res.Stale {
		t.Fatalf("second load: %+v %v", res, err)
	}
	if n := countMetadataRequests(srv); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}

	// After the TTL a conditional request is made (304 from the fake).
	clk.t = clk.t.Add(2 * time.Hour)
	res, err = l.Load(ctx, false)
	if err != nil || res.Source != FromNetwork || !res.FetchedAt.Equal(clk.t) {
		t.Fatalf("expired load: %+v %v", res, err)
	}
	reqs := srv.Requests()
	if inm := reqs[len(reqs)-1].Header.Get("If-None-Match"); inm != `"fixture"` {
		t.Errorf("If-None-Match = %q", inm)
	}

	// Forced refresh always contacts the server.
	if _, err := l.Load(ctx, true); err != nil {
		t.Fatal(err)
	}
	if n := countMetadataRequests(srv); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestLoaderFallsBackToStaleCache(t *testing.T) {
	srv := testutil.NewServer(t)
	clk := &clock{time.Now()}
	l := newLoader(t, srv.URL, clk)
	if _, err := l.Load(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	clk.t = clk.t.Add(48 * time.Hour)
	res, err := l.Load(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stale || res.RefreshErr == nil || res.Source != FromCache {
		t.Errorf("res = %+v", res)
	}
}

func TestLoaderOffline(t *testing.T) {
	srv := testutil.NewServer(t)
	clk := &clock{time.Now()}
	l := newLoader(t, srv.URL, clk)
	l.Offline = true
	if _, err := l.Load(context.Background(), false); err == nil {
		t.Fatal("offline without cache should fail")
	}
	if countMetadataRequests(srv) != 0 {
		t.Error("offline mode contacted the server")
	}
	l.Offline = false
	l.Load(context.Background(), false)
	l.Offline = true
	clk.t = clk.t.Add(100 * time.Hour)
	res, err := l.Load(context.Background(), false)
	if err != nil || !res.Stale {
		t.Errorf("offline stale: %+v %v", res, err)
	}
}

func TestLoaderNoCacheServerDown(t *testing.T) {
	srv := testutil.NewServer(t)
	srv.Close()
	l := newLoader(t, srv.URL, &clock{time.Now()})
	if _, err := l.Load(context.Background(), false); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoaderBadMetadataKeepsCache(t *testing.T) {
	srv := testutil.NewServer(t)
	clk := &clock{time.Now()}
	l := newLoader(t, srv.URL, clk)
	l.Load(context.Background(), false)
	srv.Metadata = []byte(`{"not":"initializr"}`)
	l.Store.Clear()
	if _, err := l.Load(context.Background(), false); err == nil {
		t.Error("malformed metadata accepted")
	}
}

func TestStoreCorruptAndClear(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Save(&Entry{URL: "https://a", FetchedAt: time.Now(), Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&Entry{URL: "https://b", FetchedAt: time.Now(), Metadata: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	e, err := s.Load("https://a")
	if err != nil || e.URL != "https://a" {
		t.Fatalf("load: %+v %v", e, err)
	}
	os.WriteFile(e.Path, []byte("garbage"), 0o644)
	if _, err := s.Load("https://a"); err == nil {
		t.Error("corrupt cache accepted")
	}
	if list, _ := s.List(); len(list) != 2 {
		t.Errorf("list = %d", len(list))
	}
	if n, err := s.Clear(); err != nil || n != 2 {
		t.Errorf("clear = %d %v", n, err)
	}
}
