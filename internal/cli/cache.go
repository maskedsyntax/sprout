package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/cache"
)

func newCacheCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage cached Initializr metadata",
		Long: `Sprout caches server metadata to start quickly. Cached data is refreshed
automatically once it is older than the cache TTL (default 12h; see
'sprout config set cache-ttl'). When the server is unreachable, stale cached
metadata is used for browsing commands such as 'deps' and 'versions'.
Generating a project always requires the server.`,
	}
	cmd.AddCommand(newCacheStatusCmd(app), newCacheRefreshCmd(app), newCacheClearCmd(app))
	return cmd
}

type cacheStatus struct {
	Server    string    `json:"server"`
	Cached    bool      `json:"cached"`
	Path      string    `json:"path,omitempty"`
	FetchedAt time.Time `json:"fetchedAt,omitzero"`
	Age       string    `json:"age,omitempty"`
	TTL       string    `json:"ttl"`
	Fresh     bool      `json:"fresh"`
	Size      int64     `json:"size,omitempty"`
	Error     string    `json:"error,omitempty"`
}

func (a *App) cacheStatus() (cacheStatus, error) {
	l, err := a.loader()
	if err != nil {
		return cacheStatus{}, err
	}
	ttl := l.TTL
	if ttl <= 0 {
		ttl = cache.DefaultTTL
	}
	st := cacheStatus{Server: l.Client.BaseURL.String(), TTL: ttl.String()}
	e, err := l.Store.Load(st.Server)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return st, nil
	case err != nil:
		st.Error = err.Error()
		return st, nil
	}
	age := e.Age(a.now())
	st.Cached, st.Path, st.FetchedAt, st.Size = true, e.Path, e.FetchedAt, e.Size
	st.Age, st.Fresh = humanAge(age), age < ttl
	return st, nil
}

func newCacheStatusCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show cache status for the configured server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := app.cacheStatus()
			if err != nil {
				return err
			}
			if format.json() {
				return writeJSON(app.Stdout, st)
			}
			u := app.ui
			u.kv("Server", 10, st.Server)
			if st.Error != "" {
				u.kv("Status", 10, u.errc(st.Error))
				return nil
			}
			if !st.Cached {
				u.kv("Status", 10, u.dim("not cached"))
				u.kv("TTL", 10, st.TTL)
				return nil
			}
			status := u.ok("fresh")
			if !st.Fresh {
				status = u.warnc("stale (refreshed on next use)")
			}
			u.kv("Status", 10, status)
			u.kv("Fetched", 10, fmt.Sprintf("%s (%s)", st.FetchedAt.Local().Format(time.RFC1123), st.Age))
			u.kv("TTL", 10, st.TTL)
			u.kv("File", 10, fmt.Sprintf("%s (%d KB)", st.Path, st.Size/1024))
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

func newCacheRefreshCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Download fresh metadata now",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.offline {
				return errors.New("cannot refresh in --offline mode")
			}
			l, err := app.loader()
			if err != nil {
				return err
			}
			res, err := l.Load(cmd.Context(), true)
			if err != nil {
				return err
			}
			if res.RefreshErr != nil {
				return fmt.Errorf("refresh failed: %w (cached metadata from %s kept)", res.RefreshErr, humanAge(app.now().Sub(res.FetchedAt)))
			}
			m := res.Metadata
			app.ui.printf("%s Metadata refreshed from %s: %d dependencies, %d Spring Boot versions\n",
				app.ui.ok("✓"), l.Client.BaseURL.Host, len(m.Dependencies()), len(m.BootVersions()))
			return nil
		},
	}
}

func newCacheClearCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete all cached metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.cacheStore()
			if err != nil {
				return err
			}
			n, err := store.Clear()
			if err != nil {
				return err
			}
			app.ui.printf("%s Removed %d cached metadata file(s)\n", app.ui.ok("✓"), n)
			return nil
		},
	}
}
