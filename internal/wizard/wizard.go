// Package wizard implements the interactive project wizard using Huh forms.
// Every step is built from server metadata, so new capabilities and values
// appear automatically.
package wizard

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"unicode"

	"charm.land/huh/v2"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

// Input configures the wizard.
type Input struct {
	Meta    *metadata.Metadata
	Initial project.Options
	Sources project.Sources
	// Output is an explicit output directory (from --output); may be empty.
	Output string
	// DefaultOutput computes the default output path for an artifact ID.
	DefaultOutput func(artifact string) string
	// Describe renders the review summary for a resolved request.
	Describe func(req *project.Request, output string) string

	In         io.Reader
	Out        io.Writer
	Accessible bool
	NoColor    bool
}

// Result is the confirmed configuration.
type Result struct {
	Options project.Options
	Output  string
}

// ErrDeclined is returned when the user reviews and declines generation.
var ErrDeclined = errors.New("generation declined")

type state struct {
	Name, Type, Language, Boot, Group, Artifact, Description, Package, Version string
	Packaging, Java, ConfigFormat, Output                                      string
	Deps                                                                       []string
	Extra                                                                      map[string]*string
	Confirm                                                                    bool
}

func (s *state) artifact() string {
	if v := strings.TrimSpace(s.Artifact); v != "" {
		return v
	}
	return Slug(s.Name)
}

func (s *state) options() project.Options {
	o := project.Options{
		Type: s.Type, Language: s.Language, BootVersion: s.Boot, GroupID: strings.TrimSpace(s.Group),
		ArtifactID: s.artifact(), Name: strings.TrimSpace(s.Name), Description: strings.TrimSpace(s.Description),
		PackageName: strings.TrimSpace(s.Package), Version: strings.TrimSpace(s.Version),
		Packaging: s.Packaging, JavaVersion: s.Java, ConfigFormat: s.ConfigFormat,
		Dependencies: append([]string(nil), s.Deps...),
	}
	for k, v := range s.Extra {
		if strings.TrimSpace(*v) != "" {
			o.Set(k, strings.TrimSpace(*v))
		}
	}
	return o
}

// Run shows the wizard and returns the confirmed configuration.
func Run(ctx context.Context, in Input) (*Result, error) {
	st := initialState(in)
	w := &builder{in: in, st: st}
	groups := w.groups()
	theme := huh.ThemeFunc(huh.ThemeBase16)
	if in.NoColor {
		theme = huh.ThemeFunc(huh.ThemeBase)
	}
	form := huh.NewForm(groups...).
		WithTheme(theme).
		WithShowHelp(true).
		WithAccessible(in.Accessible)
	if in.In != nil {
		form = form.WithInput(in.In)
	}
	if in.Out != nil {
		form = form.WithOutput(in.Out)
	}
	if err := form.RunWithContext(ctx); err != nil {
		return nil, err
	}
	if !st.Confirm {
		return nil, ErrDeclined
	}
	return &Result{Options: st.options(), Output: w.output()}, nil
}

func initialState(in Input) *state {
	m, o := in.Meta, in.Initial
	sel := func(id, val string) string {
		f := m.Field(id)
		if f == nil {
			return ""
		}
		if id == metadata.FieldBootVersion && val != "" {
			if opt, err := m.ResolveBootVersion(val); err == nil {
				return opt.ID
			}
		}
		if id == metadata.FieldType && val != "" {
			if opt, ok := project.ResolveType(f, val); ok {
				return opt.ID
			}
		}
		if opt, ok := f.Option(val); ok && val != "" {
			return opt.ID
		}
		if _, ok := f.Option(f.Default); ok {
			return f.Default
		}
		if len(f.Options) > 0 {
			return f.Options[0].ID
		}
		return ""
	}
	text := func(id, val string) string {
		if val != "" {
			return val
		}
		if f := m.Field(id); f != nil {
			return f.Default
		}
		return ""
	}
	st := &state{
		Name:         firstNonEmpty(o.Name, o.ArtifactID),
		Type:         sel(metadata.FieldType, o.Type),
		Language:     sel(metadata.FieldLanguage, o.Language),
		Boot:         sel(metadata.FieldBootVersion, o.BootVersion),
		Group:        text(metadata.FieldGroupID, o.GroupID),
		Artifact:     o.ArtifactID,
		Description:  text(metadata.FieldDescription, o.Description),
		Package:      o.PackageName,
		Version:      text(metadata.FieldVersion, o.Version),
		Packaging:    sel(metadata.FieldPackaging, o.Packaging),
		Java:         sel(metadata.FieldJavaVersion, o.JavaVersion),
		ConfigFormat: sel(metadata.FieldConfigFormat, o.ConfigFormat),
		Output:       in.Output,
		Extra:        map[string]*string{},
		Confirm:      true,
	}
	if st.Artifact == st.Name {
		st.Artifact = "" // keep deriving from the name until edited
	}
	for _, d := range o.Dependencies {
		if dep, ok := m.Dependency(d); ok {
			st.Deps = append(st.Deps, dep.ID)
		}
	}
	for _, f := range extraFields(m) {
		v := o.Get(f.ID)
		if f.Kind == metadata.KindSingleSelect {
			v = sel(f.ID, v)
		} else {
			v = text(f.ID, v)
		}
		st.Extra[f.ID] = &v
	}
	return st
}

// extraFields returns capabilities without a dedicated wizard step.
func extraFields(m *metadata.Metadata) []*metadata.Field {
	var out []*metadata.Field
	for _, f := range m.Fields {
		if project.IsKnownField(f.ID) {
			continue
		}
		if f.Kind == metadata.KindSingleSelect || f.Kind == metadata.KindText {
			out = append(out, f)
		}
	}
	return out
}

type builder struct {
	in Input
	st *state
}

func (w *builder) output() string {
	if strings.TrimSpace(w.st.Output) != "" {
		return strings.TrimSpace(w.st.Output)
	}
	if w.in.DefaultOutput != nil {
		return w.in.DefaultOutput(w.st.artifact())
	}
	return w.st.artifact()
}

func (w *builder) groups() []*huh.Group {
	m, st := w.in.Meta, w.st
	var groups []*huh.Group

	groups = append(groups, huh.NewGroup(
		huh.NewInput().
			Title("Project name").
			Description("Enter/Tab next · Shift+Tab back · Ctrl+C cancel").
			Placeholder("my-app").
			Value(&st.Name).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a project name is required")
				}
				if Slug(s) == "" {
					return errors.New("the name must contain letters or digits")
				}
				return nil
			}),
	).Title("Project"))

	var build []huh.Field
	if f := m.Field(metadata.FieldType); f != nil {
		build = append(build, w.selectField(f, "Project type", &st.Type))
	}
	if f := m.Field(metadata.FieldLanguage); f != nil {
		build = append(build, w.selectField(f, "Language", &st.Language))
	}
	if f := m.Field(metadata.FieldBootVersion); f != nil {
		build = append(build, huh.NewSelect[string]().
			Title("Spring Boot version").
			Options(bootOptions(m)...).
			Value(&st.Boot).
			Height(min(len(f.Options)+2, 10)))
	}
	if len(build) > 0 {
		groups = append(groups, huh.NewGroup(build...).Title("Build"))
	}

	var meta []huh.Field
	if m.Field(metadata.FieldGroupID) != nil {
		meta = append(meta, huh.NewInput().Title("Group ID").Value(&st.Group).
			Validate(project.ValidateGroupID))
	}
	if m.Field(metadata.FieldArtifactID) != nil {
		meta = append(meta, huh.NewInput().Title("Artifact ID").
			PlaceholderFunc(func() string { return Slug(st.Name) }, &st.Name).
			Value(&st.Artifact).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return nil
				}
				return project.ValidateArtifactID(strings.TrimSpace(s))
			}))
	}
	if m.Field(metadata.FieldDescription) != nil {
		meta = append(meta, huh.NewInput().Title("Description").Value(&st.Description))
	}
	if m.Field(metadata.FieldPackageName) != nil {
		meta = append(meta, huh.NewInput().Title("Package name").
			PlaceholderFunc(func() string { return project.DerivePackageName(st.Group, st.artifact()) }, st).
			Value(&st.Package).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return nil
				}
				return project.ValidatePackageName(strings.TrimSpace(s))
			}))
	}
	if m.Field(metadata.FieldVersion) != nil {
		meta = append(meta, huh.NewInput().Title("Project version").Value(&st.Version))
	}
	if len(meta) > 0 {
		groups = append(groups, huh.NewGroup(meta...).Title("Metadata"))
	}

	var runtime []huh.Field
	if f := m.Field(metadata.FieldPackaging); f != nil {
		runtime = append(runtime, w.selectField(f, "Packaging", &st.Packaging))
	}
	if f := m.Field(metadata.FieldJavaVersion); f != nil {
		runtime = append(runtime, w.selectField(f, "Java version", &st.Java))
	}
	if f := m.Field(metadata.FieldConfigFormat); f != nil {
		runtime = append(runtime, w.selectField(f, "Configuration file format", &st.ConfigFormat))
	}
	if len(runtime) > 0 {
		groups = append(groups, huh.NewGroup(runtime...).Title("Runtime"))
	}

	var extra []huh.Field
	for _, f := range extraFields(m) {
		v := st.Extra[f.ID]
		if f.Kind == metadata.KindSingleSelect {
			extra = append(extra, w.selectField(f, f.ID, v))
		} else {
			extra = append(extra, huh.NewInput().Title(f.ID).Value(v))
		}
	}
	if len(extra) > 0 {
		groups = append(groups, huh.NewGroup(extra...).Title("Additional options"))
	}

	if m.HasDependencies {
		groups = append(groups, huh.NewGroup(w.dependencyField()).Title("Dependencies"))
	}

	groups = append(groups, huh.NewGroup(
		huh.NewInput().Title("Output directory").
			PlaceholderFunc(func() string {
				if w.in.DefaultOutput != nil {
					return w.in.DefaultOutput(st.artifact())
				}
				return st.artifact()
			}, st).
			Value(&st.Output),
	).Title("Output"))

	groups = append(groups, huh.NewGroup(
		huh.NewNote().Title("Review").DescriptionFunc(w.review, st),
		huh.NewConfirm().Title("Generate this project?").
			Affirmative("Generate").Negative("Cancel").
			Value(&st.Confirm).
			Validate(func(ok bool) error {
				if !ok {
					return nil
				}
				_, err := project.Resolve(m, st.options(), nil)
				if err != nil {
					return errors.New("fix the problems listed above (Shift+Tab to go back)")
				}
				return nil
			}),
	))
	return groups
}

func (w *builder) selectField(f *metadata.Field, title string, value *string) huh.Field {
	opts := make([]huh.Option[string], len(f.Options))
	for i, o := range f.Options {
		label := o.Label()
		if o.ID == f.Default {
			label += " (default)"
		}
		opts[i] = huh.NewOption(label, o.ID)
	}
	return huh.NewSelect[string]().Title(title).Options(opts...).Value(value).Height(min(len(opts)+2, 10))
}

func bootOptions(m *metadata.Metadata) []huh.Option[string] {
	f := m.Field(metadata.FieldBootVersion)
	opts := make([]huh.Option[string], 0, len(f.Options))
	for _, o := range f.Options {
		label := o.Label()
		if v, err := metadata.ParseVersion(o.ID); err == nil && !v.Stable() && !strings.Contains(label, "(") {
			label += " (" + string(v.Kind()) + ")"
		}
		if o.ID == f.Default {
			label += " · default"
		}
		opts = append(opts, huh.NewOption(label, o.ID))
	}
	return opts
}

// hoverBinding makes Huh re-evaluate a description whenever the hovered
// option changes (Huh caches dynamic text by the hash of its bindings).
type hoverBinding struct {
	ms   *huh.MultiSelect[string]
	boot *string
}

func (h hoverBinding) Hash() (uint64, error) {
	k, _ := h.ms.Hovered()
	f := fnv.New64a()
	f.Write([]byte(k + "\x00" + *h.boot))
	return f.Sum64(), nil
}

func (w *builder) dependencyField() huh.Field {
	m, st := w.in.Meta, w.st
	ms := huh.NewMultiSelect[string]().
		Title("Dependencies").
		Filterable(true).
		Height(14)
	ms.OptionsFunc(func() []huh.Option[string] {
		var boot *metadata.Version
		if v, err := metadata.ParseVersion(st.Boot); err == nil {
			boot = &v
		}
		selected := map[string]bool{}
		for _, d := range st.Deps {
			selected[d] = true
		}
		var opts []huh.Option[string]
		for _, d := range m.Dependencies() {
			if boot != nil && !d.CompatibleWith(*boot) {
				continue
			}
			opts = append(opts, huh.NewOption(dependencyLabel(d), d.ID).Selected(selected[d.ID]))
		}
		return opts
	}, &st.Boot)
	ms.Value(&st.Deps)
	ms.DescriptionFunc(func() string { return hoverDescription(m, ms) }, hoverBinding{ms: ms, boot: &st.Boot})
	return ms
}

// review renders the summary. Huh notes interpret a small Markdown subset
// (_ * ` \), so the text is escaped to show package names such as
// com.example.my_api and Windows paths verbatim.
func (w *builder) review() string { return escapeNote(w.reviewText()) }

func escapeNote(s string) string {
	return strings.NewReplacer(`\`, `\\`, "_", `\_`, "*", `\*`, "`", "\\`").Replace(s)
}

func (w *builder) reviewText() string {
	req, err := project.Resolve(w.in.Meta, w.st.options(), nil)
	if err != nil {
		if ve, ok := errors.AsType[*project.ValidationError](err); ok {
			lines := []string{"Problems:"}
			for _, p := range ve.Problems {
				lines = append(lines, "  • "+p.Message)
			}
			return strings.Join(lines, "\n")
		}
		return err.Error()
	}
	if w.in.Describe != nil {
		return w.in.Describe(req, w.output())
	}
	var lines []string
	for _, v := range req.Values {
		lines = append(lines, fmt.Sprintf("%s: %s", v.Field, v.Value))
	}
	ids := req.DependencyIDs()
	sort.Strings(ids)
	lines = append(lines, "dependencies: "+strings.Join(ids, ", "), "output: "+w.output())
	return strings.Join(lines, "\n")
}

// Slug converts a display name into an artifact ID ("My API" -> "my-api").
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
			dash = false
		case r == '.' || r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-.")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
