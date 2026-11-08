package project

import "testing"

func TestDerivePackageName(t *testing.T) {
	tests := map[[2]string]string{
		{"com.example", "demo"}:     "com.example.demo",
		{"com.example", "my-api"}:   "com.example.my_api",
		{"com.example", "My.API-2"}: "com.example.My.API_2",
		{"com.1foo", "class"}:       "com._1foo.class_",
		{"", "app"}:                 "app",
	}
	for in, want := range tests {
		got := DerivePackageName(in[0], in[1])
		if got != want {
			t.Errorf("DerivePackageName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
		if err := ValidatePackageName(got); err != nil {
			t.Errorf("derived package %q is invalid: %v", got, err)
		}
	}
}

func TestValidators(t *testing.T) {
	for _, ok := range []string{"com.example", "io.my-company", "org_x"} {
		if err := ValidateGroupID(ok); err != nil {
			t.Errorf("group %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".com", "com.", "com example", "com/x"} {
		if ValidateGroupID(bad) == nil {
			t.Errorf("group %q accepted", bad)
		}
	}
	for _, bad := range []string{"", "my api", "../x", ".hidden", `a\b`} {
		if ValidateArtifactID(bad) == nil {
			t.Errorf("artifact %q accepted", bad)
		}
	}
	for _, bad := range []string{"com.example.class", "com.1x", "com..x", "com.my-api"} {
		if ValidatePackageName(bad) == nil {
			t.Errorf("package %q accepted", bad)
		}
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList("web,data-jpa", " postgresql  security", ",,")
	if len(got) != 4 || got[0] != "web" || got[3] != "security" {
		t.Errorf("SplitList = %v", got)
	}
}
