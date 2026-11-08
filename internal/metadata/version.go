package metadata

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a Spring Boot version as understood by Spring Initializr.
//
// Both the legacy format (2.7.0.RELEASE, 2.7.1.BUILD-SNAPSHOT) and the
// current format (4.1.2, 4.2.0-RC2, 4.2.0-SNAPSHOT) are supported. Minor and
// patch may be "x" when used as a version line wildcard (4.0.x).
type Version struct {
	Major, Minor, Patch int
	MinorX, PatchX      bool
	Qualifier           string // M, RC, SNAPSHOT, BUILD-SNAPSHOT, RELEASE or other
	QualifierNum        int
}

// VersionKind classifies a version.
type VersionKind string

const (
	KindGA        VersionKind = "GA"
	KindRC        VersionKind = "RC"
	KindMilestone VersionKind = "M"
	KindSnapshot  VersionKind = "SNAPSHOT"
	KindOther     VersionKind = "OTHER"
)

var versionRE = regexp.MustCompile(`^(\d+)\.(\d+|x)\.(\d+|x)(?:[.-]([^0-9]+)(\d+)?)?$`)

// ParseVersion parses a Spring Boot version string.
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	if m[2] == "x" {
		v.MinorX, v.PatchX = true, true
	} else {
		v.Minor, _ = strconv.Atoi(m[2])
	}
	switch {
	case m[3] == "x":
		v.PatchX = true
	case v.MinorX:
		return Version{}, fmt.Errorf("invalid version %q: patch must be x when minor is x", s)
	default:
		v.Patch, _ = strconv.Atoi(m[3])
	}
	v.Qualifier = strings.ToUpper(strings.TrimSuffix(m[4], "-"))
	if m[5] != "" {
		v.QualifierNum, _ = strconv.Atoi(m[5])
	}
	if v.Qualifier == "RELEASE" {
		v.Qualifier = ""
	}
	return v, nil
}

// IsWildcard reports whether the version contains an "x" placeholder.
func (v Version) IsWildcard() bool { return v.MinorX || v.PatchX }

// Kind returns the release kind of the version.
func (v Version) Kind() VersionKind {
	switch v.Qualifier {
	case "":
		return KindGA
	case "RC":
		return KindRC
	case "M":
		return KindMilestone
	case "SNAPSHOT", "BUILD-SNAPSHOT":
		return KindSnapshot
	default:
		return KindOther
	}
}

// Stable reports whether the version is a general-availability release.
func (v Version) Stable() bool { return v.Kind() == KindGA }

// qualifierRank follows Spring Initializr's ordering: M < RC < SNAPSHOT < RELEASE.
func qualifierRank(q string) int {
	switch q {
	case "M":
		return 1
	case "RC":
		return 2
	case "BUILD-SNAPSHOT", "SNAPSHOT":
		return 3
	case "":
		return 4
	default:
		return 0
	}
}

// Compare returns -1, 0 or 1. Wildcards compare as zero.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if c := cmpInt(p[0], p[1]); c != 0 {
			return c
		}
	}
	if c := cmpInt(qualifierRank(v.Qualifier), qualifierRank(o.Qualifier)); c != 0 {
		return c
	}
	if c := strings.Compare(v.Qualifier, o.Qualifier); c != 0 {
		return c
	}
	return cmpInt(v.QualifierNum, o.QualifierNum)
}

// Matches reports whether a concrete version satisfies v, which may be a wildcard.
func (v Version) Matches(concrete Version) bool {
	if v.Major != concrete.Major {
		return false
	}
	if !v.MinorX && v.Minor != concrete.Minor {
		return false
	}
	if v.IsWildcard() {
		return true
	}
	return v.Compare(concrete) == 0
}

func (v Version) String() string {
	minor, patch := strconv.Itoa(v.Minor), strconv.Itoa(v.Patch)
	if v.MinorX {
		minor = "x"
	}
	if v.PatchX {
		patch = "x"
	}
	s := fmt.Sprintf("%d.%s.%s", v.Major, minor, patch)
	if v.Qualifier != "" {
		s += "-" + v.Qualifier
		if v.QualifierNum > 0 {
			s += strconv.Itoa(v.QualifierNum)
		}
	}
	return s
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Range is a Spring Initializr version range such as "4.0.0" (at least
// 4.0.0) or "[4.0.0,4.1.0-M1)".
type Range struct {
	Lower, Upper                   *Version
	LowerInclusive, UpperInclusive bool
	raw                            string
}

// ParseRange parses a version range. An empty string matches every version.
func ParseRange(s string) (Range, error) {
	s = strings.TrimSpace(s)
	r := Range{raw: s}
	if s == "" {
		return r, nil
	}
	if s[0] != '[' && s[0] != '(' {
		v, err := ParseVersion(s)
		if err != nil {
			return Range{}, fmt.Errorf("invalid version range %q: %w", s, err)
		}
		r.Lower, r.LowerInclusive = &v, true
		return r, nil
	}
	last := s[len(s)-1]
	if last != ']' && last != ')' {
		return Range{}, fmt.Errorf("invalid version range %q: missing closing bracket", s)
	}
	parts := strings.Split(s[1:len(s)-1], ",")
	if len(parts) != 2 {
		return Range{}, fmt.Errorf("invalid version range %q: expected two bounds", s)
	}
	r.LowerInclusive, r.UpperInclusive = s[0] == '[', last == ']'
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := ParseVersion(p)
		if err != nil {
			return Range{}, fmt.Errorf("invalid version range %q: %w", s, err)
		}
		if i == 0 {
			r.Lower = &v
		} else {
			r.Upper = &v
		}
	}
	return r, nil
}

// Contains reports whether v is within the range.
func (r Range) Contains(v Version) bool {
	if r.Lower != nil {
		c := v.Compare(*r.Lower)
		if c < 0 || (c == 0 && !r.LowerInclusive) {
			return false
		}
	}
	if r.Upper != nil {
		c := v.Compare(*r.Upper)
		if c > 0 || (c == 0 && !r.UpperInclusive) {
			return false
		}
	}
	return true
}

// IsZero reports whether the range is unrestricted.
func (r Range) IsZero() bool { return r.Lower == nil && r.Upper == nil }

// Raw returns the range as written by the server.
func (r Range) Raw() string { return r.raw }

// String renders the range in a human-readable form.
func (r Range) String() string {
	var parts []string
	if r.Lower != nil {
		op := ">"
		if r.LowerInclusive {
			op = ">="
		}
		parts = append(parts, op+r.Lower.String())
	}
	if r.Upper != nil {
		op := "<"
		if r.UpperInclusive {
			op = "<="
		}
		parts = append(parts, op+r.Upper.String())
	}
	if len(parts) == 0 {
		return "any"
	}
	return strings.Join(parts, " and ")
}
