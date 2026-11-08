package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ui writes human-oriented output. Styling is applied only when color is
// enabled (TTY, NO_COLOR unset, TERM not "dumb", --no-color not given).
type ui struct {
	out, err io.Writer
	color    bool
	width    int
}

func newUI(out, errw io.Writer, noColor bool) *ui {
	u := &ui{out: out, err: errw, width: 100}
	if f, ok := out.(*os.File); ok {
		if term.IsTerminal(int(f.Fd())) {
			u.color = !noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 20 {
				u.width = w
			}
		}
	}
	return u
}

func (u *ui) style(s string, st lipgloss.Style) string {
	if !u.color {
		return s
	}
	return st.Render(s)
}

// ANSI base colors follow the user's terminal palette, which keeps contrast
// readable on both light and dark themes.
var (
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	styleAccent  = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	styleHeading = lipgloss.NewStyle().Bold(true).Underline(true)
)

func (u *ui) bold(s string) string    { return u.style(s, styleBold) }
func (u *ui) dim(s string) string     { return u.style(s, styleDim) }
func (u *ui) ok(s string) string      { return u.style(s, styleOK) }
func (u *ui) warnc(s string) string   { return u.style(s, styleWarn) }
func (u *ui) errc(s string) string    { return u.style(s, styleErr) }
func (u *ui) accent(s string) string  { return u.style(s, styleAccent) }
func (u *ui) heading(s string) string { return u.style(s, styleHeading) }

func (u *ui) printf(format string, a ...any) { fmt.Fprintf(u.out, format, a...) }
func (u *ui) println(a ...any)               { fmt.Fprintln(u.out, a...) }

// warn prints a warning to stderr.
func (u *ui) warn(format string, a ...any) {
	fmt.Fprintf(u.err, "%s %s\n", u.warnc("warning:"), fmt.Sprintf(format, a...))
}

// note prints an informational message to stderr so stdout stays clean for
// piping.
func (u *ui) note(format string, a ...any) {
	fmt.Fprintf(u.err, "%s\n", u.dim(fmt.Sprintf(format, a...)))
}

// kv prints an aligned key/value line.
func (u *ui) kv(key string, width int, value string) {
	u.printf("  %s %s\n", u.dim(pad(key, width)), value)
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// truncate shortens s to at most n runes, adding an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
