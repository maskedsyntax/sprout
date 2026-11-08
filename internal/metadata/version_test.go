package metadata

import "testing"

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", s, err)
	}
	return v
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
		kind VersionKind
	}{
		{"4.1.2", "4.1.2", KindGA},
		{"2.7.0.RELEASE", "2.7.0", KindGA},
		{"4.2.0-RC2", "4.2.0-RC2", KindRC},
		{"4.2.0-M1", "4.2.0-M1", KindMilestone},
		{"4.2.0-SNAPSHOT", "4.2.0-SNAPSHOT", KindSnapshot},
		{"2.7.1.BUILD-SNAPSHOT", "2.7.1-BUILD-SNAPSHOT", KindSnapshot},
		{"4.0.x", "4.0.x", KindGA},
		{"4.x.x", "4.x.x", KindGA},
	}
	for _, tt := range tests {
		v := mustVersion(t, tt.in)
		if v.String() != tt.want || v.Kind() != tt.kind {
			t.Errorf("ParseVersion(%q) = %s (%s), want %s (%s)", tt.in, v, v.Kind(), tt.want, tt.kind)
		}
	}
	for _, bad := range []string{"", "4", "4.1", "latest", "4.x.1", "a.b.c"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) succeeded, want error", bad)
		}
	}
}

func TestVersionOrdering(t *testing.T) {
	// Spring Initializr ordering: M < RC < SNAPSHOT < GA for the same number.
	ordered := []string{"4.1.0-M1", "4.1.0-M2", "4.1.0-RC1", "4.1.0-SNAPSHOT", "4.1.0", "4.1.1", "4.2.0-M1"}
	for i := 0; i+1 < len(ordered); i++ {
		a, b := mustVersion(t, ordered[i]), mustVersion(t, ordered[i+1])
		if a.Compare(b) >= 0 || b.Compare(a) <= 0 {
			t.Errorf("expected %s < %s", a, b)
		}
	}
	if mustVersion(t, "2.7.0.RELEASE").Compare(mustVersion(t, "2.7.0")) != 0 {
		t.Error("2.7.0.RELEASE should equal 2.7.0")
	}
}

func TestRange(t *testing.T) {
	tests := []struct {
		rng  string
		v    string
		want bool
	}{
		{"", "1.0.0", true},
		{"4.0.0", "4.0.0", true},
		{"4.0.0", "3.5.9", false},
		{"4.0.0", "5.0.0", true},
		{"[4.0.0,4.1.0-M1)", "4.0.9", true},
		{"[4.0.0,4.1.0-M1)", "4.1.0-M1", false},
		{"[4.0.0,4.1.0-M1)", "4.1.0-SNAPSHOT", false},
		{"[4.0.0,4.1.0-M1)", "4.1.0", false},
		{"[4.1.0,4.2.0-M1)", "4.1.3-SNAPSHOT", true},
		{"(4.0.0,4.1.0]", "4.0.0", false},
		{"(4.0.0,4.1.0]", "4.1.0", true},
		{"[2.7.0.RELEASE,3.0.0.M1)", "2.7.5", true},
	}
	for _, tt := range tests {
		r, err := ParseRange(tt.rng)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tt.rng, err)
		}
		if got := r.Contains(mustVersion(t, tt.v)); got != tt.want {
			t.Errorf("%q contains %s = %v, want %v", tt.rng, tt.v, got, tt.want)
		}
	}
	for _, bad := range []string{"[4.0.0", "[4.0.0)", "[a,b)", "nope"} {
		if _, err := ParseRange(bad); err == nil {
			t.Errorf("ParseRange(%q) succeeded, want error", bad)
		}
	}
	r, _ := ParseRange("[4.0.0,4.1.0-M1)")
	if got := r.String(); got != ">=4.0.0 and <4.1.0-M1" {
		t.Errorf("String() = %q", got)
	}
}
