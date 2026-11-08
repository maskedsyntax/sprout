package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/metadata"
)

type depsFlags struct {
	boot     string
	category string
	all      bool
	format   formatFlag
}

func (f *depsFlags) register(cmd *cobra.Command, app *App, persistent bool) {
	fs := cmd.Flags()
	if persistent {
		fs = cmd.PersistentFlags()
	}
	fs.StringVar(&f.boot, "boot", "", "only show dependencies compatible with this Spring Boot version (e.g. 4.1.x)")
	fs.StringVarP(&f.category, "category", "c", "", "only show one category (e.g. web, \"SQL\")")
	fs.BoolVar(&f.all, "all", false, "with --boot, also list incompatible dependencies (marked)")
	_ = cmd.RegisterFlagCompletionFunc("boot", app.completeField(metadata.FieldBootVersion))
	_ = cmd.RegisterFlagCompletionFunc("category", app.completeCategories)
}

func newDepsCmd(app *App) *cobra.Command {
	var f depsFlags
	cmd := &cobra.Command{
		Use:     "deps",
		Aliases: []string{"dependencies", "dep"},
		Short:   "Browse and search available dependencies",
		Example: `  sprout deps
  sprout deps search security
  sprout deps list --category web
  sprout deps info data-jpa
  sprout deps --boot 4.0.x
  sprout deps --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runDepsList(cmd, app, &f, "") },
	}
	f.register(cmd, app, true)
	f.format.register(cmd)

	var lf formatFlag
	list := &cobra.Command{
		Use:   "list",
		Short: "List dependencies grouped by category",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.format = lf
			return runDepsList(cmd, app, &f, "")
		},
	}
	lf.register(list)

	var sf formatFlag
	search := &cobra.Command{
		Use:   "search <query>...",
		Short: "Search dependencies by ID, name, description or category",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.format = sf
			return runDepsList(cmd, app, &f, strings.Join(args, " "))
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	sf.register(search)

	var inf formatFlag
	info := &cobra.Command{
		Use:               "info <id>...",
		Short:             "Show details about dependencies",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: app.completeDependencyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.format = inf
			return runDepsInfo(cmd, app, &f, args)
		},
	}
	inf.register(info)

	cmd.AddCommand(list, search, info)
	return cmd
}

type depView struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Category     string            `json:"category"`
	VersionRange string            `json:"versionRange,omitempty"`
	Compatible   *bool             `json:"compatible,omitempty"`
	Links        map[string]string `json:"links,omitempty"`
}

func toView(d *metadata.Dependency, boot *metadata.Version, bootID string) depView {
	v := depView{ID: d.ID, Name: d.Name, Description: d.Description, Category: d.Group, VersionRange: d.VersionRange}
	if boot != nil {
		c := d.CompatibleWith(*boot)
		v.Compatible = &c
	}
	if len(d.Links) > 0 {
		v.Links = map[string]string{}
		for rel, l := range d.Links {
			href := l.Href
			if l.Templated && bootID != "" {
				href = strings.ReplaceAll(href, "{bootVersion}", bootID)
			}
			v.Links[rel] = href
		}
	}
	return v
}

// bootFilter resolves --boot to a concrete version.
func bootFilter(meta *metadata.Metadata, in string) (*metadata.Version, string, error) {
	if in == "" {
		return nil, "", nil
	}
	opt, err := meta.ResolveBootVersion(in)
	if err != nil {
		return nil, "", fmt.Errorf("%w; run 'sprout versions'", err)
	}
	v, err := metadata.ParseVersion(opt.ID)
	if err != nil {
		return nil, "", err
	}
	return &v, opt.ID, nil
}

func runDepsList(cmd *cobra.Command, app *App, f *depsFlags, query string) error {
	res, err := app.metadata(cmd.Context(), false)
	if err != nil {
		return err
	}
	meta := res.Metadata
	boot, bootID, err := bootFilter(meta, f.boot)
	if err != nil {
		return err
	}
	var deps []*metadata.Dependency
	if query != "" {
		deps = meta.Search(query)
	} else {
		deps = meta.Dependencies()
	}
	if f.category != "" {
		g, ok := findCategory(meta, f.category)
		if !ok {
			names := make([]string, len(meta.Groups))
			for i, g := range meta.Groups {
				names[i] = g.Name
			}
			return fmt.Errorf("unknown category %q; available: %s", f.category, strings.Join(names, ", "))
		}
		var filtered []*metadata.Dependency
		for _, d := range deps {
			if d.Group == g.Name {
				filtered = append(filtered, d)
			}
		}
		deps = filtered
	}
	hidden := 0
	if boot != nil && !f.all {
		var filtered []*metadata.Dependency
		for _, d := range deps {
			if d.CompatibleWith(*boot) {
				filtered = append(filtered, d)
			} else {
				hidden++
			}
		}
		deps = filtered
	}

	if f.format.json() {
		out := make([]depView, len(deps))
		for i, d := range deps {
			out[i] = toView(d, boot, bootID)
		}
		return writeJSON(app.Stdout, out)
	}
	u := app.ui
	if len(deps) == 0 {
		if query != "" {
			u.printf("No dependencies match %q.\n", query)
		} else {
			u.println("No dependencies found.")
		}
		if hidden > 0 {
			u.note("%d incompatible with Spring Boot %s hidden; use --all to show them.", hidden, bootID)
		}
		return nil
	}
	idWidth := 0
	for _, d := range deps {
		idWidth = max(idWidth, len(d.ID))
	}
	idWidth = min(idWidth, 40)
	printDep := func(d *metadata.Dependency, showGroup bool) {
		marker := ""
		if boot != nil && !d.CompatibleWith(*boot) {
			marker = " " + u.warnc("[requires "+d.Range.String()+"]")
		}
		desc := d.Name
		if showGroup {
			desc += u.dim(" · " + d.Group)
		}
		room := u.width - idWidth - len(d.Name) - 8
		if d.Description != "" && room > 20 && !showGroup {
			desc += u.dim(" — " + truncate(d.Description, room))
		}
		u.printf("  %s %s%s\n", u.accent(pad(d.ID, idWidth)), desc, marker)
	}
	if query != "" {
		for _, d := range deps {
			printDep(d, true)
		}
	} else {
		group := ""
		for _, d := range deps {
			if d.Group != group {
				if group != "" {
					u.println()
				}
				group = d.Group
				u.println(u.heading(group))
			}
			printDep(d, false)
		}
	}
	summary := fmt.Sprintf("%d dependencies", len(deps))
	if bootID != "" {
		summary += " compatible with Spring Boot " + bootID
	}
	if hidden > 0 {
		summary += fmt.Sprintf(" (%d incompatible hidden; --all to show)", hidden)
	}
	u.println()
	u.println(u.dim(summary + ". Details: sprout deps info <id>"))
	return nil
}

func findCategory(meta *metadata.Metadata, name string) (*metadata.Group, bool) {
	if g, ok := meta.Group(name); ok {
		return g, true
	}
	var match *metadata.Group
	for _, g := range meta.Groups {
		if strings.Contains(strings.ToLower(g.Name), strings.ToLower(name)) {
			if match != nil {
				return nil, false // ambiguous
			}
			match = g
		}
	}
	return match, match != nil
}

func runDepsInfo(cmd *cobra.Command, app *App, f *depsFlags, ids []string) error {
	res, err := app.metadata(cmd.Context(), false)
	if err != nil {
		return err
	}
	meta := res.Metadata
	boot, bootID, err := bootFilter(meta, f.boot)
	if err != nil {
		return err
	}
	if boot == nil {
		if bf := meta.Field(metadata.FieldBootVersion); bf != nil {
			if v, err := metadata.ParseVersion(bf.Default); err == nil {
				boot, bootID = &v, bf.Default
			}
		}
	}
	var views []depView
	var missing []string
	for _, id := range ids {
		d, ok := meta.Dependency(id)
		if m := meta.Match(id, 3); !ok && m.Corrected != nil {
			app.ui.note("Using %s for %q.", m.Corrected.ID, id)
			d, ok = m.Corrected, true
		}
		if !ok {
			msg := fmt.Sprintf("unknown dependency %q", id)
			if s := meta.Suggest(id, 3); len(s) > 0 {
				msg += " (did you mean " + strings.Join(s, ", ") + "?)"
			}
			missing = append(missing, msg)
			continue
		}
		views = append(views, toView(d, boot, bootID))
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s; search with 'sprout deps search <term>'", strings.Join(missing, "; "))
	}
	if f.format.json() {
		if len(views) == 1 {
			return writeJSON(app.Stdout, views[0])
		}
		return writeJSON(app.Stdout, views)
	}
	u := app.ui
	for i, v := range views {
		if i > 0 {
			u.println()
		}
		u.printf("%s %s\n", u.bold(v.Name), u.dim("("+v.ID+")"))
		if v.Description != "" {
			u.printf("  %s\n", v.Description)
		}
		u.kv("Category", 12, v.Category)
		rng := "all versions"
		if v.VersionRange != "" {
			d, _ := meta.Dependency(v.ID)
			rng = d.Range.String()
		}
		compat := ""
		if v.Compatible != nil {
			if *v.Compatible {
				compat = u.ok(" ✓ compatible with " + boot.String())
			} else {
				compat = u.warnc(" ✗ not compatible with " + boot.String())
			}
		}
		u.kv("Spring Boot", 12, rng+compat)
		for _, rel := range []string{"reference", "guide", "home", "sample"} {
			if href, ok := v.Links[rel]; ok {
				u.kv(strings.ToUpper(rel[:1])+rel[1:], 12, href)
			}
		}
	}
	return nil
}
