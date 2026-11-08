package wizard

import (
	"context"
	"fmt"
	"io"

	"charm.land/huh/v2"

	"github.com/maskedsyntax/sprout/internal/metadata"
)

// IO configures terminal streams and accessibility for standalone prompts.
type IO struct {
	In         io.Reader
	Out        io.Writer
	NoColor    bool
	Accessible bool
}

func (o IO) form(groups ...*huh.Group) *huh.Form {
	theme := huh.ThemeFunc(huh.ThemeBase16)
	if o.NoColor {
		theme = huh.ThemeFunc(huh.ThemeBase)
	}
	f := huh.NewForm(groups...).WithTheme(theme).WithShowHelp(true).WithAccessible(o.Accessible)
	if o.In != nil {
		f = f.WithInput(o.In)
	}
	if o.Out != nil {
		f = f.WithOutput(o.Out)
	}
	return f
}

// PickInput configures PickDependencies.
type PickInput struct {
	Meta  *metadata.Metadata
	Title string
	// Boot hides dependencies incompatible with this version (nil: show all).
	Boot *metadata.Version
	// Only restricts the choices to these IDs (nil: every dependency).
	Only []string
	// Exclude hides these IDs (e.g. dependencies already in the project).
	Exclude map[string]bool
	IO
}

// ErrNothingToPick is returned when no dependency can be offered.
var ErrNothingToPick = fmt.Errorf("no dependencies to choose from")

// PickDependencies shows a searchable multi-select of dependencies and
// returns the chosen IDs. The highlighted dependency's description and
// Spring Boot compatibility are shown as you move through the list.
func PickDependencies(ctx context.Context, in PickInput) ([]string, error) {
	only := map[string]bool{}
	for _, id := range in.Only {
		only[id] = true
	}
	var opts []huh.Option[string]
	for _, d := range in.Meta.Dependencies() {
		if in.Exclude[d.ID] || (in.Only != nil && !only[d.ID]) || (in.Boot != nil && !d.CompatibleWith(*in.Boot)) {
			continue
		}
		opts = append(opts, huh.NewOption(dependencyLabel(d), d.ID))
	}
	if len(opts) == 0 {
		return nil, ErrNothingToPick
	}
	var selected []string
	noBoot := ""
	ms := huh.NewMultiSelect[string]().
		Title(in.Title).
		Options(opts...).
		Filterable(true).
		Height(min(len(opts)+4, 16)).
		Value(&selected)
	ms.DescriptionFunc(func() string { return hoverDescription(in.Meta, ms) }, hoverBinding{ms: ms, boot: &noBoot})
	if err := in.form(huh.NewGroup(ms)).RunWithContext(ctx); err != nil {
		return nil, err
	}
	return selected, nil
}

// ChooseDependency asks which candidate the user meant by input. It returns
// nil when the user chooses to skip.
func ChooseDependency(ctx context.Context, input string, candidates []*metadata.Dependency, o IO) (*metadata.Dependency, error) {
	var opts []huh.Option[string]
	for _, d := range candidates {
		opts = append(opts, huh.NewOption(dependencyLabel(d), d.ID))
	}
	opts = append(opts, huh.NewOption("None of these (skip)", ""))
	choice := candidates[0].ID
	sel := huh.NewSelect[string]().
		Title(fmt.Sprintf("%q is not a dependency ID. Did you mean:", input)).
		Options(opts...).
		Value(&choice)
	if err := o.form(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return nil, err
	}
	for _, d := range candidates {
		if d.ID == choice {
			return d, nil
		}
	}
	return nil, nil
}

func dependencyLabel(d *metadata.Dependency) string {
	return fmt.Sprintf("%s  [%s] · %s", d.Name, d.ID, d.Group)
}

func hoverDescription(m *metadata.Metadata, ms *huh.MultiSelect[string]) string {
	help := "Space select · / search · Enter confirm"
	k, ok := ms.Hovered()
	if !ok {
		return help
	}
	d, found := m.Dependency(k)
	if !found {
		return help
	}
	desc := d.Description
	if !d.Range.IsZero() {
		desc += " (Spring Boot " + d.Range.String() + ")"
	}
	return help + "\n" + desc
}
