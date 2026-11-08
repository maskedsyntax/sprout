package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maskedsyntax/sprout/internal/config"
	"github.com/maskedsyntax/sprout/internal/metadata"
)

// completionMetadata returns metadata for shell completion: cached data if
// present (even stale), else a quick network fetch. Errors yield nil so
// completion silently degrades.
func (a *App) completionMetadata(ctx context.Context) *metadata.Metadata {
	if ctx == nil {
		ctx = context.Background()
	}
	l, err := a.loader()
	if err != nil {
		return nil
	}
	if e, err := l.Store.Load(l.Client.BaseURL.String()); err == nil {
		if m, err := metadata.Parse(e.Metadata); err == nil {
			return m
		}
	}
	if a.offline {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	res, err := l.Load(ctx, false)
	if err != nil {
		return nil
	}
	return res.Metadata
}

func (a *App) completeField(id string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		m := a.completionMetadata(cmd.Context())
		if m == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		f := m.Field(id)
		if f == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, o := range f.Options {
			out = append(out, cobra.CompletionWithDesc(o.ID, o.Label()))
		}
		if id == metadata.FieldType {
			seen := map[string]bool{}
			for _, o := range f.Options {
				if o.Tags["format"] != "project" || o.Tags["build"] == "" {
					continue
				}
				short := o.Tags["build"]
				if d := o.Tags["dialect"]; d != "" {
					short += "-" + d
				}
				if !seen[short] {
					seen[short] = true
					out = append(out, cobra.CompletionWithDesc(short, o.Label()))
				}
			}
		}
		if id == metadata.FieldBootVersion {
			seen := map[string]bool{}
			for _, bv := range m.BootVersions() {
				line := strings.Join(strings.Split(bv.Version.String(), ".")[:2], ".") + ".x"
				if !seen[line] {
					seen[line] = true
					out = append(out, cobra.CompletionWithDesc(line, "latest "+line[:len(line)-2]+" release"))
				}
			}
			out = append(out, cobra.CompletionWithDesc("latest", "newest GA release"))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func (a *App) dependencyCompletions(cmd *cobra.Command, prefix string) []cobra.Completion {
	m := a.completionMetadata(cmd.Context())
	if m == nil {
		return nil
	}
	var out []cobra.Completion
	for _, d := range m.Dependencies() {
		if strings.HasPrefix(d.ID, strings.ToLower(prefix)) {
			out = append(out, cobra.CompletionWithDesc(d.ID, d.Name))
		}
	}
	return out
}

// completeDependencyList completes comma-separated dependency lists.
func (a *App) completeDependencyList(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	head, last := "", toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		head, last = toComplete[:i+1], toComplete[i+1:]
	}
	var out []cobra.Completion
	for _, c := range a.dependencyCompletions(cmd, last) {
		out = append(out, head+c)
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

func (a *App) completeDependencyArgs(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return a.dependencyCompletions(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

func (a *App) completeCategories(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	m := a.completionMetadata(cmd.Context())
	if m == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, g := range m.Groups {
		out = append(out, g.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completePresets completes a preset name as the first positional argument.
func (a *App) completePresets(cmd *cobra.Command, args []string, s string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return a.completePresetFlag(cmd, nil, s)
}

// completePresetFlag completes the value of --preset.
func (a *App) completePresetFlag(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	store, err := a.presetStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	list, err := store.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, p := range list {
		out = append(out, cobra.CompletionWithDesc(p.Name, summarizeOptions(p.Options)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeConfigKeys(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return config.KeyNames(), cobra.ShellCompDirectiveNoFileComp
}
