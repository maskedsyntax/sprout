package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DestState describes the destination before generation.
type DestState int

const (
	DestMissing  DestState = iota // does not exist; will be created
	DestEmpty                     // existing empty directory
	DestNonEmpty                  // existing non-empty directory (requires force)
)

// ErrDestinationExists is returned for a non-empty destination without force.
var ErrDestinationExists = errors.New("destination exists")

// CheckDestination inspects dest and returns an error if generation must
// not proceed.
func CheckDestination(dest string, force bool) (DestState, error) {
	fi, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return DestMissing, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect %s: %w", dest, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("%s is a symbolic link; choose the real directory as --output", dest)
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s exists and is not a directory", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dest, err)
	}
	if len(entries) == 0 {
		return DestEmpty, nil
	}
	if !force {
		return DestNonEmpty, fmt.Errorf("%w: %s is not empty; choose another --output, or use --force to merge into it (files that would be replaced are moved to a backup folder, nothing is deleted)", ErrDestinationExists, dest)
	}
	return DestNonEmpty, nil
}

// Placement reports how files were moved into the destination.
type Placement struct {
	Files     []string `json:"files"`
	Merged    bool     `json:"merged"`
	BackupDir string   `json:"backupDir,omitempty"`
	BackedUp  []string `json:"backedUp,omitempty"`
}

// Place moves the generated tree at src into dest. A missing destination is
// created with a single rename. An existing directory is merged file by file:
// colliding files are first moved into dest/.sprout-backup-<timestamp>/.
// On failure every change is rolled back.
func Place(src, dest string, now time.Time) (*Placement, error) {
	files, err := listFiles(src)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(src, dest); err != nil {
			return nil, fmt.Errorf("move project into place: %w", err)
		}
		return &Placement{Files: files}, nil
	}
	return merge(src, dest, files, now)
}

func listFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symbolic link %s in generated project", p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan generated project: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

type mergeOp struct {
	rel      string
	backedUp bool
}

func merge(src, dest string, files []string, now time.Time) (*Placement, error) {
	conflicts, err := preflight(dest, files)
	if err != nil {
		return nil, err
	}
	backupDir := filepath.Join(dest, ".sprout-backup-"+now.Format("20060102-150405"))
	pl := &Placement{Merged: true}
	if len(conflicts) > 0 {
		if _, err := os.Lstat(backupDir); err == nil {
			return nil, fmt.Errorf("backup directory %s already exists", backupDir)
		}
		pl.BackupDir = backupDir
	}

	var done []mergeOp
	var createdDirs []string
	rollback := func(cause error) error {
		var errs []error
		for i := len(done) - 1; i >= 0; i-- {
			op := done[i]
			target := filepath.Join(dest, op.rel)
			if err := os.Rename(target, filepath.Join(src, op.rel)); err != nil {
				errs = append(errs, err)
			}
			if op.backedUp {
				if err := os.Rename(filepath.Join(backupDir, op.rel), target); err != nil {
					errs = append(errs, err)
				}
			}
		}
		removeEmptyDirs(append(createdDirs, backupDir))
		if len(errs) > 0 {
			return fmt.Errorf("%w (rollback incomplete: %w)", cause, errors.Join(errs...))
		}
		return cause
	}

	for _, rel := range files {
		target := filepath.Join(dest, rel)
		dirs, err := mkdirs(dest, filepath.Dir(rel))
		createdDirs = append(createdDirs, dirs...)
		if err != nil {
			return nil, rollback(err)
		}
		op := mergeOp{rel: rel}
		if conflicts[rel] {
			b := filepath.Join(backupDir, rel)
			bdirs, err := mkdirs(backupDir, filepath.Dir(rel))
			createdDirs = append(createdDirs, bdirs...)
			if err == nil {
				err = os.Rename(target, b)
			}
			if err != nil {
				return nil, rollback(fmt.Errorf("back up %s: %w", rel, err))
			}
			op.backedUp = true
		}
		if err := os.Rename(filepath.Join(src, rel), target); err != nil {
			if op.backedUp {
				_ = os.Rename(filepath.Join(backupDir, rel), target)
			}
			return nil, rollback(fmt.Errorf("place %s: %w", rel, err))
		}
		done = append(done, op)
		pl.Files = append(pl.Files, rel)
		if op.backedUp {
			pl.BackedUp = append(pl.BackedUp, rel)
		}
	}
	return pl, nil
}

// preflight checks that every file can be placed without following a
// symbolic link or replacing a directory, and returns existing files that
// will be backed up.
func preflight(dest string, files []string) (map[string]bool, error) {
	conflicts := map[string]bool{}
	for _, rel := range files {
		parts := strings.Split(rel, string(filepath.Separator))
		cur := dest
		for i, part := range parts {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("inspect %s: %w", cur, err)
			}
			last := i == len(parts)-1
			switch {
			case !last && fi.Mode()&fs.ModeSymlink != 0:
				return nil, fmt.Errorf("refusing to write through symbolic link %s", cur)
			case !last && !fi.IsDir():
				return nil, fmt.Errorf("cannot create %s: %s is a file", rel, cur)
			case last && fi.IsDir():
				return nil, fmt.Errorf("cannot write %s: a directory with that name exists", cur)
			case last:
				conflicts[rel] = true
			}
		}
	}
	return conflicts, nil
}

// mkdirs creates root and root/rel and returns the directories it created.
func mkdirs(root, rel string) ([]string, error) {
	var created []string
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(root, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", root, err)
		}
		created = append(created, root)
	}
	if rel == "." || rel == "" {
		return created, nil
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if _, err := os.Lstat(cur); err == nil {
			continue
		}
		if err := os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return created, fmt.Errorf("create %s: %w", cur, err)
		}
		created = append(created, cur)
	}
	return created, nil
}

func removeEmptyDirs(dirs []string) {
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d) // only succeeds when empty
	}
}
