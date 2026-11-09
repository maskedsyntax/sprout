package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/maskedsyntax/sprout/internal/generator"
	"github.com/maskedsyntax/sprout/internal/history"
	"github.com/maskedsyntax/sprout/internal/javagen"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/project"
	"github.com/maskedsyntax/sprout/internal/wizard"
)

type newFlags struct {
	options       optionFlags
	output        string
	interactive   bool
	noInteractive bool
	force         bool
	git           bool
	noGit         bool
	open          bool
	editor        string
	dryRun        bool
	preset        string
	last          bool
	format        formatFlag
}

func (f *newFlags) register(cmd *cobra.Command, app *App, withPreset bool) {
	f.options.register(cmd, app)
	fs := cmd.Flags()
	fs.StringVarP(&f.output, "output", "o", "", "project directory to create (default: <output-dir>/<artifact>)")
	fs.BoolVarP(&f.interactive, "interactive", "i", false, "always run the interactive wizard")
	fs.BoolVar(&f.noInteractive, "no-interactive", false, "never prompt; fail if required input is missing")
	fs.BoolVar(&f.force, "force", false, "merge into a non-empty directory; replaced files are moved to .sprout-backup-<time>/")
	fs.BoolVar(&f.git, "git", false, "initialize a Git repository with an initial commit")
	fs.BoolVar(&f.noGit, "no-git", false, "do not initialize Git even if enabled in config")
	fs.BoolVar(&f.open, "open", false, "open the project in your editor afterwards")
	fs.StringVar(&f.editor, "editor", "", "editor command to open the project with (implies --open)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show the resolved configuration without downloading or writing anything")
	fs.BoolVar(&f.last, "last", false, "start from the last successfully generated configuration")
	if withPreset {
		fs.StringVarP(&f.preset, "preset", "p", "", "start from a saved preset (see 'sprout presets list')")
		_ = cmd.RegisterFlagCompletionFunc("preset", app.completePresetFlag)
	}
	f.format.register(cmd)
	cmd.MarkFlagsMutuallyExclusive("interactive", "no-interactive")
	cmd.MarkFlagsMutuallyExclusive("git", "no-git")
}

func newNewCmd(app *App) *cobra.Command {
	var f newFlags
	cmd := &cobra.Command{
		Use:     "new [name]",
		Aliases: []string{"init", "create"},
		Short:   "Generate a new Spring Boot project, or a Java type in the current one",
		Long: `Generate a new Spring Boot project, or create a Java class, interface,
enum, record or package in the current project (see 'sprout new class --help').

Inside an existing project, 'sprout new' without arguments asks what to
create: a Java type, a package, or a new Spring Boot project.

Without configuration flags, an interactive wizard starts when the terminal
supports it. With flags (or --no-interactive), the project is generated
directly. Values are resolved in this order, later winning:

  server defaults < config defaults < --last < --preset < flags

Dependencies from all layers are combined.

The project is downloaded and extracted into a temporary directory first and
moved into place only when complete. A non-empty destination is never
overwritten: --force merges into it, moving any file that would be replaced
into a .sprout-backup-<timestamp>/ folder inside the destination.`,
		Example: `  sprout new
  sprout new my-api
  sprout new my-api --deps web,data-jpa,postgresql
  sprout new my-api --language kotlin --build gradle-kotlin
  sprout new my-api --group com.example --artifact my-api
  sprout new my-api --java 21 --boot 4.1.x
  sprout new my-api --preset rest-api
  sprout new my-api --git --open
  sprout new my-api --deps web --dry-run --json

  # Inside a project:
  sprout new class User --in model
  sprout new record dto.UserResponse
  sprout new package controller`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageErrorf("unknown kind %q: use class, interface, enum, record or package (or one project name)", args[0])
			}
			return nil
		},
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else if app.interactive() && !anyLocalFlag(cmd) {
				// Inside a project, offer Java types as well as a new project.
				if proj, err := javagen.Find("."); err == nil {
					choice, err := wizard.ChooseKind(cmd.Context(), app.promptIO(), true)
					if err != nil {
						return err
					}
					if choice != wizard.NewProjectChoice {
						return runNewType(cmd.Context(), app, &typeFlags{dir: "."}, javagen.Kind(choice), "", proj, true)
					}
				}
			}
			return runNew(cmd.Context(), app, cmd, &f, name)
		},
	}
	f.register(cmd, app, true)
	for _, c := range newTypeCmds(app) {
		cmd.AddCommand(c)
	}
	return cmd
}

// anyLocalFlag reports whether any of the command's own flags was given.
func anyLocalFlag(cmd *cobra.Command) bool {
	used := false
	cmd.LocalFlags().VisitAll(func(fl *pflag.Flag) { used = used || fl.Changed })
	return used
}

// configFlagsUsed reports whether any project configuration was supplied
// via flags, which disables the automatic wizard.
func configFlagsUsed(cmd *cobra.Command, f *newFlags) bool {
	if f.preset != "" || f.last {
		return true
	}
	for _, n := range []string{"build", "type", "language", "boot", "java", "group", "artifact", "name", "description",
		"package", "project-version", "packaging", "config-format", "deps", "dependencies", "set", "preset", "last", "output"} {
		if fl := cmd.Flags().Lookup(n); fl != nil && fl.Changed {
			return true
		}
	}
	return false
}

func runNew(ctx context.Context, app *App, cmd *cobra.Command, f *newFlags, name string) error {
	cfg, err := app.config()
	if err != nil {
		return err
	}
	if app.offline && !f.dryRun {
		return errors.New("project generation requires a connection to the Spring Initializr server; remove --offline (or use --dry-run)")
	}
	flagOpts, err := f.options.options()
	if err != nil {
		return err
	}
	if name != "" {
		if strings.ContainsAny(name, `/\`) {
			return usageErrorf("project name %q must not contain path separators; use --output to choose a location", name)
		}
		if flagOpts.ArtifactID == "" {
			flagOpts.ArtifactID = wizard.Slug(name)
			if flagOpts.ArtifactID == "" {
				return usageErrorf("project name %q must contain letters or digits", name)
			}
		}
		if flagOpts.Name == "" {
			flagOpts.Name = name
		}
	}

	layers := []project.Layer{{Source: "config", Options: cfg.Defaults}}
	if f.last {
		hs, err := app.historyStore()
		if err != nil {
			return err
		}
		last, err := hs.Last()
		if err != nil {
			return err
		}
		lo := last.Options
		if flagOpts.ArtifactID != "" {
			lo.ArtifactID, lo.Name, lo.PackageName = "", "", ""
		}
		layers = append(layers, project.Layer{Source: "last project", Options: lo})
	}
	if f.preset != "" {
		store, err := app.presetStore()
		if err != nil {
			return err
		}
		p, err := store.Get(f.preset)
		if err != nil {
			return err
		}
		layers = append(layers, project.Layer{Source: fmt.Sprintf("preset %q", p.Name), Options: p.Options})
	}
	layers = append(layers, project.Layer{Source: "flags", Options: flagOpts})
	opts, sources := project.Merge(layers...)

	interactive := app.interactive()
	useWizard := f.interactive || (!f.noInteractive && interactive && !configFlagsUsed(cmd, f) && !f.format.json())
	if f.interactive && !interactive {
		return usageErrorf("--interactive requires a terminal (stdin and stdout must be a TTY)")
	}
	if !useWizard && opts.ArtifactID == "" {
		return usageErrorf("a project name is required in non-interactive mode: sprout new <name> [flags]")
	}

	res, err := app.metadata(ctx, false)
	if err != nil {
		return err
	}
	meta := res.Metadata

	// Forgive typos in dependency names: unambiguous corrections are applied,
	// close matches are offered in a terminal. Anything left unknown is
	// reported by validation below, together with where it came from.
	if len(opts.Dependencies) > 0 {
		dr, err := app.resolveDependencyInputs(ctx, meta, opts.Dependencies, nil, interactive && !f.format.json())
		var ve *project.ValidationError
		if err != nil && !errors.As(err, &ve) {
			return err
		}
		opts.Dependencies = append(depIDs(dr.Deps), dr.Unknown...)
	}

	output := f.output
	if useWizard {
		wres, err := wizard.Run(ctx, wizard.Input{
			Meta: meta, Initial: opts, Sources: sources, Output: output,
			DefaultOutput: func(artifact string) string { return app.displayPath(app.defaultOutput(cfg.OutputDir, artifact)) },
			Describe: func(req *project.Request, out string) string {
				var b strings.Builder
				writeSummary(&b, newUI(&b, &b, true), req, out)
				return strings.TrimRight(b.String(), "\n")
			},
			In: app.Stdin, Out: app.Stdout, NoColor: app.noColor || os.Getenv("NO_COLOR") != "",
			Accessible: os.Getenv("ACCESSIBLE") != "",
		})
		if errors.Is(err, wizard.ErrDeclined) {
			app.ui.note("Nothing generated.")
			return nil
		}
		if err != nil {
			return err
		}
		opts, output = wres.Options, wres.Output
		sources = project.Sources{}
	}

	req, err := project.Resolve(meta, opts, sources)
	if err != nil {
		return err
	}
	if output == "" {
		output = app.defaultOutput(cfg.OutputDir, req.Get(metadata.FieldArtifactID))
	}
	output, err = platform.ExpandHome(output)
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(output)
	if err != nil {
		return err
	}

	client, err := app.client()
	if err != nil {
		return err
	}
	if f.dryRun {
		return printDryRun(app, f, meta, req, dest, client.BaseURL.String())
	}
	if _, err := generator.CheckDestination(dest, f.force); err != nil {
		return err
	}

	if !f.format.json() {
		app.ui.note("Generating %s from %s…", req.Get(metadata.FieldArtifactID), client.BaseURL.Host)
	}
	result, err := generator.Generate(ctx, client, meta, req, generator.Options{Dest: dest, Force: f.force, Now: app.Now})
	if err != nil {
		return err
	}

	report := newReport{Dir: result.Dir, Files: len(result.Files), Merged: result.Merged, BackupDir: result.BackupDir,
		BackedUp: result.BackedUp, Request: requestJSON(req)}

	if hs, err := app.historyStore(); err == nil {
		if err := hs.Add(history.Entry{Time: app.now(), Dir: result.Dir, Server: client.BaseURL.String(), Preset: f.preset, Options: req.Options()}); err != nil {
			app.ui.warn("could not record history: %v", err)
		}
	}

	wantGit := (f.git || cfg.Git) && !f.noGit
	if wantGit && req.IsArchive() {
		report.Git = gitInit(ctx, app, result.Dir)
	}

	editor := platform.ResolveEditor(f.editor, cfg.Editor)
	wantOpen := f.open || f.editor != ""
	if f.format.json() {
		if wantOpen {
			report.Editor = editor
		}
		if err := writeJSON(app.Stdout, report); err != nil {
			return err
		}
	} else {
		printCreated(app, req, result)
	}
	if wantOpen {
		if editor == "" {
			return errors.New("no editor configured: pass --editor, run 'sprout config set editor <cmd>', or set $EDITOR")
		}
		stdout := app.Stdout
		if f.format.json() {
			stdout = app.Stderr
		}
		if err := platform.RunEditor(ctx, editor, result.Dir, platform.Stdio{In: app.Stdin, Out: stdout, Err: app.Stderr}); err != nil {
			return err
		}
	}
	return nil
}

func gitInit(ctx context.Context, app *App, dir string) string {
	err := platform.GitInit(ctx, dir)
	var ce *platform.CommitError
	switch {
	case err == nil:
		return "initialized"
	case errors.Is(err, platform.ErrAlreadyRepository):
		app.ui.note("Skipped git init: %s is already a Git repository.", dir)
		return "skipped (existing repository)"
	case errors.As(err, &ce):
		app.ui.warn("initialized a Git repository but %v", err)
		return "initialized (no commit)"
	default:
		app.ui.warn("git init failed: %v", err)
		return "failed"
	}
}

func (a *App) defaultOutput(outputDir, artifact string) string {
	base := outputDir
	if base == "" {
		base = "."
	}
	if p, err := platform.ExpandHome(base); err == nil {
		base = p
	}
	return filepath.Join(base, artifact)
}

// displayPath shows paths relative to the working directory when shorter.
func (a *App) displayPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	wd, err := os.Getwd()
	if err != nil {
		return abs
	}
	if rel, err := filepath.Rel(wd, abs); err == nil && filepath.IsLocal(rel) {
		return "." + string(filepath.Separator) + rel
	}
	return abs
}

type newReport struct {
	Dir       string      `json:"dir"`
	Files     int         `json:"files"`
	Merged    bool        `json:"merged"`
	BackupDir string      `json:"backupDir,omitempty"`
	BackedUp  []string    `json:"backedUp,omitempty"`
	Git       string      `json:"git,omitempty"`
	Editor    string      `json:"editor,omitempty"`
	Request   requestView `json:"request"`
}

type requestView struct {
	Type         string            `json:"type"`
	Values       map[string]string `json:"values"`
	Dependencies []string          `json:"dependencies"`
	URL          string            `json:"url,omitempty"`
	Output       string            `json:"output,omitempty"`
	Destination  string            `json:"destination,omitempty"`
}

func requestJSON(req *project.Request) requestView {
	v := requestView{Type: req.Type.ID, Values: map[string]string{}, Dependencies: req.DependencyIDs()}
	for _, val := range req.Values {
		v.Values[val.Field] = val.Value
	}
	return v
}

// writeSummary renders the resolved configuration.
func writeSummary(w interface{ Write([]byte) (int, error) }, u *ui, req *project.Request, output string) {
	const width = 14
	line := func(k, v string) { fmt.Fprintf(w, "  %s %s\n", u.dim(pad(k, width)), v) }
	line(fieldLabel(metadata.FieldType), describeOption(req.Type.ID, req.Type.Name))
	for _, v := range req.Values {
		val := v.Value
		if v.Label != "" && v.Label != v.Value {
			val = describeOption(v.Value, v.Label)
		}
		line(fieldLabel(v.Field), val)
	}
	if len(req.Dependencies) == 0 {
		line("Dependencies", u.dim("none"))
	} else {
		for i, d := range req.Dependencies {
			k := ""
			if i == 0 {
				k = "Dependencies"
			}
			line(k, fmt.Sprintf("%s %s", d.ID, u.dim("("+d.Name+")")))
		}
	}
	if output != "" {
		line("Output", output)
	}
}

func printDryRun(app *App, f *newFlags, meta *metadata.Metadata, req *project.Request, dest, server string) error {
	client, err := app.client()
	if err != nil {
		return err
	}
	target, err := client.ActionURL(generator.Endpoint(meta, req.Type), req.Query())
	if err != nil {
		return err
	}
	state := "will be created"
	switch st, err := generator.CheckDestination(dest, f.force); {
	case err != nil:
		state = "blocked: " + err.Error()
	case st == generator.DestEmpty:
		state = "exists (empty); files will be added"
	case st == generator.DestNonEmpty:
		state = "exists; will merge with backups (--force)"
	}
	if f.format.json() {
		v := requestJSON(req)
		v.URL, v.Destination, v.Output = target, dest, state
		return writeJSON(app.Stdout, struct {
			DryRun  bool        `json:"dryRun"`
			Server  string      `json:"server"`
			Request requestView `json:"request"`
		}{true, server, v})
	}
	u := app.ui
	u.println(u.bold("Dry run") + u.dim(" — nothing will be downloaded or written."))
	u.println()
	writeSummary(app.Stdout, u, req, dest)
	u.kv("Destination", 14, state)
	u.kv("Request", 14, "GET "+target)
	return nil
}

func printCreated(app *App, req *project.Request, r *generator.Result) {
	u := app.ui
	shown := app.displayPath(r.Dir)
	u.printf("%s Created %s in %s %s\n", u.ok("✓"), u.bold(req.Get(metadata.FieldArtifactID)), shown, u.dim(plural(len(r.Files), "file")))
	facts := []string{req.Type.Label()}
	lang, java := req.Label(metadata.FieldLanguage), req.Get(metadata.FieldJavaVersion)
	switch {
	case java != "" && strings.EqualFold(lang, "java"):
		facts = append(facts, "Java "+java)
	case java != "":
		facts = append(facts, lang, "Java "+java)
	case lang != "":
		facts = append(facts, lang)
	}
	if b := req.Get(metadata.FieldBootVersion); b != "" {
		facts = append(facts, "Spring Boot "+b)
	}
	if p := req.Label(metadata.FieldPackaging); p != "" {
		facts = append(facts, p)
	}
	u.printf("  %s\n", strings.Join(facts, " · "))
	if len(req.Dependencies) > 0 {
		u.printf("  %s %s\n", u.dim("Dependencies:"), strings.Join(req.DependencyIDs(), ", "))
	}
	if r.Merged && len(r.BackedUp) > 0 {
		u.printf("  %s %d existing file(s) replaced; originals saved in %s\n", u.warnc("!"), len(r.BackedUp), app.displayPath(r.BackupDir))
	}
	if !req.IsArchive() {
		return
	}
	u.println()
	u.println(u.dim("Next steps:"))
	u.printf("  cd %s\n", shown)
	win := runtime.GOOS == "windows"
	switch req.Type.Tags["build"] {
	case "maven":
		u.println(map[bool]string{false: "  ./mvnw spring-boot:run", true: "  mvnw.cmd spring-boot:run"}[win])
	case "gradle":
		u.println(map[bool]string{false: "  ./gradlew bootRun", true: "  gradlew.bat bootRun"}[win])
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("(1 %s)", word)
	}
	return fmt.Sprintf("(%d %ss)", n, word)
}
