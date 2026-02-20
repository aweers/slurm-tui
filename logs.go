package main

import (
	"fmt"
	"io"
	"os"
	"strings"
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
	lines       []string
	currentLine string
	limit       int
}

func newTailRenderer(limit int) tailRenderer {
	return tailRenderer{lines: make([]string, 0, 256), limit: limit}
}

func (r *tailRenderer) reset() {
	r.lines = r.lines[:0]
	r.currentLine = ""
}

func (r *tailRenderer) appendLine(line string) {
	r.lines = append(r.lines, line)
	if len(r.lines) > r.limit {
		drop := len(r.lines) - r.limit
		r.lines = r.lines[drop:]
	}
}

func (r *tailRenderer) ingest(data []byte) (newLines []string, currentChanged bool) {
	for _, b := range data {
		switch b {
		case '\r':
			r.currentLine = ""
			currentChanged = true
		case '\n':
			line := r.currentLine
			r.appendLine(line)
			newLines = append(newLines, line)
			r.currentLine = ""
			currentChanged = true
		default:
			r.currentLine += string(b)
			currentChanged = true
		}
	}
	return newLines, currentChanged
}

func (r *tailRenderer) content() string {
	if len(r.lines) == 0 {
		return r.currentLine
	}
	if r.currentLine == "" {
		return strings.Join(r.lines, "\n")
	}
	return strings.Join(append(append([]string{}, r.lines...), r.currentLine), "\n")
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
		chunk.CurrentLine = f.renderer.currentLine
		f.offset = st.Size()
		f.initialized = true
		f.missing = false
		return chunk, nil
	}

	if st.Size() == f.offset {
		chunk.CurrentLine = f.renderer.currentLine
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
	chunk.CurrentLine = f.renderer.currentLine
	return chunk, nil
}

func (f *logFollower) content() string {
	return f.renderer.content()
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
