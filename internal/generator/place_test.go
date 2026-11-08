package generator

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func tree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestCheckDestination(t *testing.T) {
	base := t.TempDir()
	if st, err := CheckDestination(filepath.Join(base, "new"), false); err != nil || st != DestMissing {
		t.Errorf("missing: %v %v", st, err)
	}
	empty := filepath.Join(base, "empty")
	os.Mkdir(empty, 0o755)
	if st, err := CheckDestination(empty, false); err != nil || st != DestEmpty {
		t.Errorf("empty: %v %v", st, err)
	}
	full := filepath.Join(base, "full")
	tree(t, full, map[string]string{"x": "1"})
	if _, err := CheckDestination(full, false); !errors.Is(err, ErrDestinationExists) {
		t.Errorf("non-empty without force: %v", err)
	}
	if st, err := CheckDestination(full, true); err != nil || st != DestNonEmpty {
		t.Errorf("non-empty with force: %v %v", st, err)
	}
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := CheckDestination(file, true); err == nil {
		t.Error("file destination accepted")
	}
}

func TestPlaceNewDirectory(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "stage")
	tree(t, src, map[string]string{"a.txt": "a", "dir/b.txt": "b"})
	dest := filepath.Join(base, "proj")
	pl, err := Place(src, dest, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Merged || len(pl.Files) != 2 || read(t, filepath.Join(dest, "dir", "b.txt")) != "b" {
		t.Errorf("placement = %+v", pl)
	}
}

func TestPlaceMergeBacksUpConflicts(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "stage")
	tree(t, src, map[string]string{".gitignore": "new", "src/App.java": "app", "README.md": "generated"})
	dest := filepath.Join(base, "proj")
	tree(t, dest, map[string]string{".gitignore": "mine", "notes.txt": "keep", "README.md": "my readme"})

	pl, err := Place(src, dest, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dest, ".sprout-backup-20260102-030405")
	if pl.BackupDir != backup || len(pl.BackedUp) != 2 {
		t.Errorf("placement = %+v", pl)
	}
	if read(t, filepath.Join(dest, ".gitignore")) != "new" || read(t, filepath.Join(backup, ".gitignore")) != "mine" {
		t.Error("conflict not backed up correctly")
	}
	if read(t, filepath.Join(backup, "README.md")) != "my readme" {
		t.Error("README not backed up")
	}
	if read(t, filepath.Join(dest, "notes.txt")) != "keep" {
		t.Error("unrelated file changed")
	}
}

func TestPlaceMergeRefusesUnsafeLayouts(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "stage")
	tree(t, src, map[string]string{"src/App.java": "app", "a.txt": "a"})

	// A directory where a file must go.
	dest := filepath.Join(base, "d1")
	os.MkdirAll(filepath.Join(dest, "a.txt"), 0o755)
	if _, err := Place(src, dest, fixedNow); err == nil {
		t.Error("expected directory conflict error")
	}
	// A file where a directory must go.
	dest = filepath.Join(base, "d2")
	tree(t, dest, map[string]string{"src": "file"})
	if _, err := Place(src, dest, fixedNow); err == nil {
		t.Error("expected file conflict error")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// A symlinked directory must never be written through.
	outside := filepath.Join(base, "outside")
	os.Mkdir(outside, 0o755)
	dest = filepath.Join(base, "d3")
	os.Mkdir(dest, 0o755)
	if err := os.Symlink(outside, filepath.Join(dest, "src")); err != nil {
		t.Skip(err)
	}
	_, err := Place(src, dest, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("expected symlink refusal, got %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Error("wrote through symlink")
	}
	// Nothing was placed in any failed destination.
	if _, err := os.Stat(filepath.Join(dest, "a.txt")); err == nil {
		t.Error("partial placement left behind")
	}
}

func TestPlaceMergeRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	base := t.TempDir()
	src := filepath.Join(base, "stage")
	tree(t, src, map[string]string{"a.txt": "new-a", "z/locked.txt": "z"})
	dest := filepath.Join(base, "proj")
	tree(t, dest, map[string]string{"a.txt": "old-a"})
	// Make "z" creation fail after a.txt was already placed.
	os.Mkdir(filepath.Join(dest, "z"), 0o555)
	defer os.Chmod(filepath.Join(dest, "z"), 0o755)

	if _, err := Place(src, dest, fixedNow); err == nil {
		t.Fatal("expected failure")
	}
	if read(t, filepath.Join(dest, "a.txt")) != "old-a" {
		t.Error("a.txt not restored")
	}
	if _, err := os.Stat(filepath.Join(dest, ".sprout-backup-20260102-030405")); err == nil {
		t.Error("empty backup dir left behind")
	}
}
