package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/buildfile"
	"github.com/maskedsyntax/sprout/internal/depedit"
	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/wizard"
)

type depEditFlags struct {
	dir    string
	boot   string
	dryRun bool
	yes    bool
	format formatFlag
}

func (f *depEditFlags) register(cmd *cobra.Command, app *App) {
	fs := cmd.Flags()
	fs.StringVarP(&f.dir, "dir", "C", ".", "project directory (searched upwards for pom.xml / build.gradle[.kts])")
	fs.StringVar(&f.boot, "boot", "", "Spring Boot version of the project, if it cannot be detected")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show the changes without writing them")
	fs.BoolVarP(&f.yes, "yes", "y", false, "apply without asking for confirmation")
	f.format.register(cmd)
	_ = cmd.RegisterFlagCompletionFunc("boot", app.completeField(metadata.FieldBootVersion))
	_ = cmd.MarkFlagDirname("dir")
}

const depEditLong = `Sprout asks Spring Initializr to generate your project's build file with
and without the change, and applies exactly that difference to your
pom.xml, build.gradle or build.gradle.kts. This brings in everything the
dependency needs (test starters, BOMs, version properties, plugins,
annotation processors) just as start.spring.io would, while keeping your
formatting, comments and other content.

Nothing is written until you have seen the diff and confirmed (use --yes in
scripts, --dry-run to only preview). Anything Sprout cannot change safely,
such as a plugin you have customized, is left alone and shown as a manual
step. Typos are forgiven: "datajpa" becomes data-jpa, and close matches are
offered for anything else.`

func newAddCmd(app *App) *cobra.Command {
	var f depEditFlags
	cmd := &cobra.Command{
		Use:   "add [dependency...]",
		Short: "Add dependencies to an existing project",
		Long:  "Add Spring Initializr dependencies to an existing project.\n\n" + depEditLong + "\n\nWithout arguments, a searchable list of dependencies opens.",
		Example: `  sprout add                      # search and pick interactively
  sprout add web
  sprout add data-jpa postgresql
  sprout add datajpa              # corrected to data-jpa
  sprout add lombok --dry-run
  sprout add security -C ../other-service --yes`,
		ValidArgsFunction: app.completeDependencyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDepEdit(cmd.Context(), app, &f, args, true)
		},
	}
	f.register(cmd, app)
	return cmd
}

func newRemoveCmd(app *App) *cobra.Command {
	var f depEditFlags
	cmd := &cobra.Command{
		Use:     "remove [dependency...]",
		Aliases: []string{"rm"},
		Short:   "Remove dependencies from an existing project",
		Long:    "Remove Spring Initializr dependencies from an existing project.\n\n" + depEditLong + "\n\nWithout arguments, a list of the project's dependencies opens.",
		Example: `  sprout remove                   # pick from the project's dependencies
  sprout remove web
  sprout remove lombok devtools --dry-run`,
		ValidArgsFunction: app.completeDependencyArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDepEdit(cmd.Context(), app, &f, args, false)
		},
	}
	f.register(cmd, app)
	return cmd
}

type depEditReport struct {
	File    string             `json:"file"`
	Kind    string             `json:"kind"`
	Boot    string             `json:"bootVersion"`
	Present []string           `json:"present"`
	Added   []string           `json:"added,omitempty"`
	Removed []string           `json:"removed,omitempty"`
	Skipped []string           `json:"skipped,omitempty"`
	Changes []buildfile.Change `json:"changes"`
	Notes   []buildfile.Note   `json:"notes,omitempty"`
	Diff    string             `json:"diff,omitempty"`
	Applied bool               `json:"applied"`
}

func runDepEdit(ctx context.Context, app *App, f *depEditFlags, args []string, adding bool) error {
	if app.offline {
		return errors.New("changing dependencies requires the Spring Initializr server; remove --offline")
	}
	proj, err := buildfile.Find(f.dir)
	if err != nil {
		return err
	}
	jsonOut := f.format.json()
	u := app.ui
	if !jsonOut {
		facts := []string{proj.Kind.String()}
		if proj.BootVersion != "" {
			facts = append(facts, "Spring Boot "+proj.BootVersion)
		}
		if proj.JavaVersion != "" {
			facts = append(facts, "Java "+proj.JavaVersion)
		}
		if proj.Language != "java" {
			facts = append(facts, proj.Language)
		}
		u.printf("%s %s\n", u.bold(app.displayPath(proj.File)), u.dim("("+strings.Join(facts, " · ")+")"))
	}
	for _, w := range proj.Warnings {
		u.warn("%s", w)
	}

	res, err := app.metadata(ctx, false)
	if err != nil {
		return err
	}
	meta := res.Metadata
	client, err := app.client()
	if err != nil {
		return err
	}
	ed := &depedit.Editor{Client: client, Meta: meta}
	target, err := ed.Target(proj, f.boot)
	if err != nil {
		return err
	}
	det, err := ed.Detect(ctx, target)
	if err != nil {
		return err
	}
	present := det.Present
	interactive := app.interactive() && !jsonOut

	ids, skipped, err := app.chooseDepEdit(ctx, meta, target, det, args, adding, interactive)
	if err != nil {
		return err
	}
	report := depEditReport{File: proj.File, Kind: string(proj.Kind), Boot: target.Boot.String(), Present: present, Skipped: skipped}
	if len(ids) == 0 {
		if jsonOut {
			return writeJSON(app.Stdout, report)
		}
		u.println("Nothing to change.")
		return nil
	}

	var plan *buildfile.Plan
	if adding {
		plan, err = ed.Plan(ctx, target, present, ids, nil)
		report.Added = ids
	} else {
		plan, err = ed.Plan(ctx, target, present, nil, ids)
		report.Removed = ids
	}
	if err != nil {
		return err
	}
	report.Changes, report.Notes = plan.Changes, plan.Notes

	diff := buildfile.Diff(proj.Doc.Src, plan.Result, 3)
	var diffText strings.Builder
	for _, l := range diff {
		if l.Op != '@' {
			diffText.WriteByte(l.Op)
		}
		diffText.WriteString(l.Text + "\n")
	}
	report.Diff = diffText.String()

	if !jsonOut {
		printDepDiff(app, proj, diff)
		printNotes(app, plan.Notes)
	}
	if !plan.Changed() {
		if !adding && len(plan.Notes) == 0 {
			return fmt.Errorf("%s not found in %s", strings.Join(ids, ", "), filepath.Base(proj.File))
		}
		if jsonOut {
			return writeJSON(app.Stdout, report)
		}
		u.println("Nothing to change.")
		return nil
	}
	if f.dryRun {
		if jsonOut {
			return writeJSON(app.Stdout, report)
		}
		u.note("Dry run: %s was not modified.", filepath.Base(proj.File))
		return nil
	}
	if interactive && !f.yes {
		ok, err := confirm(app, fmt.Sprintf("Apply these changes to %s?", filepath.Base(proj.File)))
		if err != nil {
			return err
		}
		if !ok {
			u.println("Aborted; nothing was changed.")
			return nil
		}
	}
	if err := writeBuildFile(proj, plan.Result); err != nil {
		return err
	}
	report.Applied = true
	if jsonOut {
		return writeJSON(app.Stdout, report)
	}
	verb := "Added"
	if !adding {
		verb = "Removed"
	}
	u.printf("%s %s %s %s %s\n", u.ok("✓"), verb, strings.Join(ids, ", "), map[bool]string{true: "to", false: "from"}[adding], filepath.Base(proj.File))
	u.println(u.dim(refreshHint(proj.Kind)))
	return nil
}

// chooseDepEdit turns arguments (or an interactive pick) into dependency IDs
// to add or remove, reporting IDs skipped because nothing would change.
func (a *App) chooseDepEdit(ctx context.Context, meta *metadata.Metadata, t *depedit.Target, det *depedit.Detection, args []string, adding, interactive bool) ([]string, []string, error) {
	present := det.Present
	isPresent := map[string]bool{}
	for _, id := range present {
		isPresent[id] = true
	}
	if len(args) == 0 {
		if !interactive {
			return nil, nil, usageErrorf("name the dependencies to %s, e.g. 'sprout %s web' (run in a terminal to pick from a list)",
				map[bool]string{true: "add", false: "remove"}[adding], map[bool]string{true: "add", false: "remove"}[adding])
		}
		in := wizard.PickInput{Meta: meta, IO: a.promptIO()}
		if adding {
			in.Title = "Add dependencies (compatible with Spring Boot " + t.Boot.String() + ")"
			in.Boot, in.Exclude = &t.Boot, isPresent
		} else {
			if len(present) == 0 {
				return nil, nil, errors.New("no Spring Initializr dependencies were recognized in this project; name the ones to remove explicitly")
			}
			in.Title, in.Only = "Remove dependencies", present
		}
		ids, err := wizard.PickDependencies(ctx, in)
		return ids, nil, err
	}

	var prefer []string
	if !adding {
		prefer = present
	}
	res, err := a.resolveDependencyInputs(ctx, meta, args, prefer, interactive)
	if err != nil {
		return nil, nil, err
	}
	var ids, skipped []string
	var problems []string
	for _, d := range res.Deps {
		switch {
		case adding && isPresent[d.ID]:
			a.ui.note("%s is already in the project.", d.ID)
			skipped = append(skipped, d.ID)
		case adding && !d.CompatibleWith(t.Boot):
			problems = append(problems, fmt.Sprintf("dependency %q (%s) requires Spring Boot %s, but the project uses %s", d.ID, d.Name, d.Range, t.Boot))
		case !adding && !isPresent[d.ID] && !det.Ambiguous[d.ID]:
			a.ui.note("%s is not in the project.", d.ID)
			skipped = append(skipped, d.ID)
		default:
			ids = append(ids, d.ID)
		}
	}
	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return ids, skipped, nil
}

func confirm(app *App, question string) (bool, error) {
	fmt.Fprintf(app.Stdout, "%s [y/N] ", question)
	line, err := bufio.NewReader(app.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

func printDepDiff(app *App, proj *buildfile.Project, diff []buildfile.DiffLine) {
	u := app.ui
	if len(diff) == 0 {
		return
	}
	u.println()
	u.println(u.dim("--- " + filepath.Base(proj.File)))
	u.println(u.dim("+++ " + filepath.Base(proj.File)))
	for _, l := range diff {
		// Expand tabs uniformly: styled lines would otherwise be expanded by
		// the renderer while plain ones are not, misaligning the diff.
		l.Text = strings.ReplaceAll(l.Text, "\t", "    ")
		switch l.Op {
		case '@':
			u.println(u.accent(l.Text))
		case '+':
			u.println(u.ok("+" + l.Text))
		case '-':
			u.println(u.errc("-" + l.Text))
		default:
			u.println(" " + l.Text)
		}
	}
	u.println()
}

func printNotes(app *App, notes []buildfile.Note) {
	u := app.ui
	for _, n := range notes {
		u.warn("%s %s", describeKey(n.Key), n.Message)
		if n.Snippet != "" {
			for line := range strings.SplitSeq(n.Snippet, "\n") {
				fmt.Fprintln(app.Stderr, "    "+line)
			}
		}
	}
}

// describeKey renders a node key such as "plugin:org.apache.maven.plugins:x".
func describeKey(key string) string {
	kind, rest, ok := strings.Cut(key, ":")
	if !ok {
		return key
	}
	switch kind {
	case "plugin":
		return "plugin " + rest
	case "dep":
		return "dependency " + rest
	case "prop":
		return "property " + rest
	case "block":
		return rest + " { }"
	case "el":
		return "<" + rest + ">"
	case "repo":
		return "repository " + rest
	case "bom":
		return "BOM " + rest
	}
	return rest
}

func refreshHint(k buildfile.Kind) string {
	if k == buildfile.Maven {
		return "Reload the project in your IDE, or run ./mvnw compile, to fetch the changes."
	}
	return "Reload the project in your IDE, or run ./gradlew build, to fetch the changes."
}

// writeBuildFile writes the new content atomically, refusing if the file
// changed on disk since it was read.
func writeBuildFile(p *buildfile.Project, content string) error {
	current, err := os.ReadFile(p.File)
	if err != nil {
		return err
	}
	if !p.Unchanged(current) {
		return fmt.Errorf("%s changed while Sprout was working; nothing was written, please run the command again", p.File)
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(p.File); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := platform.WriteFileAtomic(p.File, []byte(p.Encode(content)), mode); err != nil {
		return fmt.Errorf("write %s: %w", p.File, err)
	}
	return nil
}
