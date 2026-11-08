package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
)

// optionFlags are the project configuration flags shared by 'new',
// 'presets save' and 'presets edit'.
type optionFlags struct {
	opts project.Options
	deps []string
	set  []string
	fs   *pflag.FlagSet
}

func (f *optionFlags) register(cmd *cobra.Command, app *App) {
	fs := cmd.Flags()
	f.fs = fs
	o := &f.opts
	fs.StringVarP(&o.Type, "build", "b", "", "project type: a type ID (maven-project, gradle-project-kotlin, ...) or shorthand (maven, gradle, gradle-kotlin)")
	fs.StringVar(&o.Type, "type", "", "alias for --build")
	_ = fs.MarkHidden("type")
	fs.StringVarP(&o.Language, "language", "l", "", "language (java, kotlin, groovy, ...)")
	fs.StringVar(&o.BootVersion, "boot", "", "Spring Boot version: exact (4.1.2), line (4.1.x) or 'latest'")
	fs.StringVar(&o.JavaVersion, "java", "", "Java version (e.g. 21)")
	fs.StringVarP(&o.GroupID, "group", "g", "", "group ID (e.g. com.example)")
	fs.StringVarP(&o.ArtifactID, "artifact", "a", "", "artifact ID (defaults to the project name)")
	fs.StringVarP(&o.Name, "name", "n", "", "project display name (defaults to the artifact ID)")
	fs.StringVarP(&o.Description, "description", "d", "", "project description")
	fs.StringVar(&o.PackageName, "package", "", "base package (defaults to <group>.<artifact>)")
	fs.StringVar(&o.Version, "project-version", "", "project version (e.g. 0.0.1-SNAPSHOT)")
	fs.StringVar(&o.Packaging, "packaging", "", "packaging (jar, war, ...)")
	fs.StringVar(&o.ConfigFormat, "config-format", "", "application configuration file format (properties, yaml, ...)")
	fs.StringSliceVar(&f.deps, "deps", nil, "dependency IDs, comma-separated or repeated (see 'sprout deps')")
	fs.StringSliceVar(&f.deps, "dependencies", nil, "alias for --deps")
	_ = fs.MarkHidden("dependencies")
	fs.StringArrayVar(&f.set, "set", nil, "set any other server capability as key=value (repeatable)")

	comp := func(field string) {
		_ = cmd.RegisterFlagCompletionFunc(flagForField(field), app.completeField(field))
	}
	for _, id := range []string{metadata.FieldType, metadata.FieldLanguage, metadata.FieldBootVersion, metadata.FieldJavaVersion, metadata.FieldPackaging, metadata.FieldConfigFormat} {
		comp(id)
	}
	_ = cmd.RegisterFlagCompletionFunc("deps", app.completeDependencyList)
}

func flagForField(id string) string {
	switch id {
	case metadata.FieldType:
		return "build"
	case metadata.FieldVersion:
		return "project-version"
	}
	return project.KeyForField(id)
}

// options returns the options set explicitly on the command line.
func (f *optionFlags) options() (project.Options, error) {
	var out project.Options
	if f.fs == nil {
		return out, nil
	}
	for _, id := range project.KnownFieldIDs() {
		name := flagForField(id)
		changed := f.fs.Changed(name) || (id == metadata.FieldType && f.fs.Changed("type"))
		if changed {
			v := strings.TrimSpace(f.opts.Get(id))
			if v == "" {
				return out, usageErrorf("--%s must not be empty", name)
			}
			out.Set(id, v)
		}
	}
	out.Dependencies = project.SplitList(f.deps...)
	for _, kv := range f.set {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return out, usageErrorf("invalid --set %q: expected key=value", kv)
		}
		id := k
		if known, ok := project.FieldForKey(k); ok {
			id = known
		}
		if id == metadata.FieldDependencies {
			return out, usageErrorf("use --deps to select dependencies")
		}
		out.Set(id, strings.TrimSpace(v))
	}
	return out, nil
}

// fieldLabel is the display label for a capability.
func fieldLabel(id string) string {
	switch id {
	case metadata.FieldType:
		return "Project type"
	case metadata.FieldLanguage:
		return "Language"
	case metadata.FieldBootVersion:
		return "Spring Boot"
	case metadata.FieldGroupID:
		return "Group"
	case metadata.FieldArtifactID:
		return "Artifact"
	case metadata.FieldName:
		return "Name"
	case metadata.FieldDescription:
		return "Description"
	case metadata.FieldPackageName:
		return "Package"
	case metadata.FieldVersion:
		return "Version"
	case metadata.FieldPackaging:
		return "Packaging"
	case metadata.FieldJavaVersion:
		return "Java"
	case metadata.FieldConfigFormat:
		return "Config format"
	}
	return id
}

func describeOption(id, label string) string {
	if label == "" || strings.EqualFold(label, id) {
		return id
	}
	return fmt.Sprintf("%s (%s)", id, label)
}
