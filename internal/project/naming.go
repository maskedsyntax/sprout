package project

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	groupRE    = regexp.MustCompile(`^[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+)*$`)
	artifactRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
)

var javaKeywords = map[string]bool{}

func init() {
	for k := range strings.FieldsSeq(`abstract assert boolean break byte case catch char class const continue
		default do double else enum extends final finally float for goto if implements import instanceof
		int interface long native new package private protected public return short static strictfp super
		switch synchronized this throw throws transient try void volatile while true false null _`) {
		javaKeywords[k] = true
	}
}

// ValidateGroupID checks a Maven group ID.
func ValidateGroupID(s string) error {
	if !groupRE.MatchString(s) {
		return fmt.Errorf("invalid group ID %q: use letters, digits, '-', '_' separated by dots (e.g. com.example)", s)
	}
	return nil
}

// ValidateArtifactID checks a Maven artifact ID.
func ValidateArtifactID(s string) error {
	if !artifactRE.MatchString(s) {
		return fmt.Errorf("invalid artifact ID %q: use letters, digits, '-', '_' or '.' (e.g. my-api)", s)
	}
	return nil
}

// ValidatePackageName checks that s is a valid Java package name.
func ValidatePackageName(s string) error {
	if s == "" {
		return fmt.Errorf("package name must not be empty")
	}
	for seg := range strings.SplitSeq(s, ".") {
		if !isJavaIdentifier(seg) {
			msg := fmt.Sprintf("invalid package name %q", s)
			if fixed := CleanPackageName(s); fixed != s && ValidatePackageName(fixed) == nil {
				msg += fmt.Sprintf(" (did you mean %q?)", fixed)
			}
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// IsJavaIdentifier reports whether s is a valid Java identifier that is not
// a reserved keyword or literal.
func IsJavaIdentifier(s string) bool { return isJavaIdentifier(s) }

func isJavaIdentifier(s string) bool {
	if s == "" || javaKeywords[s] {
		return false
	}
	for i, r := range s {
		if r == '_' || r == '$' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

// DerivePackageName builds a package name from group and artifact IDs the
// way Spring Initializr does (com.example + my-api -> com.example.my_api).
func DerivePackageName(group, artifact string) string {
	return CleanPackageName(strings.Trim(group+"."+artifact, "."))
}

// CleanPackageName converts arbitrary text into a valid Java package name:
// invalid characters become underscores, segments starting with a digit or
// equal to a keyword are prefixed/suffixed with an underscore.
func CleanPackageName(s string) string {
	var segs []string
	for seg := range strings.SplitSeq(strings.TrimSpace(s), ".") {
		var b strings.Builder
		for i, r := range seg {
			switch {
			case r == '_' || r == '$' || unicode.IsLetter(r):
				b.WriteRune(r)
			case unicode.IsDigit(r):
				if i == 0 {
					b.WriteRune('_')
				}
				b.WriteRune(r)
			default:
				b.WriteRune('_')
			}
		}
		out := b.String()
		if out == "" {
			continue
		}
		if javaKeywords[out] {
			out += "_"
		}
		segs = append(segs, out)
	}
	return strings.Join(segs, ".")
}
