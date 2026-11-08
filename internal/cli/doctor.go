package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/platform"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok, warn, fail, info
	Detail string `json:"detail"`
}

func newDoctorCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the local environment and server connectivity",
		Long: `Check connectivity to the Spring Initializr server, configuration, cache
and local tools. Java, Maven, Gradle and Git are optional: generated
projects include build wrappers, and downloading a project needs none of them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runChecks(cmd.Context(), app)
			failed := false
			for _, c := range checks {
				failed = failed || c.Status == "fail"
			}
			if format.json() {
				if err := writeJSON(app.Stdout, checks); err != nil {
					return err
				}
			} else {
				u := app.ui
				for _, c := range checks {
					var icon string
					switch c.Status {
					case "ok":
						icon = u.ok("✓")
					case "warn":
						icon = u.warnc("!")
					case "fail":
						icon = u.errc("✗")
					default:
						icon = u.dim("·")
					}
					u.printf("%s %s %s\n", icon, pad(c.Name, 14), c.Detail)
				}
			}
			if failed {
				return fmt.Errorf("some checks failed")
			}
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

func runChecks(ctx context.Context, app *App) []check {
	var checks []check
	add := func(name, status, format string, a ...any) {
		checks = append(checks, check{name, status, fmt.Sprintf(format, a...)})
	}

	cfgPath, _ := app.configPath()
	cfg, err := app.config()
	if err != nil {
		add("Config", "fail", "%v", err)
	} else {
		add("Config", "ok", "%s", cfgPath)
	}

	client, err := app.client()
	switch {
	case err != nil:
		add("Server", "fail", "%v", err)
	case app.offline:
		add("Server", "info", "skipped (--offline)")
	default:
		start := time.Now()
		resp, err := client.FetchMetadata(ctx, "")
		switch {
		case err != nil:
			add("Server", "fail", "%s: %v", client.BaseURL, err)
		default:
			m, perr := metadata.Parse(resp.Body)
			if perr != nil {
				add("Server", "fail", "%s: %v", client.BaseURL, perr)
			} else {
				boot := ""
				if f := m.Field(metadata.FieldBootVersion); f != nil {
					boot = ", default Spring Boot " + f.Default
				}
				add("Server", "ok", "%s (%s, %d dependencies%s)", client.BaseURL, time.Since(start).Round(time.Millisecond), len(m.Dependencies()), boot)
				var unsupported []string
				for _, f := range m.Fields {
					switch f.Kind {
					case metadata.KindText, metadata.KindSingleSelect, metadata.KindAction:
					default:
						unsupported = append(unsupported, f.ID+" ("+string(f.Kind)+")")
					}
				}
				if len(unsupported) > 0 {
					add("Capabilities", "warn", "not supported by this Sprout version: %s", strings.Join(unsupported, ", "))
				}
			}
		}
	}

	st, err := app.cacheStatus()
	switch {
	case err != nil:
		add("Cache", "warn", "%v", err)
	case st.Error != "":
		add("Cache", "warn", "%s", st.Error)
	case !st.Cached:
		add("Cache", "info", "no cached metadata yet")
	default:
		fresh := "fresh"
		if !st.Fresh {
			fresh = "stale"
		}
		add("Cache", "ok", "%s, fetched %s", fresh, st.Age)
	}

	toolCheck := func(name, bin string, args []string, re *regexp.Regexp, missing string) {
		path, err := exec.LookPath(bin)
		if err != nil {
			add(name, "info", "%s", missing)
			return
		}
		ver := toolVersion(ctx, path, args, re)
		add(name, "ok", "%s %s", ver, platform.ShortPath(path))
	}
	toolCheck("Java", "java", []string{"-version"}, regexp.MustCompile(`version "([^"]+)"`),
		"not found (needed to build and run projects, not to generate them)")
	toolCheck("Git", "git", []string{"--version"}, regexp.MustCompile(`git version (\S+)`),
		"not found (only needed for --git)")
	toolCheck("Maven", "mvn", []string{"--version"}, regexp.MustCompile(`Apache Maven (\S+)`),
		"not found (optional: Maven projects include ./mvnw)")
	toolCheck("Gradle", "gradle", []string{"--version"}, regexp.MustCompile(`Gradle (\S+)`),
		"not found (optional: Gradle projects include ./gradlew)")

	editorCmd := ""
	if cfg != nil {
		editorCmd = platform.ResolveEditor("", cfg.Editor)
	}
	if editorCmd == "" {
		add("Editor", "info", "not configured (set with 'sprout config set editor <cmd>' or $EDITOR)")
	} else {
		args, err := platform.SplitCommand(editorCmd)
		if err != nil {
			add("Editor", "warn", "%q: %v", editorCmd, err)
		} else if p, err := exec.LookPath(args[0]); err != nil {
			add("Editor", "warn", "%q not found on PATH", args[0])
		} else {
			add("Editor", "ok", "%s (%s)", editorCmd, platform.ShortPath(p))
		}
	}
	return checks
}

func toolVersion(ctx context.Context, path string, args []string, re *regexp.Regexp) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	if m := re.FindStringSubmatch(out.String()); m != nil {
		return m[1]
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	return truncate(first, 60)
}
