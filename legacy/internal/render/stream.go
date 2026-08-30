package render

import (
	"io"
	"unicode"
)

// minUsableWidth is the narrowest line StreamWriter will wrap to, so an indent
// wider than the terminal cannot produce a zero or negative width.
const minUsableWidth = 8

// StreamWriter shows model output as it arrives and can then erase it, so the
// finished answer can be repainted as rendered markdown in its place.
//
// It does its own soft-wrapping rather than letting the terminal wrap, because
// erasing requires knowing exactly how many physical lines were drawn. A line
// the terminal wrapped on its own is a line this type cannot account for.
//
// Column counting is by rune, not by display cell. Double-width glyphs would
// wrap a column early in the live view; the repaint that follows is measured by
// glamour and unaffected, which makes this the cheap side of the tradeoff.
type StreamWriter struct {
	w      io.Writer
	width  int    // usable columns, excluding the indent
	indent string // prefix drawn at the start of every physical line

	word    []rune // the word being accumulated, not yet placed
	col     int    // columns used on the current physical line
	lines   int    // physical lines below the first one, i.e. newlines emitted
	started bool   // whether anything at all has been written
	err     error  // first write error; all later calls become no-ops
}

// NewStreamWriter returns a writer that wraps to width columns, indenting each
// line. The indent is counted against width, matching how glamour lays out its
// own output, so the repaint lands in roughly the same shape.
func NewStreamWriter(w io.Writer, width int, indent string) *StreamWriter {
	usable := width - len([]rune(indent))
	if usable < minUsableWidth {
		// Only a floor against a nonsensical width, not a preference: an
		// indent wider than the terminal must still leave columns to write in.
		usable = minUsableWidth
	}
	return &StreamWriter{w: w, width: usable, indent: indent}
}

// WriteString appends a delta. It is the method the streaming callback uses;
// Write exists so StreamWriter satisfies io.Writer.
func (s *StreamWriter) WriteString(text string) error {
	for _, r := range text {
		switch {
		case r == '\n':
			s.flushWord()
			s.newline()
		case r == '\r':
			// Providers occasionally emit CRLF. The LF does the work.
		case unicode.IsSpace(r):
			s.flushWord()
			// A space that would push past the edge becomes the wrap itself,
			// and a space at the start of a fresh line is dropped rather than
			// indenting the text by one.
			if s.col > 0 && s.col+1 <= s.width {
				s.emit(" ")
				s.col++
			}
		default:
			s.word = append(s.word, r)
		}
	}
	return s.err
}

func (s *StreamWriter) Write(p []byte) (int, error) {
	if err := s.WriteString(string(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush places any word still being accumulated. Call it once the stream ends,
// before reading Lines or erasing.
func (s *StreamWriter) Flush() error {
	s.flushWord()
	return s.err
}

// Erase removes everything written so far and returns the cursor to where it
// started, leaving the writer ready for reuse.
func (s *StreamWriter) Erase() error {
	if !s.started {
		return s.err
	}
	// Clear the current line, then walk up clearing each line above it.
	s.emit("\r\x1b[2K")
	for i := 0; i < s.lines; i++ {
		s.emit("\x1b[1A\x1b[2K")
	}
	s.word = s.word[:0]
	s.col, s.lines, s.started = 0, 0, false
	return s.err
}

// flushWord places the accumulated word, wrapping first if it does not fit.
func (s *StreamWriter) flushWord() {
	if len(s.word) == 0 {
		return
	}
	word := s.word
	s.word = s.word[:0]

	if s.col+len(word) > s.width && s.col > 0 {
		s.newline()
	}
	// A word longer than the whole line is broken across lines rather than
	// forcing a line the terminal would wrap behind our back. Long URLs and
	// unbroken PGN strings both hit this.
	for len(word) > s.width {
		s.emitIndentIfNeeded()
		s.emit(string(word[:s.width-s.col]))
		word = word[s.width-s.col:]
		s.newline()
	}
	s.emitIndentIfNeeded()
	s.emit(string(word))
	s.col += len(word)
}

func (s *StreamWriter) newline() {
	// Deliberately no indent: a blank line should be blank, not two spaces.
	s.emit("\n")
	s.col = 0
	s.lines++
}

// emitIndentIfNeeded writes the indent exactly once per physical line.
func (s *StreamWriter) emitIndentIfNeeded() {
	if s.col == 0 && s.indent != "" {
		s.emit(s.indent)
	}
}

func (s *StreamWriter) emit(str string) {
	if s.err != nil {
		return
	}
	s.started = true
	if _, err := io.WriteString(s.w, str); err != nil {
		s.err = err
	}
}

// Indent is the left margin glamour's built-in styles use for body text.
// Matching it keeps the streamed text and the repainted text in the same
// column.
const Indent = "  "

var _ io.Writer = (*StreamWriter)(nil)
