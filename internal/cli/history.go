package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func newHistoryCmd(app *App) *cobra.Command {
	var (
		limit  int
		format formatFlag
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List recently generated projects",
		Long: `List projects generated with Sprout (newest first). Reuse the most recent
configuration with 'sprout new <name> --last', or save it as a preset with
'sprout presets save <name> --from-last'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hs, err := app.historyStore()
			if err != nil {
				return err
			}
			entries, err := hs.List()
			if err != nil {
				return err
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			if format.json() {
				if entries == nil {
					return writeJSON(app.Stdout, []struct{}{})
				}
				return writeJSON(app.Stdout, entries)
			}
			u := app.ui
			if len(entries) == 0 {
				u.println("No projects generated yet.")
				return nil
			}
			for _, e := range entries {
				o := e.Options
				facts := []string{o.Type, o.Language}
				if o.JavaVersion != "" {
					facts = append(facts, "Java "+o.JavaVersion)
				}
				if o.BootVersion != "" {
					facts = append(facts, "Boot "+o.BootVersion)
				}
				u.printf("%s  %s  %s\n", u.dim(e.Time.Local().Format("2006-01-02 15:04")), u.bold(o.ArtifactID), u.dim(e.Dir))
				line := "    " + strings.Join(facts, " · ")
				if len(o.Dependencies) > 0 {
					line += "  [" + strings.Join(o.Dependencies, ", ") + "]"
				}
				if e.Preset != "" {
					line += u.dim("  preset " + e.Preset)
				}
				u.println(line)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "maximum entries to show (0 for all)")
	format.register(cmd)

	clear := &cobra.Command{
		Use:   "clear",
		Short: "Forget all generated projects (project files are not touched)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hs, err := app.historyStore()
			if err != nil {
				return err
			}
			if err := hs.Clear(); err != nil {
				return err
			}
			app.ui.printf("%s History cleared\n", app.ui.ok("✓"))
			return nil
		},
	}
	cmd.AddCommand(clear)
	return cmd
}
