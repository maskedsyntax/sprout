package metadata

import (
	"sort"
	"strings"
	"unicode"
)

// Search returns dependencies matching query (case-insensitive) on ID, name,
// description or category, best matches first. Every whitespace-separated
// term must match.
func (m *Metadata) Search(query string) []*Dependency {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return m.Dependencies()
	}
	type scored struct {
		d     *Dependency
		score int
		order int
	}
	var hits []scored
	for i, d := range m.Dependencies() {
		total := 0
		for _, t := range terms {
			s := matchScore(d, t)
			if s == 0 {
				total = 0
				break
			}
			total += s
		}
		if total > 0 {
			hits = append(hits, scored{d, total, i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].order < hits[j].order
	})
	out := make([]*Dependency, len(hits))
	for i, h := range hits {
		out[i] = h.d
	}
	return out
}

func matchScore(d *Dependency, t string) int {
	id, name := strings.ToLower(d.ID), strings.ToLower(d.Name)
	switch {
	case id == t:
		return 100
	case strings.HasPrefix(id, t):
		return 60
	case strings.Contains(name, t):
		return 50
	case strings.Contains(id, t):
		return 40
	case strings.Contains(strings.ToLower(d.Group), t):
		return 20
	case strings.Contains(strings.ToLower(d.Description), t):
		return 10
	}
	return 0
}

// Normalize lowercases s and drops everything except letters and digits,
// so "data-jpa", "Data JPA" and "datajpa" compare equal.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Match is the result of looking up user input that may not be an exact ID.
type Match struct {
	// Exact is set when the input is a dependency ID (case-insensitive).
	Exact *Dependency
	// Corrected is set when the input matches exactly one dependency once
	// case and separators are ignored, by ID or by name ("datajpa",
	// "Spring Data JPA"). Such corrections are unambiguous.
	Corrected *Dependency
	// Candidates are ranked suggestions when there is no exact match.
	Candidates []*Dependency
}

// Match looks up a dependency, tolerating case, separators and typos.
func (m *Metadata) Match(input string, n int) Match {
	if d, ok := m.Dependency(input); ok {
		return Match{Exact: d}
	}
	norm := Normalize(input)
	if norm == "" {
		return Match{}
	}
	var byID, byName []*Dependency
	for _, d := range m.Dependencies() {
		if Normalize(d.ID) == norm {
			byID = append(byID, d)
		} else if Normalize(d.Name) == norm {
			byName = append(byName, d)
		}
	}
	switch {
	case len(byID) == 1:
		return Match{Corrected: byID[0]}
	case len(byID) == 0 && len(byName) == 1:
		return Match{Corrected: byName[0]}
	}
	return Match{Candidates: m.rank(input, n)}
}

// rank orders dependencies by similarity to input.
func (m *Metadata) rank(input string, n int) []*Dependency {
	norm := Normalize(input)
	type cand struct {
		d     *Dependency
		score int
		order int
	}
	var cands []cand
	limit := max(2, len(norm)/3)
	searchHits := map[string]int{}
	for i, d := range m.Search(input) {
		searchHits[d.ID] = i
	}
	for i, d := range m.Dependencies() {
		id, name := Normalize(d.ID), Normalize(d.Name)
		score := -1
		switch {
		case id == norm || name == norm:
			score = 0
		case strings.HasPrefix(id, norm) || strings.HasPrefix(norm, id) || strings.HasPrefix(name, norm):
			score = 1
		case strings.Contains(id, norm) || strings.Contains(name, norm):
			score = 2
		}
		if dist := levenshtein(norm, id); dist <= limit && (score < 0 || 3+dist < score) {
			score = 3 + dist
		}
		if score < 0 {
			if pos, ok := searchHits[d.ID]; ok {
				score = 10 + pos
			}
		}
		if score >= 0 {
			cands = append(cands, cand{d, score, i})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score < cands[j].score
		}
		return cands[i].order < cands[j].order
	})
	var out []*Dependency
	for i := 0; i < len(cands) && i < n; i++ {
		out = append(out, cands[i].d)
	}
	return out
}

// Suggest returns up to n dependency IDs similar to an unknown ID.
func (m *Metadata) Suggest(id string, n int) []string {
	res := m.Match(id, n)
	var out []string
	switch {
	case res.Exact != nil:
		out = []string{res.Exact.ID}
	case res.Corrected != nil:
		out = []string{res.Corrected.ID}
	}
	for _, d := range res.Candidates {
		out = append(out, d.ID)
	}
	return out
}

// levenshtein returns the optimal-string-alignment distance: insertions,
// deletions, substitutions and adjacent transpositions ("jap" -> "jpa")
// each cost one edit.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
