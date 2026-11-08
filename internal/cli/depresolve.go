package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/maskedsyntax/sprout/internal/metadata"
	"github.com/maskedsyntax/sprout/internal/project"
	"github.com/maskedsyntax/sprout/internal/wizard"
)

func (a *App) promptIO() wizard.IO {
	return wizard.IO{In: a.Stdin, Out: a.Stdout, NoColor: a.noColor || os.Getenv("NO_COLOR") != "", Accessible: os.Getenv("ACCESSIBLE") != ""}
}

// depResolution is the outcome of resolving user-typed dependency names.
type depResolution struct {
	Deps    []*metadata.Dependency
	Unknown []string // inputs left unresolved (non-interactive)
}

// resolveDependencyInputs maps user input to dependencies:
//
//   - exact IDs are used as-is;
//   - unambiguous corrections ignoring case and separators ("datajpa",
//     "Spring Data JPA") are applied with a note;
//   - otherwise the user picks from close matches when interactive. When
//     not interactive the input is reported as unknown, with suggestions.
//
// prefer, when non-empty, limits suggestions to those IDs (e.g. the
// dependencies a project already has, for 'remove').
func (a *App) resolveDependencyInputs(ctx context.Context, meta *metadata.Metadata, inputs []string, prefer []string, interactive bool) (*depResolution, error) {
	res := &depResolution{}
	seen := map[string]bool{}
	add := func(d *metadata.Dependency) {
		if !seen[d.ID] {
			seen[d.ID] = true
			res.Deps = append(res.Deps, d)
		}
	}
	preferred := map[string]bool{}
	for _, id := range prefer {
		preferred[id] = true
	}
	var problems []string
	for _, in := range splitCommas(inputs) {
		m := meta.Match(in, 8)
		switch {
		case m.Exact != nil:
			add(m.Exact)
			continue
		case m.Corrected != nil:
			a.ui.note("Using %s for %q.", m.Corrected.ID, in)
			add(m.Corrected)
			continue
		}
		cands := m.Candidates
		if len(preferred) > 0 {
			var filtered []*metadata.Dependency
			for _, d := range cands {
				if preferred[d.ID] {
					filtered = append(filtered, d)
				}
			}
			cands = filtered
		}
		if interactive && len(cands) > 0 {
			d, err := wizard.ChooseDependency(ctx, in, cands, a.promptIO())
			if err != nil {
				return nil, err
			}
			if d == nil {
				a.ui.note("Skipped %q.", in)
				continue
			}
			add(d)
			continue
		}
		res.Unknown = append(res.Unknown, in)
		msg := fmt.Sprintf("unknown dependency %q", in)
		if len(cands) > 0 {
			ids := make([]string, 0, 3)
			for i := 0; i < len(cands) && i < 3; i++ {
				ids = append(ids, cands[i].ID)
			}
			msg += "; did you mean " + strings.Join(ids, ", ") + "?"
		} else {
			msg += "; search with 'sprout deps search " + in + "'"
		}
		problems = append(problems, msg)
	}
	if len(problems) > 0 && !interactive {
		return res, &project.ValidationError{Problems: toProblems(problems)}
	}
	return res, nil
}

func toProblems(msgs []string) []project.Problem {
	out := make([]project.Problem, len(msgs))
	for i, m := range msgs {
		out[i] = project.Problem{Field: metadata.FieldDependencies, Message: m}
	}
	return out
}

func depIDs(deps []*metadata.Dependency) []string {
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = d.ID
	}
	return out
}

// splitCommas splits on commas only, so display names such as
// "Spring Data JPA" stay intact.
func splitCommas(inputs []string) []string {
	var out []string
	for _, in := range inputs {
		for part := range strings.SplitSeq(in, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
