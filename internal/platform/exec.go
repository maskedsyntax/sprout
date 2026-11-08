package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"golang.org/x/term"
)

// IsTerminal reports whether f is an interactive terminal.
func IsTerminal(f *os.File) bool {
	return f != nil && term.IsTerminal(int(f.Fd()))
}

// SplitCommand splits a command line such as `code --wait` or
// `"C:\Program Files\Editor\editor.exe" -n` into arguments without invoking
// a shell. Single and double quotes group words; a backslash escapes the
// next character only inside double quotes or before a quote/space, so
// Windows paths keep their separators.
func SplitCommand(s string) ([]string, error) {
	var (
		args    []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		runes   = []rune(s)
		flushed = func() {
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		}
	)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			if r == '\\' && quote == '"' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				r = runes[i]
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\' && i+1 < len(runes) && (runes[i+1] == ' ' || runes[i+1] == '"' || runes[i+1] == '\''):
			i++
			cur.WriteRune(runes[i])
			inWord = true
		case unicode.IsSpace(r):
			flushed()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command %q", s)
	}
	flushed()
	if len(args) == 0 {
		return nil, errors.New("empty command")
	}
	return args, nil
}

// ResolveEditor returns the editor command line from, in order: explicit,
// configured, $VISUAL, $EDITOR. It returns "" when none is set.
func ResolveEditor(explicit, configured string) string {
	for _, c := range []string{explicit, configured, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

// Stdio bundles the streams given to child processes.
type Stdio struct {
	In       io.Reader
	Out, Err io.Writer
}

// RunEditor launches an editor command with target appended as the last
// argument. No shell is involved, so paths and names cannot inject commands.
func RunEditor(ctx context.Context, editor, target string, stdio Stdio) error {
	args, err := SplitCommand(editor)
	if err != nil {
		return fmt.Errorf("editor: %w", err)
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("editor %q not found: %w", args[0], err)
	}
	cmd := exec.CommandContext(ctx, bin, append(args[1:], target)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.In, stdio.Out, stdio.Err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run editor %q: %w", args[0], err)
	}
	return nil
}
