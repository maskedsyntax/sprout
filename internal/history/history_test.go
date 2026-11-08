package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maskedsyntax/sprout/internal/project"
)

func TestHistory(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "state", FileName)}
	if _, err := s.Last(); err == nil {
		t.Error("Last on empty history should fail")
	}
	for i := range MaxEntries + 5 {
		if err := s.Add(Entry{Time: time.Unix(int64(i), 0), Dir: "/p", Options: project.Options{ArtifactID: "a"}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.List()
	if err != nil || len(entries) != MaxEntries {
		t.Fatalf("entries = %d %v", len(entries), err)
	}
	last, _ := s.Last()
	if last.Time.Unix() != MaxEntries+4 {
		t.Errorf("last = %v", last.Time.Unix())
	}
	os.WriteFile(s.Path, []byte("corrupt"), 0o644)
	if err := s.Add(Entry{Dir: "/q"}); err != nil {
		t.Errorf("Add should recover from a corrupt file: %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := s.List(); len(entries) != 0 {
		t.Error("not cleared")
	}
}
