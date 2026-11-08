package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/config"
	"github.com/maskedsyntax/sprout/internal/platform"
)

func newConfigCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage global preferences",
		Long: `Manage global preferences stored in config.toml.

The file lives in the OS configuration directory ($XDG_CONFIG_HOME/sprout or
~/.config/sprout on Linux, ~/Library/Application Support/sprout on macOS,
%AppData%\sprout on Windows) unless SPROUT_CONFIG_DIR is set.`,
	}
	cmd.AddCommand(newConfigShowCmd(app), newConfigGetCmd(app), newConfigSetCmd(app),
		newConfigUnsetCmd(app), newConfigResetCmd(app), newConfigPathCmd(app))
	return cmd
}

func keyHelp() string {
	var b strings.Builder
	b.WriteString("Keys:\n")
	for _, k := range config.Keys() {
		name := k.Name
		if len(k.Aliases) > 0 {
			name += " (" + strings.Join(k.Aliases, ", ") + ")"
		}
		fmt.Fprintf(&b, "  %-38s %s\n", name, k.Description)
	}
	fmt.Fprintf(&b, "  %-38s %s", "default-extra.<id>", "default for a server-specific option (see 'sprout options')")
	return b.String()
}

func newConfigShowCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := app.config()
			if err != nil {
				return err
			}
			path, err := app.configPath()
			if err != nil {
				return err
			}
			if format.json() {
				return writeJSON(app.Stdout, struct {
					Path   string         `json:"path"`
					Config *config.Config `json:"config"`
				}{path, cfg})
			}
			u := app.ui
			u.println(u.dim("# " + path))
			for _, k := range config.Keys() {
				v, _ := cfg.Get(k.Name)
				shown := v
				switch {
				case k.Name == "server" && v == "":
					shown = u.dim(cfg.ServerURL() + " (default)")
				case k.Name == "cache-ttl" && v == "":
					shown = u.dim("12h (default)")
				case k.Name == "editor" && v == "":
					if e := platform.ResolveEditor("", ""); e != "" {
						shown = u.dim(e + " (from $VISUAL/$EDITOR)")
					} else {
						shown = u.dim("(unset)")
					}
				case k.Name == "output-dir" && v == "":
					shown = u.dim(". (current directory)")
				case v == "":
					shown = u.dim("(unset)")
				}
				u.kv(k.Name, 24, shown)
			}
			for id, v := range cfg.Defaults.Extra {
				u.kv("default-extra."+id, 24, v)
			}
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

func newConfigGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "get <key>",
		Short:             "Print one configuration value",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.config()
			if err != nil {
				return err
			}
			v, err := cfg.Get(args[0])
			if err != nil {
				return usageError{err}
			}
			app.ui.println(v)
			return nil
		},
	}
}

func newConfigSetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long:  "Set a configuration value.\n\n" + keyHelp(),
		Example: `  sprout config set default-language java
  sprout config set default-build maven-project
  sprout config set default-java 21
  sprout config set default-boot 4.1.x
  sprout config set default-group com.example
  sprout config set default-editor hx
  sprout config set output-dir ~/code
  sprout config set server https://start.spring.io`,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.config()
			if err != nil {
				return err
			}
			value := strings.Join(args[1:], " ")
			if err := cfg.Set(args[0], value); err != nil {
				return usageError{err}
			}
			path, err := app.configPath()
			if err != nil {
				return err
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			app.ui.printf("%s %s = %s\n", app.ui.ok("✓"), args[0], value)
			return nil
		},
	}
}

func newConfigUnsetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "unset <key>",
		Short:             "Remove a configuration value",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfigKeys,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := app.config()
			if err != nil {
				return err
			}
			if err := cfg.Unset(args[0]); err != nil {
				return usageError{err}
			}
			path, err := app.configPath()
			if err != nil {
				return err
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			app.ui.printf("%s %s unset\n", app.ui.ok("✓"), args[0])
			return nil
		},
	}
}

func newConfigResetCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Reset configuration to defaults (presets are kept)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := app.configPath()
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
				app.ui.println("Configuration is already at defaults.")
				return nil
			}
			if !yes {
				if !app.interactive() {
					return usageErrorf("refusing to reset without confirmation; pass --yes")
				}
				fmt.Fprintf(app.Stdout, "Reset %s to defaults? [y/N] ", path)
				line, _ := bufio.NewReader(app.Stdin).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
					app.ui.println("Aborted.")
					return nil
				}
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("reset config: %w", err)
			}
			app.cfg = nil
			app.ui.printf("%s Configuration reset (presets in presets.toml are unchanged)\n", app.ui.ok("✓"))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func newConfigPathCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := app.configPath()
			if err != nil {
				return err
			}
			app.ui.println(p)
			return nil
		},
	}
}
