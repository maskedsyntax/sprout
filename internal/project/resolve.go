package project

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/maskedsyntax/sprout/internal/metadata"
)

// Value is one resolved capability value that will be sent to the server.
type Value struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
}

// Request is a fully resolved, validated generation request.
type Request struct {
	Type         metadata.Option        `json:"type"`
	Values       []Value                `json:"values"`
	Dependencies []*metadata.Dependency `json:"dependencies"`
}

// Get returns the resolved value of a capability, or "".
func (r *Request) Get(id string) string {
	if id == metadata.FieldType {
		return r.Type.ID
	}
	for _, v := range r.Values {
		if v.Field == id {
			return v.Value
		}
	}
	return ""
}

// Label returns the display label of a resolved value (falling back to the
// value itself).
func (r *Request) Label(id string) string {
	for _, v := range r.Values {
		if v.Field == id {
			if v.Label != "" {
				return v.Label
			}
			return v.Value
		}
	}
	return ""
}

// DependencyIDs returns the selected dependency IDs.
func (r *Request) DependencyIDs() []string {
	ids := make([]string, len(r.Dependencies))
	for i, d := range r.Dependencies {
		ids[i] = d.ID
	}
	return ids
}

// IsArchive reports whether the project type produces a ZIP archive rather
// than a single build file (e.g. "maven-build" returns only pom.xml).
func (r *Request) IsArchive() bool {
	if f, ok := r.Type.Tags["format"]; ok {
		return f != "build"
	}
	return strings.HasSuffix(r.Type.Action, ".zip") || r.Type.Action == ""
}

// Query returns the URL query parameters for the generation endpoint.
func (r *Request) Query() url.Values {
	q := url.Values{}
	q.Set(metadata.FieldType, r.Type.ID)
	for _, v := range r.Values {
		q.Set(v.Field, v.Value)
	}
	if len(r.Dependencies) > 0 {
		q.Set(metadata.FieldDependencies, strings.Join(r.DependencyIDs(), ","))
	}
	return q
}

// Options converts the request back into options (used for presets and
// history so a configuration can be replayed).
func (r *Request) Options() Options {
	var o Options
	o.Type = r.Type.ID
	for _, v := range r.Values {
		o.Set(v.Field, v.Value)
	}
	o.Dependencies = r.DependencyIDs()
	return o
}

// Problem is a single validation failure.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError lists every problem found while resolving options.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0].Message
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems with the project configuration:", len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  - " + p.Message)
	}
	return b.String()
}

type resolver struct {
	meta  *metadata.Metadata
	opts  Options
	src   Sources
	probs []Problem
	text  map[string]string
}

// Resolve validates options against server metadata and fills in defaults.
// It returns a *ValidationError describing every problem found.
func Resolve(meta *metadata.Metadata, opts Options, src Sources) (*Request, error) {
	if src == nil {
		src = Sources{}
	}
	r := &resolver{meta: meta, opts: opts, src: src, text: map[string]string{}}
	req := &Request{}

	for _, id := range opts.SetIDs() {
		if meta.Field(id) == nil {
			r.fail(id, "%s is not supported by this Spring Initializr server", r.describe(id))
		}
	}
	req.Type = r.resolveType()

	for _, f := range meta.Fields {
		var v Value
		var ok bool
		switch f.Kind {
		case metadata.KindAction:
			continue
		case metadata.KindSingleSelect:
			v, ok = r.resolveSelect(f)
		case metadata.KindText:
			v, ok = r.resolveText(f)
		default:
			if opts.Get(f.ID) != "" {
				r.fail(f.ID, "%s uses unsupported capability type %q", r.describe(f.ID), f.Kind)
			}
			continue
		}
		if ok && v.Value != "" {
			req.Values = append(req.Values, v)
		}
	}
	sortValues(req.Values)
	req.Dependencies = r.resolveDependencies(req.Get(metadata.FieldBootVersion))
	if len(r.probs) > 0 {
		return nil, &ValidationError{Problems: r.probs}
	}
	return req, nil
}

// sortValues orders known fields logically; other capabilities keep the
// server order after them.
func sortValues(vals []Value) {
	rank := func(id string) int {
		for i, k := range knownFields {
			if k.id == id {
				return i
			}
		}
		return len(knownFields)
	}
	sort.SliceStable(vals, func(i, j int) bool { return rank(vals[i].Field) < rank(vals[j].Field) })
}

func (r *resolver) fail(field, format string, args ...any) {
	r.probs = append(r.probs, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
}

// describe names a field together with where its value came from.
func (r *resolver) describe(id string) string {
	s := fmt.Sprintf("%s %q", KeyForField(id), r.opts.Get(id))
	if src := r.src[id]; src != "" {
		s += " (from " + src + ")"
	}
	return s
}

func (r *resolver) resolveType() metadata.Option {
	f := r.meta.Field(metadata.FieldType)
	in := r.opts.Type
	if in == "" {
		o, ok := f.Option(f.Default)
		if !ok {
			o = f.Options[0]
		}
		return o
	}
	if o, ok := ResolveType(f, in); ok {
		return o
	}
	r.fail(metadata.FieldType, "unknown project type %s; available: %s", r.describe(metadata.FieldType), strings.Join(f.OptionIDs(), ", "))
	return metadata.Option{}
}

// ResolveType matches a type ID, name or shorthand such as "maven",
// "gradle", "gradle-kotlin" or "gradle-groovy" using the type tags advertised
// by the server.
func ResolveType(f *metadata.Field, in string) (metadata.Option, bool) {
	if o, ok := f.Option(in); ok {
		return o, true
	}
	build, dialect, _ := strings.Cut(strings.ToLower(strings.TrimSpace(in)), "-")
	var cands []metadata.Option
	for _, o := range f.Options {
		if !strings.EqualFold(o.Tags["build"], build) {
			continue
		}
		if fm := o.Tags["format"]; fm != "" && fm != "project" {
			continue
		}
		if dialect != "" && !strings.EqualFold(o.Tags["dialect"], dialect) {
			continue
		}
		cands = append(cands, o)
	}
	if len(cands) == 0 {
		return metadata.Option{}, false
	}
	for _, o := range cands {
		if o.ID == f.Default {
			return o, true
		}
	}
	for _, o := range cands {
		if o.Tags["dialect"] == "" {
			return o, true
		}
	}
	return cands[0], true
}

func (r *resolver) resolveSelect(f *metadata.Field) (Value, bool) {
	in := r.opts.Get(f.ID)
	source := r.src[f.ID]
	if in == "" {
		in, source = f.Default, "server default"
	}
	if in == "" {
		return Value{}, false
	}
	var (
		o   metadata.Option
		ok  bool
		err error
	)
	if f.ID == metadata.FieldBootVersion {
		o, err = r.meta.ResolveBootVersion(in)
		ok = err == nil
	} else {
		o, ok = f.Option(in)
	}
	if !ok {
		r.fail(f.ID, "%s is not available; choose one of: %s", r.describe(f.ID), strings.Join(f.OptionIDs(), ", "))
		return Value{}, false
	}
	return Value{Field: f.ID, Value: o.ID, Label: o.Label(), Source: source}, true
}

func (r *resolver) resolveText(f *metadata.Field) (Value, bool) {
	val, source := r.textValue(f.ID)
	if val == "" {
		return Value{}, false
	}
	var err error
	switch f.ID {
	case metadata.FieldGroupID:
		err = ValidateGroupID(val)
	case metadata.FieldArtifactID:
		err = ValidateArtifactID(val)
	case metadata.FieldPackageName:
		err = ValidatePackageName(val)
	case metadata.FieldVersion:
		if strings.ContainsAny(val, " \t\n") {
			err = fmt.Errorf("invalid project version %q: must not contain whitespace", val)
		}
	}
	if err != nil {
		if s := r.src[f.ID]; s != "" {
			err = fmt.Errorf("%w (from %s)", err, s)
		}
		r.fail(f.ID, "%s", err)
		return Value{}, false
	}
	return Value{Field: f.ID, Value: val, Source: source}, true
}

// textValue computes a text value with Initializr-style derivations:
// name defaults to the artifact ID and the package name is derived from the
// group and artifact IDs.
func (r *resolver) textValue(id string) (string, string) {
	if v := strings.TrimSpace(r.opts.Get(id)); v != "" {
		return v, r.src[id]
	}
	switch id {
	case metadata.FieldName:
		if a, _ := r.textValue(metadata.FieldArtifactID); a != "" {
			return a, "artifact"
		}
	case metadata.FieldPackageName:
		g, _ := r.textValue(metadata.FieldGroupID)
		a, _ := r.textValue(metadata.FieldArtifactID)
		if g != "" || a != "" {
			return DerivePackageName(g, a), "group + artifact"
		}
	}
	if f := r.meta.Field(id); f != nil && f.Default != "" {
		return f.Default, "server default"
	}
	return "", ""
}

func (r *resolver) resolveDependencies(boot string) []*metadata.Dependency {
	if len(r.opts.Dependencies) == 0 {
		return nil
	}
	if !r.meta.HasDependencies {
		r.fail(metadata.FieldDependencies, "this Spring Initializr server does not support dependencies")
		return nil
	}
	var bootVersion *metadata.Version
	if boot != "" {
		if v, err := metadata.ParseVersion(boot); err == nil {
			bootVersion = &v
		}
	}
	var out []*metadata.Dependency
	seen := map[string]bool{}
	for _, id := range r.opts.Dependencies {
		id = strings.TrimSpace(id)
		key := strings.ToLower(id)
		if id == "" || seen[key] {
			continue
		}
		seen[key] = true
		from := ""
		if s := r.src["dep:"+key]; s != "" {
			from = " (from " + s + ")"
		}
		d, ok := r.meta.Dependency(id)
		if !ok {
			msg := fmt.Sprintf("unknown dependency %q%s", id, from)
			if sug := r.meta.Suggest(id, 3); len(sug) > 0 {
				msg += "; did you mean " + strings.Join(sug, ", ") + "?"
			} else {
				msg += "; run 'sprout deps search " + id + "'"
			}
			r.fail(metadata.FieldDependencies, "%s", msg)
			continue
		}
		if bootVersion != nil && !d.CompatibleWith(*bootVersion) {
			r.fail(metadata.FieldDependencies, "dependency %q (%s)%s requires Spring Boot %s, but %s is selected",
				d.ID, d.Name, from, d.Range, boot)
			continue
		}
		out = append(out, d)
	}
	return out
}
