// Package cli implements Sprout's command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/buildinfo"
	"github.com/maskedsyntax/sprout/internal/cache"
	"github.com/maskedsyntax/sprout/internal/config"
	"github.com/maskedsyntax/sprout/internal/history"
	"github.com/maskedsyntax/sprout/internal/initializr"
	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/presets"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitCanceled = 130
)

// App holds per-invocation dependencies. Directories default to the OS
// conventions; tests override them.
type App struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	ConfigDir      string
	CacheDir       string
	StateDir       string
	// Interactive forces TTY detection on (true) or off (false); nil detects.
	Interactive *bool
	Now         func() time.Time

	// Global flags.
	server  string
	offline bool
	noColor bool

	cfg *config.Config
	ui  *ui
}

// usageError marks errors caused by invalid invocation.
type usageError struct{ error }

func (e usageError) Unwrap() error { return e.error }

func usageErrorf(format string, a ...any) error { return usageError{fmt.Errorf(format, a...)} }

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, app *App, args []string) int {
	if app.Stdin == nil {
		app.Stdin = os.Stdin
	}
	if app.Stdout == nil {
		app.Stdout = os.Stdout
	}
	if app.Stderr == nil {
		app.Stderr = os.Stderr
	}
	root := NewRootCommand(app)
	root.SetArgs(args)
	root.SetIn(app.Stdin)
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	u := app.ui
	if u == nil {
		u = newUI(app.Stdout, app.Stderr, true)
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, huh.ErrUserAborted):
		fmt.Fprintln(app.Stderr, "Cancelled.")
		return ExitCanceled
	case errors.As(err, new(usageError)):
		fmt.Fprintf(app.Stderr, "%s %v\n", u.errc("error:"), err)
		fmt.Fprintf(app.Stderr, "Run '%s --help' for usage.\n", root.Name())
		return ExitUsage
	}
	fmt.Fprintf(app.Stderr, "%s %v\n", u.errc("error:"), err)
	return ExitError
}

// NewRootCommand builds the command tree.
func NewRootCommand(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "sprout",
		Short: "Generate Spring Boot projects from your terminal",
		Long: `Sprout is a fast, interactive CLI for generating Spring Boot projects,
powered by Spring Initializr (https://start.spring.io).

All options (project types, languages, Spring Boot versions, Java versions,
dependencies, ...) are discovered from the server's metadata at runtime.

Sprout is an independent project and is not affiliated with or endorsed by
the Spring team.`,
		Version:           buildinfo.Get().String(),
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			app.ui = newUI(app.Stdout, app.Stderr, app.noColor)
			return nil
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	pf := root.PersistentFlags()
	pf.StringVar(&app.server, "server", "", "Spring Initializr base URL (default from config, else "+initializr.DefaultBaseURL+")")
	pf.BoolVar(&app.offline, "offline", false, "use cached metadata only; never contact the server")
	pf.BoolVar(&app.noColor, "no-color", false, "disable colored output (also honors NO_COLOR)")

	root.AddGroup(&cobra.Group{ID: "main", Title: "Project commands:"}, &cobra.Group{ID: "manage", Title: "Management commands:"})
	for _, c := range []*cobra.Command{newNewCmd(app), newDepsCmd(app), newVersionsCmd(app), newOptionsCmd(app)} {
		c.GroupID = "main"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newPresetsCmd(app), newConfigCmd(app), newCacheCmd(app), newHistoryCmd(app), newDoctorCmd(app), newVersionCmd(app)} {
		c.GroupID = "manage"
		root.AddCommand(c)
	}
	root.SetHelpCommandGroupID("manage")
	root.SetCompletionCommandGroupID("manage")
	return root
}

func newVersionCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := buildinfo.Get()
			if format.json() {
				return writeJSON(app.Stdout, info)
			}
			app.ui.println(info.String())
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) configDir() (string, error) {
	if a.ConfigDir != "" {
		return a.ConfigDir, nil
	}
	return platform.ConfigDir()
}

func (a *App) configPath() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, config.FileName), nil
}

// config loads (once) the global configuration.
func (a *App) config() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	p, err := a.configPath()
	if err != nil {
		return nil, err
	}
	c, err := config.Load(p)
	if err != nil {
		return nil, err
	}
	a.cfg = c
	return c, nil
}

func (a *App) presetStore() (presets.Store, error) {
	dir, err := a.configDir()
	if err != nil {
		return presets.Store{}, err
	}
	return presets.Store{Path: filepath.Join(dir, presets.FileName)}, nil
}

func (a *App) historyStore() (history.Store, error) {
	dir := a.StateDir
	if dir == "" {
		var err error
		if dir, err = platform.StateDir(); err != nil {
			return history.Store{}, err
		}
	}
	return history.Store{Path: filepath.Join(dir, history.FileName)}, nil
}

func (a *App) cacheStore() (cache.Store, error) {
	dir := a.CacheDir
	if dir == "" {
		var err error
		if dir, err = platform.CacheDir(); err != nil {
			return cache.Store{}, err
		}
	}
	return cache.Store{Dir: dir}, nil
}

func (a *App) serverURL() (string, error) {
	if a.server != "" {
		return a.server, nil
	}
	if env := os.Getenv("SPROUT_SERVER"); env != "" {
		return env, nil
	}
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	return cfg.ServerURL(), nil
}

func (a *App) client() (*initializr.Client, error) {
	s, err := a.serverURL()
	if err != nil {
		return nil, err
	}
	return initializr.New(s, buildinfo.UserAgent())
}

func (a *App) loader() (*cache.Loader, error) {
	c, err := a.client()
	if err != nil {
		return nil, err
	}
	store, err := a.cacheStore()
	if err != nil {
		return nil, err
	}
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	return &cache.Loader{Client: c, Store: store, TTL: cfg.TTL(), Offline: a.offline, Now: a.Now}, nil
}

// metadata loads metadata via the cache, warning when stale data is used.
func (a *App) metadata(ctx context.Context, refresh bool) (*cache.Result, error) {
	l, err := a.loader()
	if err != nil {
		return nil, err
	}
	res, err := l.Load(ctx, refresh)
	if err != nil {
		return nil, err
	}
	if res.RefreshErr != nil {
		a.ui.warn("%v; using cached metadata from %s", res.RefreshErr, humanAge(a.now().Sub(res.FetchedAt)))
	} else if res.Stale {
		a.ui.warn("offline: using cached metadata from %s", humanAge(a.now().Sub(res.FetchedAt)))
	}
	return res, nil
}

// interactive reports whether prompts may be shown.
func (a *App) interactive() bool {
	if a.Interactive != nil {
		return *a.Interactive
	}
	in, ok1 := a.Stdin.(*os.File)
	out, ok2 := a.Stdout.(*os.File)
	return ok1 && ok2 && platform.IsTerminal(in) && platform.IsTerminal(out)
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// formatFlag implements --format text|json with a --json shorthand.
type formatFlag struct {
	value    string
	jsonFlag bool
}

func (f *formatFlag) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.value, "format", "text", "output format: text or json")
	cmd.Flags().BoolVar(&f.jsonFlag, "json", false, "shorthand for --format json")
	_ = cmd.RegisterFlagCompletionFunc("format", cobra.FixedCompletions([]string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp))
	prev := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if f.value != "text" && f.value != "json" {
			return usageErrorf("invalid --format %q: use text or json", f.value)
		}
		if prev != nil {
			return prev(c, args)
		}
		return nil
	}
}

func (f *formatFlag) json() bool { return f.jsonFlag || f.value == "json" }
