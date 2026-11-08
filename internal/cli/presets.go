package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/presets"
	"github.com/maskedsyntax/sprout/internal/project"
)

func newPresetsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "presets",
		Aliases: []string{"preset"},
		Short:   "Save and reuse project configurations",
		Long: `Presets are named project configurations stored in presets.toml in the
config directory. Built-in examples (minimal, rest-api, web-jpa, postgres,
secure-api) can be customized by saving or editing a preset with the same
name. Presets are validated against current server metadata before use.`,
	}
	cmd.AddCommand(newPresetsListCmd(app), newPresetsShowCmd(app), newPresetsSaveCmd(app),
		newPresetsUseCmd(app), newPresetsEditCmd(app), newPresetsDeleteCmd(app))
	return cmd
}

func newPresetsListCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List presets",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := app.presetStore()
			if err != nil {
				return err
			}
			list, err := store.List()
			if err != nil {
				return err
			}
			if format.json() {
				return writeJSON(app.Stdout, list)
			}
			u := app.ui
			width := 0
			for _, p := range list {
				width = max(width, len(p.Name))
			}
			for _, p := range list {
				tag := ""
				if p.Builtin {
					tag = u.dim(" (example)")
				}
				u.printf("  %s %s%s\n", u.accent(pad(p.Name, width)), summarizeOptions(p.Options), tag)
			}
			u.println()
			u.println(u.dim("Use: sprout new <name> --preset <preset>   Stored in " + store.Path))
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

func summarizeOptions(o project.Options) string {
	var parts []string
	for _, id := range o.SetIDs() {
		parts = append(parts, project.KeyForField(id)+"="+o.Get(id))
	}
	if len(o.Dependencies) > 0 {
		parts = append(parts, "deps="+strings.Join(o.Dependencies, ","))
	}
	if len(parts) == 0 {
		return "(server defaults)"
	}
	return strings.Join(parts, " ")
}

func newPresetsShowCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:               "show <name>",
		Short:             "Show a preset and check it against current metadata",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: app.completePresets,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := app.presetStore()
			if err != nil {
				return err
			}
			p, err := store.Get(args[0])
			if err != nil {
				return err
			}
			problems, checkErr := app.checkPreset(cmd, p)
			if format.json() {
				out := struct {
					presets.Preset
					Valid    *bool             `json:"valid,omitempty"`
					Problems []project.Problem `json:"problems,omitempty"`
				}{Preset: p, Problems: problems}
				if checkErr == nil {
					v := len(problems) == 0
					out.Valid = &v
				}
				return writeJSON(app.Stdout, out)
			}
			u := app.ui
			title := u.bold(p.Name)
			if p.Builtin {
				title += u.dim(" (built-in example)")
			}
			u.println(title)
			var b strings.Builder
			if err := toml.NewEncoder(&b).Encode(p.Options); err != nil {
				return err
			}
			body := strings.TrimSpace(b.String())
			if body == "" {
				body = "# uses server and config defaults"
			}
			for line := range strings.SplitSeq(body, "\n") {
				u.println("  " + line)
			}
			u.println()
			switch {
			case checkErr != nil:
				u.warn("could not validate preset: %v", checkErr)
			case len(problems) == 0:
				u.println(u.ok("✓") + " valid for the current server metadata")
			default:
				u.println(u.warnc("✗") + " outdated or invalid for the current server metadata:")
				for _, pr := range problems {
					u.println("  - " + pr.Message)
				}
			}
			return nil
		},
	}
	format.register(cmd)
	return cmd
}

// checkPreset validates a preset in isolation (server defaults + preset).
func (a *App) checkPreset(cmd *cobra.Command, p presets.Preset) ([]project.Problem, error) {
	res, err := a.metadata(cmd.Context(), false)
	if err != nil {
		return nil, err
	}
	opts, src := project.Merge(project.Layer{Source: fmt.Sprintf("preset %q", p.Name), Options: p.Options})
	if opts.ArtifactID == "" {
		opts.ArtifactID = "demo"
	}
	_, err = project.Resolve(res.Metadata, opts, src)
	if ve, ok := errors.AsType[*project.ValidationError](err); ok {
		return ve.Problems, nil
	}
	return nil, err
}

func newPresetsSaveCmd(app *App) *cobra.Command {
	var (
		flags    optionFlags
		fromLast bool
		force    bool
	)
	cmd := &cobra.Command{
		Use:   "save <name> [flags]",
		Short: "Save a preset from flags or from the last generated project",
		Example: `  sprout presets save rest-api --java 21 --deps web,validation,actuator
  sprout presets save kotlin-api --language kotlin --build gradle-kotlin --deps web
  sprout presets save my-usual --from-last`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := presets.ValidateName(name); err != nil {
				return usageError{err}
			}
			store, err := app.presetStore()
			if err != nil {
				return err
			}
			opts, err := flags.options()
			if err != nil {
				return err
			}
			if fromLast {
				hs, err := app.historyStore()
				if err != nil {
					return err
				}
				last, err := hs.Last()
				if err != nil {
					return err
				}
				lo := last.Options
				// Project identity is not part of a reusable preset.
				lo.ArtifactID, lo.Name, lo.PackageName, lo.Description = "", "", "", ""
				opts, _ = project.Merge(project.Layer{Options: lo}, project.Layer{Options: opts})
			}
			if opts.IsZero() && !fromLast {
				return usageErrorf("nothing to save: pass project flags (e.g. --deps web --java 21) or --from-last")
			}
			if existing, err := store.Get(name); err == nil && !existing.Builtin && !force {
				return fmt.Errorf("preset %q already exists; use --force to replace it or 'sprout presets edit %s'", name, name)
			}
			p := presets.Preset{Name: name, Options: opts}
			problems, checkErr := app.checkPreset(cmd, p)
			if err := store.Save(name, opts); err != nil {
				return err
			}
			app.ui.printf("%s Saved preset %s: %s\n", app.ui.ok("✓"), app.ui.bold(name), summarizeOptions(opts))
			reportProblems(app, checkErr, problems)
			return nil
		},
	}
	flags.register(cmd, app)
	cmd.Flags().BoolVar(&fromLast, "from-last", false, "start from the last generated project's configuration")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing preset")
	return cmd
}

func reportProblems(app *App, checkErr error, problems []project.Problem) {
	if checkErr != nil {
		app.ui.warn("could not validate preset against server metadata: %v", checkErr)
		return
	}
	for _, p := range problems {
		app.ui.warn("%s", p.Message)
	}
}

func newPresetsUseCmd(app *App) *cobra.Command {
	var f newFlags
	cmd := &cobra.Command{
		Use:   "use <preset> [project-name] [flags]",
		Short: "Generate a project from a preset (same as 'new --preset')",
		Example: `  sprout presets use rest-api my-api
  sprout presets use postgres orders-service --group com.acme --git`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: app.completePresets,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.preset = args[0]
			name := ""
			if len(args) == 2 {
				name = args[1]
			}
			return runNew(cmd.Context(), app, cmd, &f, name)
		},
	}
	f.register(cmd, app, false)
	return cmd
}

func newPresetsEditCmd(app *App) *cobra.Command {
	var (
		flags  optionFlags
		editor string
	)
	cmd := &cobra.Command{
		Use:   "edit <name> [flags]",
		Short: "Edit a preset with flags, or in your editor",
		Long: `Edit a preset. With project flags, the given fields are updated in place
(--deps replaces the dependency list). Without flags, presets.toml is opened
in your editor (built-in examples are copied there first) and the result is
validated afterwards.`,
		Example: `  sprout presets edit rest-api --java 25
  sprout presets edit rest-api --deps web,actuator,security
  sprout presets edit rest-api`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: app.completePresets,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			store, err := app.presetStore()
			if err != nil {
				return err
			}
			p, err := store.Get(name)
			if err != nil {
				return err
			}
			changes, err := flags.options()
			if err != nil {
				return err
			}
			if !changes.IsZero() {
				for _, id := range changes.SetIDs() {
					p.Options.Set(id, changes.Get(id))
				}
				if changes.Dependencies != nil {
					p.Options.Dependencies = changes.Dependencies
				}
				if err := store.Save(name, p.Options); err != nil {
					return err
				}
				app.ui.printf("%s Updated preset %s: %s\n", app.ui.ok("✓"), app.ui.bold(name), summarizeOptions(p.Options))
			} else {
				cfg, err := app.config()
				if err != nil {
					return err
				}
				ed := platform.ResolveEditor(editor, cfg.Editor)
				if ed == "" {
					return errors.New("no editor configured: pass --editor, run 'sprout config set editor <cmd>', or set $EDITOR")
				}
				if err := store.Materialize(name); err != nil {
					return err
				}
				if err := platform.RunEditor(cmd.Context(), ed, store.Path, platform.Stdio{In: app.Stdin, Out: app.Stdout, Err: app.Stderr}); err != nil {
					return err
				}
				if err := store.Check(); err != nil {
					return fmt.Errorf("presets file is invalid after editing: %w", err)
				}
				if p, err = store.Get(name); err != nil {
					app.ui.note("Preset %q no longer exists.", name)
					return nil
				}
			}
			problems, checkErr := app.checkPreset(cmd, p)
			reportProblems(app, checkErr, problems)
			return nil
		},
	}
	flags.register(cmd, app)
	cmd.Flags().StringVar(&editor, "editor", "", "editor command to use")
	return cmd
}

func newPresetsDeleteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:               "delete <name>",
		Aliases:           []string{"rm", "remove"},
		Short:             "Delete a preset",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: app.completePresets,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := app.presetStore()
			if err != nil {
				return err
			}
			reverted, err := store.Delete(args[0])
			if err != nil {
				return err
			}
			if reverted {
				app.ui.printf("%s Removed your customization of %s; the built-in example is used again.\n", app.ui.ok("✓"), args[0])
			} else {
				app.ui.printf("%s Deleted preset %s\n", app.ui.ok("✓"), args[0])
			}
			return nil
		},
	}
}
