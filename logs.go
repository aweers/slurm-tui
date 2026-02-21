package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const (
	initialTailBytes = 1024 * 1024
	renderLineLimit  = 20000
)

type streamLabel string

const (
	streamOut streamLabel = "OUT"
	streamErr streamLabel = "ERR"
)

type streamChunk struct {
	Label          streamLabel
	NewLines       []string
	CurrentLine    string
	CurrentChanged bool
	Missing        bool
}

type tailRenderer struct {
	history      []string
	active       []lineBuffer
	cursorLine   int
	cursorCol    int
	limit        int
	activeWindow int
	pendingUTF8  []byte
	pendingCSI   []byte
}

func newTailRenderer(limit int) tailRenderer {
	r := tailRenderer{
		history:      make([]string, 0, 256),
		active:       make([]lineBuffer, 1, 64),
		limit:        limit,
		activeWindow: 256,
		pendingUTF8:  make([]byte, 0, 8),
		pendingCSI:   make([]byte, 0, 32),
	}
	return r
}

func (r *tailRenderer) reset() {
	r.history = r.history[:0]
	r.active = r.active[:1]
	r.active[0].runes = r.active[0].runes[:0]
	r.cursorLine = 0
	r.cursorCol = 0
	r.pendingUTF8 = r.pendingUTF8[:0]
	r.pendingCSI = r.pendingCSI[:0]
}

func (r *tailRenderer) ingest(data []byte) (newLines []string, currentChanged bool) {
	for _, b := range data {
		if len(r.pendingCSI) > 0 {
			if len(r.pendingCSI) == 1 {
				if b != '[' {
					r.pendingCSI = r.pendingCSI[:0]
					continue
				}
				r.pendingCSI = append(r.pendingCSI, b)
				continue
			}
			r.pendingCSI = append(r.pendingCSI, b)
			if csiDone(b) {
				if r.applyCSI(string(r.pendingCSI)) {
					currentChanged = true
				}
				r.pendingCSI = r.pendingCSI[:0]
			}
			continue
		}

		switch b {
		case 0x1b:
			r.flushPendingUTF8(&currentChanged)
			r.pendingCSI = append(r.pendingCSI[:0], b)
		case '\r':
			r.flushPendingUTF8(&currentChanged)
			r.cursorCol = 0
			currentChanged = true
		case '\n':
			r.flushPendingUTF8(&currentChanged)
			line := r.active[r.cursorLine].String()
			newLines = append(newLines, line)
			r.advanceLine()
			currentChanged = true
		default:
			r.pendingUTF8 = append(r.pendingUTF8, b)
			r.flushPendingUTF8(&currentChanged)
		}
	}
	return newLines, currentChanged
}

func (r *tailRenderer) content() string {
	return strings.Join(r.logicalLines(), "\n")
}

func (r *tailRenderer) contentWrapped(width int) string {
	lines := r.logicalLines()
	if width <= 0 {
		return strings.Join(lines, "\n")
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapRunes(line, width)...)
	}
	return strings.Join(wrapped, "\n")
}

func (r *tailRenderer) currentLine() string {
	if len(r.active) == 0 {
		return ""
	}
	return r.active[r.cursorLine].String()
}

func (r *tailRenderer) flushPendingUTF8(changed *bool) {
	for len(r.pendingUTF8) > 0 {
		if !utf8.FullRune(r.pendingUTF8) {
			return
		}
		ru, size := utf8.DecodeRune(r.pendingUTF8)
		if ru == utf8.RuneError && size == 1 {
			ru = rune(r.pendingUTF8[0])
		}
		r.writeRune(ru)
		*changed = true
		r.pendingUTF8 = r.pendingUTF8[size:]
	}
}

func (r *tailRenderer) writeRune(ru rune) {
	line := &r.active[r.cursorLine]
	line.WriteAt(r.cursorCol, ru)
	r.cursorCol += runewidth.RuneWidth(ru)
	if r.cursorCol < 0 {
		r.cursorCol = 0
	}
}

func (r *tailRenderer) advanceLine() {
	if r.cursorLine == len(r.active)-1 {
		r.active = append(r.active, lineBuffer{})
	}
	r.cursorLine++
	r.cursorCol = 0
	r.compactActive()
}

func (r *tailRenderer) moveCursorUp(n int) {
	if n <= 0 {
		n = 1
	}
	r.cursorLine -= n
	if r.cursorLine < 0 {
		r.cursorLine = 0
	}
}

func (r *tailRenderer) applyCSI(seq string) bool {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return false
	}
	cmd := seq[len(seq)-1]
	params := seq[2 : len(seq)-1]
	switch cmd {
	case 'A':
		n := 1
		if params != "" {
			parts := strings.Split(params, ";")
			if v, err := strconv.Atoi(parts[0]); err == nil && v > 0 {
				n = v
			}
		}
		r.moveCursorUp(n)
		return true
	default:
		return false
	}
}

func (r *tailRenderer) logicalLines() []string {
	out := make([]string, 0, len(r.history)+len(r.active))
	out = append(out, r.history...)
	activeLen := len(r.active)
	for activeLen > 0 && len(r.active[activeLen-1].runes) == 0 {
		activeLen--
	}
	for i := 0; i < activeLen; i++ {
		out = append(out, r.active[i].String())
	}
	if r.limit > 0 && len(out) > r.limit {
		out = out[len(out)-r.limit:]
	}
	return out
}

func (r *tailRenderer) compactActive() {
	if len(r.active) <= r.activeWindow {
		return
	}
	excess := len(r.active) - r.activeWindow
	if excess <= 0 {
		return
	}
	if excess > r.cursorLine {
		excess = r.cursorLine
	}
	if excess <= 0 {
		return
	}
	for i := 0; i < excess; i++ {
		r.history = append(r.history, r.active[i].String())
	}
	r.active = append([]lineBuffer(nil), r.active[excess:]...)
	r.cursorLine -= excess

	if r.limit > 0 {
		maxHistory := r.limit - len(r.active)
		if maxHistory < 0 {
			maxHistory = 0
		}
		if len(r.history) > maxHistory {
			r.history = r.history[len(r.history)-maxHistory:]
		}
	}
}

func csiDone(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

type lineBuffer struct {
	runes []rune
}

func (l *lineBuffer) WriteAt(col int, ru rune) {
	if col < 0 {
		col = 0
	}
	pos := l.runeIndexForColumn(col)

	if pos >= len(l.runes) {
		for w := l.visualWidth(); w < col; w++ {
			l.runes = append(l.runes, ' ')
		}
		l.runes = append(l.runes, ru)
		return
	}
	l.runes[pos] = ru
}

func (l *lineBuffer) runeIndexForColumn(col int) int {
	if col <= 0 {
		return 0
	}
	w := 0
	for i, ru := range l.runes {
		rw := runewidth.RuneWidth(ru)
		if rw < 1 {
			rw = 1
		}
		if w >= col {
			return i
		}
		if w+rw > col {
			return i
		}
		w += rw
	}
	return len(l.runes)
}

func (l *lineBuffer) visualWidth() int {
	w := 0
	for _, ru := range l.runes {
		rw := runewidth.RuneWidth(ru)
		if rw < 1 {
			rw = 1
		}
		w += rw
	}
	return w
}

func (l *lineBuffer) String() string {
	return string(l.runes)
}

func wrapRunes(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if line == "" {
		return []string{""}
	}
	var out []string
	var b strings.Builder
	w := 0
	for _, ru := range line {
		rw := runewidth.RuneWidth(ru)
		if rw < 1 {
			rw = 1
		}
		if w+rw > width && b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
			w = 0
		}
		b.WriteRune(ru)
		w += rw
		if w >= width {
			out = append(out, b.String())
			b.Reset()
			w = 0
		}
	}
	if b.Len() > 0 || len(out) == 0 {
		out = append(out, b.String())
	}
	return out
}

type logFollower struct {
	path        string
	offset      int64
	initialized bool
	renderer    tailRenderer
	missing     bool
}

func newLogFollower(path string) *logFollower {
	return &logFollower{
		path:     path,
		renderer: newTailRenderer(renderLineLimit),
	}
}

func (f *logFollower) reset(path string) {
	f.path = path
	f.offset = 0
	f.initialized = false
	f.renderer.reset()
	f.missing = false
}

func (f *logFollower) poll(label streamLabel) (streamChunk, error) {
	chunk := streamChunk{Label: label}

	st, err := os.Stat(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			f.missing = true
			chunk.Missing = true
			return chunk, nil
		}
		return chunk, err
	}

	if st.Size() < f.offset {
		f.offset = 0
		f.initialized = false
		f.renderer.reset()
	}

	file, err := os.Open(f.path)
	if err != nil {
		return chunk, err
	}
	defer file.Close()

	if !f.initialized {
		start := int64(0)
		if st.Size() > initialTailBytes {
			start = st.Size() - initialTailBytes
		}
		if _, err := file.Seek(start, io.SeekStart); err != nil {
			return chunk, err
		}
		buf, err := io.ReadAll(file)
		if err != nil {
			return chunk, err
		}
		if start > 0 {
			if idx := strings.IndexByte(string(buf), '\n'); idx >= 0 && idx+1 < len(buf) {
				buf = buf[idx+1:]
			}
		}
		newLines, changed := f.renderer.ingest(buf)
		chunk.NewLines = newLines
		chunk.CurrentChanged = changed
		chunk.CurrentLine = f.renderer.currentLine()
		f.offset = st.Size()
		f.initialized = true
		f.missing = false
		return chunk, nil
	}

	if st.Size() == f.offset {
		chunk.CurrentLine = f.renderer.currentLine()
		chunk.Missing = false
		return chunk, nil
	}

	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return chunk, err
	}
	buf, err := io.ReadAll(file)
	if err != nil {
		return chunk, err
	}
	newLines, changed := f.renderer.ingest(buf)
	f.offset = st.Size()
	f.missing = false

	chunk.NewLines = newLines
	chunk.CurrentChanged = changed
	chunk.CurrentLine = f.renderer.currentLine()
	return chunk, nil
}

func (f *logFollower) content(width int) string {
	return f.renderer.contentWrapped(width)
}

type mergedBuffer struct {
	lines      []string
	limit      int
	outCurrent string
	errCurrent string
}

func newMergedBuffer(limit int) mergedBuffer {
	return mergedBuffer{lines: make([]string, 0, 256), limit: limit}
}

func (m *mergedBuffer) reset() {
	m.lines = m.lines[:0]
	m.outCurrent = ""
	m.errCurrent = ""
}

func (m *mergedBuffer) addLine(label streamLabel, line string) {
	m.lines = append(m.lines, fmt.Sprintf("[%s] %s", label, line))
	if len(m.lines) > m.limit {
		drop := len(m.lines) - m.limit
		m.lines = m.lines[drop:]
	}
}

func (m *mergedBuffer) applyChunk(chunk streamChunk) {
	for _, line := range chunk.NewLines {
		m.addLine(chunk.Label, line)
	}
	if chunk.CurrentChanged {
		switch chunk.Label {
		case streamOut:
			m.outCurrent = chunk.CurrentLine
		case streamErr:
			m.errCurrent = chunk.CurrentLine
		}
	}
}

func (m *mergedBuffer) content() string {
	out := append([]string{}, m.lines...)
	if m.outCurrent != "" {
		out = append(out, fmt.Sprintf("[%s] %s", streamOut, m.outCurrent))
	}
	if m.errCurrent != "" {
		out = append(out, fmt.Sprintf("[%s] %s", streamErr, m.errCurrent))
	}
	return strings.Join(out, "\n")
}
