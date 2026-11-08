package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	tests := map[string][]string{
		"hx":                                {"hx"},
		"code --wait":                       {"code", "--wait"},
		`"C:\Program Files\Ed\ed.exe" -n`:   {`C:\Program Files\Ed\ed.exe`, "-n"},
		`C:\tools\nvim.exe`:                 {`C:\tools\nvim.exe`},
		`emacs -nw --eval '(message "hi")'`: {"emacs", "-nw", "--eval", `(message "hi")`},
		`my\ editor arg`:                    {"my editor", "arg"},
		"  vim  ":                           {"vim"},
		`sh -c "echo \"quoted\""`:           {"sh", "-c", `echo "quoted"`},
		"idea; rm -rf /":                    {"idea;", "rm", "-rf", "/"}, // no shell: ';' is just text
	}
	for in, want := range tests {
		got, err := SplitCommand(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", `"unterminated`} {
		if _, err := SplitCommand(bad); err == nil {
			t.Errorf("SplitCommand(%q) succeeded", bad)
		}
	}
}

func TestResolveEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	if got := ResolveEditor("", ""); got != "nano" {
		t.Errorf("env fallback = %q", got)
	}
	if got := ResolveEditor("", "hx"); got != "hx" {
		t.Errorf("config = %q", got)
	}
	if got := ResolveEditor("code", "hx"); got != "code" {
		t.Errorf("explicit = %q", got)
	}
}

func TestDirsHonorOverrides(t *testing.T) {
	t.Setenv("SPROUT_CONFIG_DIR", "/x/config")
	t.Setenv("SPROUT_CACHE_DIR", "/x/cache")
	t.Setenv("SPROUT_STATE_DIR", "/x/state")
	for _, tc := range []struct {
		f    func() (string, error)
		want string
	}{{ConfigDir, "/x/config"}, {CacheDir, "/x/cache"}, {StateDir, "/x/state"}} {
		if got, err := tc.f(); err != nil || got != tc.want {
			t.Errorf("got %q %v want %q", got, err, tc.want)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b.txt")
	if err := WriteFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Errorf("content = %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestGitInit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	if err := GitInit(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := GitInit(context.Background(), dir); !errors.Is(err, ErrAlreadyRepository) {
		t.Errorf("second init = %v", err)
	}
}
