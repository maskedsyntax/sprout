package metadata

import (
	"sort"
	"strings"
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

// Suggest returns up to n dependency IDs similar to an unknown ID.
func (m *Metadata) Suggest(id string, n int) []string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil
	}
	type cand struct {
		id   string
		dist int
	}
	var cands []cand
	limit := max(2, len(id)/3)
	for _, d := range m.Dependencies() {
		did := strings.ToLower(d.ID)
		dist := levenshtein(id, did)
		if strings.Contains(did, id) || strings.Contains(id, did) || strings.Contains(strings.ToLower(d.Name), id) {
			dist = min(dist, 1)
		}
		if dist <= limit {
			cands = append(cands, cand{d.ID, dist})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].dist < cands[j].dist })
	var out []string
	for i := 0; i < len(cands) && i < n; i++ {
		out = append(out, cands[i].id)
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
