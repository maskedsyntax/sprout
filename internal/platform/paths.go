// Package platform contains operating-system specific helpers: standard
// directories, atomic writes, terminal detection and process launching.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const appName = "sprout"

// ConfigDir returns the directory for configuration files:
// $SPROUT_CONFIG_DIR, else the OS convention ($XDG_CONFIG_HOME or ~/.config
// on Linux, ~/Library/Application Support on macOS, %AppData% on Windows).
func ConfigDir() (string, error) {
	if d := os.Getenv("SPROUT_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// CacheDir returns the directory for cached data: $SPROUT_CACHE_DIR, else
// the OS convention ($XDG_CACHE_HOME or ~/.cache on Linux,
// ~/Library/Caches on macOS, %LocalAppData% on Windows).
func CacheDir() (string, error) {
	if d := os.Getenv("SPROUT_CACHE_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(base, appName), nil
}

// StateDir returns the directory for history and other state:
// $SPROUT_STATE_DIR, else $XDG_STATE_HOME/sprout (default ~/.local/state)
// on Linux and BSDs, and the config directory elsewhere.
func StateDir() (string, error) {
	if d := os.Getenv("SPROUT_STATE_DIR"); d != "" {
		return d, nil
	}
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9", "android":
		return ConfigDir()
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", appName), nil
}

// ExpandHome expands a leading "~" to the user's home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: %w", p, err)
	}
	return filepath.Join(home, p[1:]), nil
}

// WriteFileAtomic writes data to a temporary file in the same directory and
// renames it into place, creating parent directories as needed.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(e error) error {
		f.Close()
		os.Remove(tmp)
		return e
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil && runtime.GOOS != "windows" {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ShortPath replaces the home directory prefix with "~" for display.
func ShortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
