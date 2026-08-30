package render

import (
	"bytes"
	"strings"
	"testing"
)

// A non-terminal writer must never receive styling: this is what keeps
// `chesser ... > notes.md` a clean markdown capture.
func TestNewOnNonTerminalIsPlain(t *testing.T) {
	r := New(&bytes.Buffer{})
	if r.Styled() {
		t.Fatal("Styled() = true for a non-terminal writer")
	}

	const md = "## Sicilian\n\nYou play **1...c5** often."
	got := r.Render(md)
	if got != md {
		t.Errorf("Render() = %q, want the markdown unchanged", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("Render() emitted ANSI escapes in plain mode: %q", got)
	}
}

func TestRenderTrimsSurroundingBlankLines(t *testing.T) {
	r := New(&bytes.Buffer{})
	if got := r.Render("\n\n  hello  \n\n"); got != "hello" {
		t.Errorf("Render() = %q, want %q", got, "hello")
	}
}

func TestWidthIsClamped(t *testing.T) {
	// A non-terminal writer has no size, so the fallback applies. The point of
	// the assertion is that Width never returns something unusable, which the
	// streaming writer divides by.
	got := New(&bytes.Buffer{}).Width()
	if got < minWidth || got > maxWidth {
		t.Errorf("Width() = %d, want it within [%d, %d]", got, minWidth, maxWidth)
	}
}

// A nil Renderer must behave like a plain one rather than panic: cmd/chat holds
// one for the life of the process, and a nil here would crash mid-session.
func TestNilRendererIsSafe(t *testing.T) {
	var r *Renderer
	if r.Styled() {
		t.Error("Styled() = true for a nil Renderer")
	}
	if got := r.Width(); got != fallbackWidth {
		t.Errorf("Width() = %d, want %d", got, fallbackWidth)
	}
	if got := r.Render("hi"); got != "hi" {
		t.Errorf("Render() = %q, want %q", got, "hi")
	}
}
