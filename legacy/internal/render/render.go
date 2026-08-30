// Package render turns the coach's markdown into something readable in a
// terminal.
//
// The chat provider is asked for markdown and answers in markdown; printing it
// raw put literal ** and ### in front of the user. Rendering happens at the
// edge, in cmd/chat, and nowhere else: internal/chat still returns the markdown
// source, so an eval harness or a future non-terminal frontend is unaffected.
//
// Every degraded environment falls back to the raw markdown rather than to
// half-applied escapes. Markdown is readable on its own, which is what makes
// "when in doubt, print the source" the right failure mode here.
package render

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"golang.org/x/term"
)

const (
	// fallbackWidth is used when the output is a terminal whose size cannot be
	// read, which happens under some CI shells and inside tmux popups.
	fallbackWidth = 80

	// maxWidth keeps prose readable on a maximized window. Full-width lines on
	// a 200-column terminal are measurably harder to read than wrapped ones,
	// and the coach's answers are prose, not code.
	maxWidth = 100

	// minWidth is the point below which wrapping does more harm than good.
	minWidth = 40
)

// Renderer converts markdown to styled terminal output. The zero value is not
// usable; call New.
type Renderer struct {
	// tr is nil in plain mode, which is the signal Render and Styled both key
	// off of. There is no separate bool to keep out of sync with it.
	tr    *glamour.TermRenderer
	width int
}

// New builds a Renderer for out. It never returns an error: if styling is
// unavailable for any reason, the result is a plain-mode Renderer that passes
// markdown through untouched.
//
// Styling is disabled when out is not a terminal (so `chesser ... > notes.md`
// captures clean markdown rather than escape codes), when NO_COLOR is set (the
// no-color.org convention), or when TERM says the terminal cannot render it.
func New(out io.Writer) *Renderer {
	width := detectWidth(out)

	if !styleable(out) {
		return &Renderer{width: width}
	}

	// WithAutoStyle picks light or dark from the terminal's background, so the
	// output is legible on both without the user configuring anything.
	tr, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		// A style that fails to build is not worth failing a chat session
		// over. Plain markdown is still perfectly readable.
		return &Renderer{width: width}
	}
	return &Renderer{tr: tr, width: width}
}

// Styled reports whether output is being styled. cmd/chat uses it to decide
// whether the live streaming view (which needs cursor control) is available.
func (r *Renderer) Styled() bool { return r != nil && r.tr != nil }

// Width is the column count Render wraps to, and the width the streaming view
// should wrap to so the repaint is not a reflow.
func (r *Renderer) Width() int {
	if r == nil || r.width == 0 {
		return fallbackWidth
	}
	return r.width
}

// Render returns md as styled terminal output, or md itself in plain mode.
//
// The result never has leading or trailing blank lines: glamour pads its output
// with them, and the caller controls spacing in the REPL.
func (r *Renderer) Render(md string) string {
	if !r.Styled() {
		return strings.TrimSpace(md)
	}
	out, err := r.tr.Render(md)
	if err != nil {
		return strings.TrimSpace(md)
	}
	return strings.Trim(out, "\n")
}

// styleable reports whether out can display ANSI styling.
func styleable(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	switch os.Getenv("TERM") {
	case "dumb", "":
		// An unset TERM is normal for a pipe and abnormal for a terminal; in
		// both cases assuming no styling is the safe read.
		return false
	}
	return isTerminal(out)
}

// detectWidth reads the terminal width, clamped to a readable range.
func detectWidth(out io.Writer) int {
	w := fallbackWidth
	if f, ok := out.(*os.File); ok {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			w = cols
		}
	}
	// Leave a column free: writing into the last cell makes some terminals wrap
	// on their own, which would double-count lines in the streaming view.
	w--
	if w > maxWidth {
		w = maxWidth
	}
	if w < minWidth {
		w = minWidth
	}
	return w
}

func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
