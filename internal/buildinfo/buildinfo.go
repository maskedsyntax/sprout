// Package buildinfo exposes version information injected at build time.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// These are set with -ldflags "-X github.com/maskedsyntax/sprout/internal/buildinfo.Version=...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info describes the running binary.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns build information, falling back to module data embedded by
// `go install` when ldflags were not provided.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if info.Version != "dev" {
		return info
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		info.Version = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		}
	}
	return info
}

// UserAgent returns the User-Agent header used for HTTP requests.
func UserAgent() string {
	return fmt.Sprintf("Sprout/%s (+https://github.com/maskedsyntax/sprout; %s/%s)", Get().Version, runtime.GOOS, runtime.GOARCH)
}

// String renders a one-line version description.
func (i Info) String() string {
	s := "sprout " + i.Version
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		s += " (" + c
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return s + fmt.Sprintf(" %s %s/%s", i.Go, i.OS, i.Arch)
}
