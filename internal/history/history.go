// Package history records successfully generated projects so the last
// configuration can be reused and past projects listed.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/maskedsyntax/sprout/internal/platform"
	"github.com/maskedsyntax/sprout/internal/project"
)

// FileName is the history file inside the state directory.
const FileName = "history.json"

// MaxEntries bounds the history length.
const MaxEntries = 50

// Entry is one generated project.
type Entry struct {
	Time    time.Time       `json:"time"`
	Dir     string          `json:"dir"`
	Server  string          `json:"server"`
	Preset  string          `json:"preset,omitempty"`
	Options project.Options `json:"options"`
}

// Store is the history file.
type Store struct {
	Path string
}

// DefaultStore returns the store in the standard state directory.
func DefaultStore() (Store, error) {
	dir, err := platform.StateDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Path: filepath.Join(dir, FileName)}, nil
}

// List returns entries, newest first.
func (s Store) List() ([]Entry, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse history %s: %w", s.Path, err)
	}
	return entries, nil
}

// Last returns the most recent entry.
func (s Store) Last() (Entry, error) {
	entries, err := s.List()
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, errors.New("no previous project found; generate one with 'sprout new' first")
	}
	return entries[0], nil
}

// Add prepends an entry, trimming old ones.
func (s Store) Add(e Entry) error {
	entries, err := s.List()
	if err != nil {
		// A corrupt history file must not block generation; start over.
		entries = nil
	}
	entries = append([]Entry{e}, entries...)
	if len(entries) > MaxEntries {
		entries = entries[:MaxEntries]
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteFileAtomic(s.Path, data, 0o644)
}

// Clear deletes the history file.
func (s Store) Clear() error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
