package cli

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/javagen"
	"github.com/maskedsyntax/sprout/internal/wizard"
)

type typeFlags struct {
	in     string
	dir    string
	module string
	dryRun bool
	yes    bool
	format formatFlag
}

func (f *typeFlags) register(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.in, "in", "", "destination package, relative to the base package (controller) or fully qualified")
	fs.StringVarP(&f.dir, "dir", "C", ".", "directory inside the project (searched upwards for the build file)")
	fs.StringVarP(&f.module, "module", "m", "", "module to use in a multi-module project (its path from the project root)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show what would be created without writing anything")
	fs.BoolVarP(&f.yes, "yes", "y", false, "create without asking for confirmation")
	f.format.register(cmd)
	_ = cmd.RegisterFlagCompletionFunc("in", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return completePackages(f.dir, f.module), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("module", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		if p, err := javagen.Find(f.dir); err == nil {
			return p.ModuleNames(), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.MarkFlagDirname("dir")
}

func completePackages(dir, module string) []string {
	p, err := javagen.Find(dir)
	if err != nil {
		return nil
	}
	mods := p.Modules
	if m, err := p.Module(module); module != "" && err == nil {
		mods = []*javagen.Module{m}
	} else if m := p.ModuleAt(dir); m != nil {
		mods = []*javagen.Module{m}
	}
	var out []string
	for _, m := range mods {
		out = append(out, m.Packages...)
	}
	return out
}

var typeExamples = map[javagen.Kind]string{
	javagen.Class: `  sprout new class User                     # pick the package from a list
  sprout new class User --in model          # com.example.demo.model.User
  sprout new class controller.UserController
  sprout new class User --in com.example.demo.model`,
	javagen.Interface: `  sprout new interface UserService --in service
  sprout new interface service.UserService`,
	javagen.Enum: `  sprout new enum UserStatus --in model
  sprout new enum model.UserStatus`,
	javagen.Record: `  sprout new record UserResponse --in dto
  sprout new record dto.UserResponse`,
	javagen.Package: `  sprout new package controller             # com.example.demo.controller
  sprout new package com.example.demo.web.api
  sprout new package admin --in controller   # com.example.demo.controller.admin`,
}

// newTypeCmds builds 'sprout new class|interface|enum|record|package'.
func newTypeCmds(app *App) []*cobra.Command {
	var cmds []*cobra.Command
	for _, k := range javagen.Kinds {
		var f typeFlags
		what, arg := "a Java "+string(k), "Name"
		if k == javagen.Package {
			what, arg = "a Java package", "name"
		}
		cmd := &cobra.Command{
			Use:   string(k) + " [" + arg + "]",
			Short: "Create " + what + " in the current project",
			Long: "Create " + what + ` in the current project, from the project root or any
folder inside it.

The destination package can be given as part of the name (dto.UserResponse),
with --in, or picked from a list. Package names are relative to the
application's base package (the package of the @SpringBootApplication class)
unless they are fully qualified: with base package com.example.demo,
"controller" and "com.example.demo.controller" are the same package.

Run from inside a package folder (src/main/java/...), a plain name goes into
that package. Missing package folders are created. Existing files are never
overwritten.`,
			Example:           typeExamples[k],
			Args:              cobra.MaximumNArgs(1),
			ValidArgsFunction: cobra.NoFileCompletions,
			RunE: func(cmd *cobra.Command, args []string) error {
				name := ""
				if len(args) == 1 {
					name = args[0]
				}
				return runNewType(cmd.Context(), app, &f, k, name, nil, false)
			},
		}
		f.register(cmd)
		cmds = append(cmds, cmd)
	}
	return cmds
}

type newTypeReport struct {
	*javagen.Target
	Module  string `json:"module"`
	Created bool   `json:"created"`
}

// runNewType creates a type or package. proj may be pre-loaded; prompted
// tells whether the user already answered a prompt (which asks for
// confirmation before writing).
func runNewType(ctx context.Context, app *App, f *typeFlags, kind javagen.Kind, name string, proj *javagen.Project, prompted bool) error {
	var err error
	if proj == nil {
		if proj, err = javagen.Find(f.dir); err != nil {
			return err
		}
	}
	jsonOut := f.format.json()
	interactive := app.interactive() && !jsonOut
	pio := app.promptIO()
	u := app.ui

	mod, asked, err := app.selectModule(ctx, proj, f, interactive)
	if err != nil {
		return err
	}
	prompted = prompted || asked

	in := f.in
	if here := mod.PackageAt(f.dir); in == "" && here != "" && !strings.Contains(name, ".") {
		in = here // run from inside a package folder
	}
	needPkg := in == "" && !strings.Contains(name, ".")
	if kind == javagen.Package {
		needPkg = in == "" && name == ""
	}
	if needPkg && interactive {
		title := "Select destination package:"
		if kind == javagen.Package {
			title = "Create the new package inside:"
		}
		if in, err = wizard.ChoosePackage(ctx, pio, mod, title, mod.BasePackage, kind != javagen.Package); err != nil {
			return err
		}
		prompted = true
	}

	if name == "" {
		if !interactive {
			example := map[bool]string{true: "controller", false: "User"}[kind == javagen.Package]
			return usageErrorf("name the %s to create, e.g. 'sprout new %s %s' (run in a terminal to be asked)", kind, kind, example)
		}
		examples := map[javagen.Kind]string{javagen.Class: "User", javagen.Interface: "UserService", javagen.Enum: "UserStatus",
			javagen.Record: "UserResponse", javagen.Package: "dto"}
		title, desc := kind.Label()+" name", "e.g. "+examples[kind]
		if in != "" {
			desc += " · in " + in
		}
		name, err = wizard.AskName(ctx, pio, title, desc, func(s string) error {
			_, err := mod.Plan(kind, s, in)
			return err
		})
		if err != nil {
			return err
		}
		prompted = true
	}

	t, err := mod.Plan(kind, name, in)
	if err != nil {
		return err
	}
	report := newTypeReport{Target: t, Module: mod.Name}
	shown := app.projectPath(proj, t.Path)

	if !jsonOut && (prompted || f.dryRun) {
		u.println()
		u.printf("%s %s %s\n", u.bold("Create"), kind, u.accent(shown))
		if t.Source != "" {
			u.println()
			for line := range strings.SplitSeq(strings.TrimRight(t.Source, "\n"), "\n") {
				u.println("    " + u.dim(line))
			}
		}
		u.println()
	}
	if f.dryRun {
		if jsonOut {
			return writeJSON(app.Stdout, report)
		}
		u.note("Dry run: nothing was created.")
		return nil
	}
	if prompted && interactive && !f.yes {
		ok, err := confirmYes(app, "Create it?")
		if err != nil {
			return err
		}
		if !ok {
			u.println("Aborted; nothing was created.")
			return nil
		}
	}
	if err := javagen.Create(t); err != nil {
		return err
	}
	report.Created = true
	if jsonOut {
		return writeJSON(app.Stdout, report)
	}
	fqn := t.Package
	if t.Name != "" {
		fqn += "." + t.Name
	}
	u.printf("%s Created %s %s\n", u.ok("✓"), kind, u.bold(fqn))
	u.printf("  %s\n", app.displayPath(t.Path))
	return nil
}

// selectModule picks the module to work in. It reports whether the user was
// asked.
func (a *App) selectModule(ctx context.Context, p *javagen.Project, f *typeFlags, interactive bool) (*javagen.Module, bool, error) {
	switch {
	case f.module != "":
		m, err := p.Module(f.module)
		return m, false, err
	case len(p.Modules) == 1:
		return p.Modules[0], false, nil
	}
	if m := p.ModuleAt(f.dir); m != nil {
		return m, false, nil
	}
	if !interactive {
		return nil, false, fmt.Errorf("%s has %d modules with Java sources (%s); choose one with --module, or run Sprout inside the module",
			a.displayPath(p.Root), len(p.Modules), strings.Join(p.ModuleNames(), ", "))
	}
	m, err := wizard.ChooseModule(ctx, a.promptIO(), p.Modules)
	return m, true, err
}

// projectPath shows path relative to the project root.
func (a *App) projectPath(p *javagen.Project, path string) string {
	if rel, err := filepath.Rel(p.Root, path); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return path
}

// confirmYes asks a yes/no question that defaults to yes.
func confirmYes(app *App, question string) (bool, error) {
	fmt.Fprintf(app.Stdout, "%s [Y/n] ", question)
	line, err := bufio.NewReader(app.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "" || a == "y" || a == "yes", nil
}
