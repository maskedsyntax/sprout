package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrAlreadyRepository is returned by GitInit when dir is already in a repo.
var ErrAlreadyRepository = errors.New("already a Git repository")

// GitInit initializes a repository in dir and creates an initial commit
// containing all files. When the commit cannot be created (for example
// because user.name is not configured) the repository is still initialized
// and the commit error is returned wrapped in CommitError.
func GitInit(ctx context.Context, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return ErrAlreadyRepository
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git not found on PATH: %w", err)
	}
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("git %s: %s", args[0], msg)
		}
		return nil
	}
	if err := run("init", "--quiet"); err != nil {
		return err
	}
	if err := run("add", "--all"); err != nil {
		return err
	}
	if err := run("commit", "--quiet", "--no-verify", "-m", "Initial commit from Spring Initializr (via Sprout)"); err != nil {
		return &CommitError{Err: err}
	}
	return nil
}

// CommitError means the repository was initialized but not committed.
type CommitError struct{ Err error }

func (e *CommitError) Error() string { return "initial commit failed: " + e.Err.Error() }
func (e *CommitError) Unwrap() error { return e.Err }
