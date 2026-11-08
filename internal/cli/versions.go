package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

func newVersionsCmd(app *App) *cobra.Command {
	var (
		stable bool
		format formatFlag
	)
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "List available Spring Boot versions",
		Long: `List the Spring Boot versions offered by the server.

Versions are classified by their qualifier, following Spring Initializr's
conventions: no qualifier (or .RELEASE) is a GA release, -M<n> a milestone,
-RC<n> a release candidate and -SNAPSHOT / .BUILD-SNAPSHOT a snapshot.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := app.metadata(cmd.Context(), false)
			if err != nil {
				return err
			}
			if res.Metadata.Field(metadata.FieldBootVersion) == nil {
				return fmt.Errorf("this server does not advertise Spring Boot versions")
			}
			type view struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				Stable  bool   `json:"stable"`
				Default bool   `json:"default"`
			}
			var out []view
			for _, bv := range res.Metadata.BootVersions() {
				if stable && !bv.Version.Stable() {
					continue
				}
				out = append(out, view{bv.Option.ID, bv.Option.Label(), kindName(bv.Version.Kind()), bv.Version.Stable(), bv.Default})
			}
			if format.json() {
				return writeJSON(app.Stdout, out)
			}
			u := app.ui
			for _, v := range out {
				kind := v.Kind
				if v.Stable {
					kind = u.ok(pad(kind, 17))
				} else {
					kind = u.warnc(pad(kind, 17))
				}
				def := ""
				if v.Default {
					def = u.dim(" (default)")
				}
				u.printf("  %s %s%s\n", u.bold(pad(v.ID, 18)), kind, def)
			}
			if len(out) == 0 {
				u.println("No versions found.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&stable, "stable", false, "only show GA releases")
	format.register(cmd)
	return cmd
}

func kindName(k metadata.VersionKind) string {
	switch k {
	case metadata.KindGA:
		return "stable"
	case metadata.KindRC:
		return "release candidate"
	case metadata.KindMilestone:
		return "milestone"
	case metadata.KindSnapshot:
		return "snapshot"
	}
	return "pre-release"
}

// newOptionsCmd lists every capability advertised by the server.
func newOptionsCmd(app *App) *cobra.Command {
	var format formatFlag
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Show every project option the server supports, with defaults",
		Long: `Show every capability advertised by the Spring Initializr server: project
types, languages, Java versions, packaging, configuration formats, text
fields and any server-specific options, with their defaults and the flag
used to set them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := app.metadata(cmd.Context(), false)
			if err != nil {
				return err
			}
			m := res.Metadata
			if format.json() {
				return writeJSON(app.Stdout, struct {
					Fields []*metadata.Field `json:"fields"`
				}{m.Fields})
			}
			u := app.ui
			for i, f := range m.Fields {
				if i > 0 {
					u.println()
				}
				flag := "--set " + f.ID + "=…"
				if project.IsKnownField(f.ID) {
					flag = "--" + flagForField(f.ID)
				}
				title := fieldLabel(f.ID)
				u.printf("%s %s\n", u.bold(title), u.dim(fmt.Sprintf("(%s, %s)", f.ID, flag)))
				if len(f.Options) == 0 {
					def := f.Default
					if def == "" {
						def = "(none)"
					}
					u.kv("default", 8, def)
					continue
				}
				for _, o := range f.Options {
					mark := "  "
					if o.ID == f.Default {
						mark = u.ok("* ")
					}
					label := ""
					if o.Name != "" && !strings.EqualFold(o.Name, o.ID) {
						label = u.dim(" " + o.Name)
					}
					u.printf("  %s%s%s\n", mark, o.ID, label)
				}
			}
			if m.HasDependencies {
				u.println()
				u.printf("%s %s\n", u.bold("Dependencies"), u.dim(fmt.Sprintf("(%d in %d categories, --deps; see 'sprout deps')", len(m.Dependencies()), len(m.Groups))))
			}
			u.println()
			u.println(u.dim("* = server default"))
			return nil
		},
	}
	format.register(cmd)
	return cmd
}
