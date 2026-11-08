package generator

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Limits bound archive extraction to protect against zip bombs.
type Limits struct {
	MaxFiles int
	MaxBytes int64
}

// DefaultLimits are generous for any real generated project.
var DefaultLimits = Limits{MaxFiles: 20000, MaxBytes: 512 << 20}

// UnsafeEntryError reports an archive entry that was rejected.
type UnsafeEntryError struct {
	Name   string
	Reason string
}

func (e *UnsafeEntryError) Error() string {
	return fmt.Sprintf("refusing to extract archive entry %q: %s", e.Name, e.Reason)
}

// ExtractZip extracts the archive at zipPath into dir, which must be an
// empty directory owned by the caller. Entries that are absolute, contain
// "..", use backslashes, or are symbolic links or special files are rejected
// (ZIP Slip protection). Files are created exclusively, so duplicate entries
// fail rather than overwrite. Executable bits are preserved.
func ExtractZip(zipPath, dir string, lim Limits) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer r.Close()
	if lim.MaxFiles > 0 && len(r.File) > lim.MaxFiles {
		return nil, fmt.Errorf("archive has %d entries, more than the limit of %d", len(r.File), lim.MaxFiles)
	}
	budget := lim.MaxBytes
	var files []string
	for _, f := range r.File {
		rel, err := entryPath(f.Name)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dir, rel)
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			return nil, &UnsafeEntryError{f.Name, "symbolic links are not allowed"}
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("create directory %s: %w", rel, err)
			}
		case !mode.IsRegular():
			return nil, &UnsafeEntryError{f.Name, "special files are not allowed"}
		default:
			n, err := extractFile(f, target, budget)
			if err != nil {
				return nil, fmt.Errorf("extract %s: %w", rel, err)
			}
			budget -= n
			files = append(files, rel)
		}
	}
	return files, nil
}

// entryPath validates an archive entry name and converts it to a local
// relative path. It returns "" for the root directory entry.
func entryPath(name string) (string, error) {
	if strings.Contains(name, `\`) {
		return "", &UnsafeEntryError{name, "backslashes are not allowed in entry names"}
	}
	if strings.ContainsRune(name, ':') {
		// Drive letters and NTFS alternate data streams; rejected on every
		// OS so extraction behaves identically everywhere.
		return "", &UnsafeEntryError{name, "colons are not allowed in entry names"}
	}
	if strings.ContainsRune(name, 0) {
		return "", &UnsafeEntryError{name, "NUL byte in entry name"}
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", &UnsafeEntryError{name, "absolute paths are not allowed"}
	}
	for _, seg := range strings.Split(trimmed, "/") {
		if seg == ".." {
			return "", &UnsafeEntryError{name, "path escapes the output directory"}
		}
	}
	rel := filepath.FromSlash(trimmed)
	if !filepath.IsLocal(rel) {
		return "", &UnsafeEntryError{name, "path is not local to the output directory"}
	}
	return rel, nil
}

func extractFile(f *zip.File, target string, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	perm := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		perm = 0o755
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return 0, fmt.Errorf("duplicate archive entry")
		}
		return 0, err
	}
	var src io.Reader = rc
	if budget > 0 {
		src = io.LimitReader(rc, budget+1)
	}
	n, err := io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if budget > 0 && n > budget {
		return n, fmt.Errorf("archive exceeds the uncompressed size limit")
	}
	// Apply the mode explicitly: OpenFile's perm is filtered by umask.
	if err := os.Chmod(target, perm); err != nil {
		return n, err
	}
	return n, nil
}
