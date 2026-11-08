package generator

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/maskedsyntax/sprout/internal/testutil"
)

func writeZip(t *testing.T, build func(zw *zip.Writer)) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	build(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func addFile(t *testing.T, zw *zip.Writer, name string, mode os.FileMode, body string) {
	t.Helper()
	h := &zip.FileHeader{Name: name}
	h.SetMode(mode)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(body))
}

func TestExtractZip(t *testing.T) {
	data := testutil.BuildZip(map[string]string{"pom.xml": "x", "src/main/A.java": "a", "mvnw": "#!/bin/sh"}, map[string]bool{"mvnw": true})
	p := filepath.Join(t.TempDir(), "p.zip")
	os.WriteFile(p, data, 0o644)
	dir := t.TempDir()
	files, err := ExtractZip(p, dir, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Errorf("files = %v", files)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "src", "main", "A.java")); string(b) != "a" {
		t.Errorf("content = %q", b)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "mvnw"))
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("mvnw not executable: %v", fi.Mode())
		}
		fi, _ = os.Stat(filepath.Join(dir, "pom.xml"))
		if fi.Mode().Perm()&0o111 != 0 {
			t.Errorf("pom.xml executable: %v", fi.Mode())
		}
	}
}

func TestExtractZipRejectsUnsafeEntries(t *testing.T) {
	cases := map[string]func(zw *zip.Writer){
		"parent traversal": func(zw *zip.Writer) { addFile(t, zw, "../evil.txt", 0o644, "x") },
		"nested traversal": func(zw *zip.Writer) { addFile(t, zw, "a/../../evil.txt", 0o644, "x") },
		"absolute":         func(zw *zip.Writer) { addFile(t, zw, "/etc/evil", 0o644, "x") },
		"backslash":        func(zw *zip.Writer) { addFile(t, zw, `..\evil.txt`, 0o644, "x") },
		"drive letter":     func(zw *zip.Writer) { addFile(t, zw, "C:/evil.txt", 0o644, "x") },
		"symlink":          func(zw *zip.Writer) { addFile(t, zw, "link", os.ModeSymlink|0o777, "/etc/passwd") },
		"device":           func(zw *zip.Writer) { addFile(t, zw, "dev", os.ModeDevice|0o644, "") },
		"duplicate": func(zw *zip.Writer) {
			addFile(t, zw, "a.txt", 0o644, "1")
			addFile(t, zw, "a.txt", 0o644, "2")
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeZip(t, build)
			parent := t.TempDir()
			dir := filepath.Join(parent, "out")
			os.Mkdir(dir, 0o755)
			_, err := ExtractZip(p, dir, DefaultLimits)
			if err == nil {
				t.Fatal("expected error")
			}
			if _, err := os.Stat(filepath.Join(parent, "evil.txt")); err == nil {
				t.Fatal("file escaped the output directory")
			}
			if name != "duplicate" {
				var ue *UnsafeEntryError
				if !errors.As(err, &ue) {
					t.Errorf("want UnsafeEntryError, got %v", err)
				}
			}
		})
	}
}

func TestExtractZipLimits(t *testing.T) {
	p := writeZip(t, func(zw *zip.Writer) {
		addFile(t, zw, "big", 0o644, strings.Repeat("x", 4096))
	})
	_, err := ExtractZip(p, t.TempDir(), Limits{MaxFiles: 10, MaxBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Errorf("expected size limit error, got %v", err)
	}
	p = writeZip(t, func(zw *zip.Writer) {
		for _, n := range []string{"a", "b", "c"} {
			addFile(t, zw, n, 0o644, "")
		}
	})
	_, err = ExtractZip(p, t.TempDir(), Limits{MaxFiles: 2, MaxBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("expected entry limit error, got %v", err)
	}
}
