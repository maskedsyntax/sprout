package wizard

import (
	"context"
	"fmt"
	"strings"

	"charm.land/huh/v2"

	"github.com/maskedsyntax/sprout/internal/javagen"
)

// NewProjectChoice is returned by ChooseKind when the user wants a new
// Spring Boot project instead of a Java type.
const NewProjectChoice = "project"

// ChooseKind asks what to create. With withProject, a "New Spring Boot
// project" entry is offered last and NewProjectChoice is returned for it.
func ChooseKind(ctx context.Context, o IO, withProject bool) (string, error) {
	var opts []huh.Option[string]
	for _, k := range javagen.Kinds {
		opts = append(opts, huh.NewOption(k.Label(), string(k)))
	}
	if withProject {
		opts = append(opts, huh.NewOption("New Spring Boot project…", NewProjectChoice))
	}
	choice := string(javagen.Class)
	sel := huh.NewSelect[string]().Title("What would you like to create?").Options(opts...).Value(&choice)
	if err := o.form(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return "", err
	}
	return choice, nil
}

// ChooseModule asks which module (source root) to use.
func ChooseModule(ctx context.Context, o IO, mods []*javagen.Module) (*javagen.Module, error) {
	var opts []huh.Option[int]
	for i, m := range mods {
		label := m.Name
		if m.BasePackage != "" {
			label += "  (" + m.BasePackage + ")"
		}
		opts = append(opts, huh.NewOption(label, i))
	}
	choice := 0
	sel := huh.NewSelect[int]().Title("Select module:").Options(opts...).Value(&choice)
	if err := o.form(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return nil, err
	}
	return mods[choice], nil
}

const newPackageChoice = "\x00new"

// ChoosePackage shows a searchable list of the module's packages, with def
// preselected. With allowNew, "+ Create new package" lets the user type a
// package (relative to the base package or fully qualified). It returns a
// fully qualified package.
func ChoosePackage(ctx context.Context, o IO, m *javagen.Module, title, def string, allowNew bool) (string, error) {
	var opts []huh.Option[string]
	for _, p := range m.Packages {
		opts = append(opts, huh.NewOption(p, p))
	}
	if allowNew || len(opts) == 0 {
		opts = append(opts, huh.NewOption("+ Create new package", newPackageChoice))
	}
	choice := def
	if choice == "" && len(m.Packages) > 0 {
		choice = m.Packages[0]
	}
	sel := huh.NewSelect[string]().Title(title).Options(opts...).Height(min(len(opts)+2, 16)).Value(&choice)
	if err := o.form(huh.NewGroup(sel)).RunWithContext(ctx); err != nil {
		return "", err
	}
	if choice != newPackageChoice {
		return choice, nil
	}
	desc := "Fully qualified, e.g. com.example.app.dto"
	if m.BasePackage != "" {
		desc = fmt.Sprintf("Relative to %s (e.g. dto) or fully qualified", m.BasePackage)
	}
	name, err := AskName(ctx, o, "New package name", desc, func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("enter a package name")
		}
		_, err := m.ResolvePackage(s)
		return err
	})
	if err != nil {
		return "", err
	}
	return m.ResolvePackage(name)
}

// AskName asks for a name, validating as the user types.
func AskName(ctx context.Context, o IO, title, description string, validate func(string) error) (string, error) {
	var v string
	in := huh.NewInput().Title(title).Description(description).Value(&v).Validate(validate)
	if err := o.form(huh.NewGroup(in)).RunWithContext(ctx); err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}
