package render

import (
	"errors"
	"strings"
	"testing"
)

// write feeds text through a StreamWriter one chunk at a time and returns what
// reached the underlying writer. Chunking matters: wrapping must not depend on
// where the provider happened to split its tokens.
func write(width int, indent string, chunks ...string) (string, *StreamWriter) {
	var buf strings.Builder
	s := NewStreamWriter(&buf, width, indent)
	for _, c := range chunks {
		s.WriteString(c)
	}
	s.Flush()
	return buf.String(), s
}

func TestWrapsAtWidth(t *testing.T) {
	got, _ := write(20, "", "the quick brown fox jumps over the lazy dog")
	for i, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 20 {
			t.Errorf("line %d is %d columns, want <= 20: %q", i, len([]rune(line)), line)
		}
	}
	if joined := strings.Join(strings.Fields(got), " "); joined != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("words = %q, want the input words in order", joined)
	}
}

// The same text must lay out identically however the stream chops it up.
func TestChunkingDoesNotAffectLayout(t *testing.T) {
	const text = "You lose most often in rook endgames, especially when down a pawn."
	whole, _ := write(24, "  ", text)

	var byRune []string
	for _, r := range text {
		byRune = append(byRune, string(r))
	}
	perRune, _ := write(24, "  ", byRune...)

	if whole != perRune {
		t.Errorf("layout differs by chunking:\nwhole:\n%s\nper-rune:\n%s", whole, perRune)
	}
}

func TestIndentsEveryLineButNotBlankOnes(t *testing.T) {
	got, _ := write(20, "> ", "alpha bravo charlie delta\n\necho")
	for _, line := range strings.Split(got, "\n") {
		if line == "" {
			continue // a blank line stays blank rather than becoming "> "
		}
		if !strings.HasPrefix(line, "> ") {
			t.Errorf("line %q is missing the indent", line)
		}
	}
	if strings.Contains(got, "> \n") {
		t.Error("a blank line was padded with the indent, leaving trailing whitespace")
	}
}

// A word longer than the line is broken rather than allowed to run past the
// edge, where the terminal would wrap it and desync the erase count.
func TestOverlongWordIsBroken(t *testing.T) {
	long := strings.Repeat("x", 25)
	got, _ := write(10, "", long)
	for _, line := range strings.Split(got, "\n") {
		if len([]rune(line)) > 10 {
			t.Fatalf("line is %d columns, want <= 10: %q", len([]rune(line)), line)
		}
	}
	if joined := strings.ReplaceAll(got, "\n", ""); joined != long {
		t.Errorf("reassembled = %q, want the original word", joined)
	}
}

// Erase must move up exactly as many lines as were drawn. Counting the cursor-up
// escapes is the only way to catch an off-by-one that would eat the prompt above
// the answer or leave a stray line below it.
func TestEraseMatchesLinesDrawn(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"single line", "short"},
		{"wrapped", "the quick brown fox jumps over the lazy dog again and again"},
		{"explicit newlines", "one\ntwo\nthree"},
		{"trailing newline", "one\ntwo\n"},
		{"blank lines", "one\n\n\ntwo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			s := NewStreamWriter(&buf, 20, "  ")
			s.WriteString(tc.text)
			s.Flush()

			drawn := strings.Count(buf.String(), "\n")
			buf.Reset()
			if err := s.Erase(); err != nil {
				t.Fatalf("Erase: %v", err)
			}
			ups := strings.Count(buf.String(), "\x1b[1A")
			if ups != drawn {
				t.Errorf("Erase moved up %d lines, but %d newlines were drawn", ups, drawn)
			}
			if !strings.HasPrefix(buf.String(), "\r\x1b[2K") {
				t.Error("Erase did not clear the current line first")
			}
		})
	}
}

// Erasing before anything is written must be a no-op, or the error path in
// cmd/chat would scroll the prompt away.
func TestEraseWithNothingWrittenEmitsNothing(t *testing.T) {
	var buf strings.Builder
	s := NewStreamWriter(&buf, 40, "  ")
	if err := s.Erase(); err != nil {
		t.Fatalf("Erase: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("Erase wrote %q, want nothing", buf.String())
	}
}

func TestEraseResetsForReuse(t *testing.T) {
	var buf strings.Builder
	s := NewStreamWriter(&buf, 20, "")
	s.WriteString("first\nsecond")
	s.Flush()
	s.Erase()

	buf.Reset()
	s.WriteString("third")
	s.Flush()
	if buf.String() != "third" {
		t.Errorf("after Erase, wrote %q, want %q", buf.String(), "third")
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write(p []byte) (int, error) { return 0, f.err }

// A write failure must be reported once and then stop the writer, rather than
// being swallowed or retried against a broken terminal.
func TestWriteErrorIsSticky(t *testing.T) {
	want := errors.New("broken pipe")
	s := NewStreamWriter(failingWriter{err: want}, 40, "")
	s.WriteString("hello world")
	if err := s.Flush(); !errors.Is(err, want) {
		t.Fatalf("Flush() = %v, want %v", err, want)
	}
	if err := s.Erase(); !errors.Is(err, want) {
		t.Fatalf("Erase() = %v, want %v", err, want)
	}
}

// An indent wider than the terminal must still leave usable columns rather than
// producing a zero or negative width.
func TestNarrowWidthStillUsable(t *testing.T) {
	s := NewStreamWriter(&strings.Builder{}, 4, strings.Repeat(" ", 10))
	if s.width <= 0 {
		t.Fatalf("usable width = %d, want positive", s.width)
	}
}
