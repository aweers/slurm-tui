package main

import "testing"

func TestTailRendererCarriageReturnProgress(t *testing.T) {
	r := newTailRenderer(100)
	r.ingest([]byte("progress 10%\rprogress 20%\rprogress 30%"))

	if got := r.content(); got != "progress 30%" {
		t.Fatalf("unexpected content: %q", got)
	}

	r.ingest([]byte("\n"))
	if got := r.content(); got != "progress 30%" {
		t.Fatalf("unexpected finalized content: %q", got)
	}
}

func TestTailRendererPartialLineAcrossChunks(t *testing.T) {
	r := newTailRenderer(100)
	r.ingest([]byte("hello"))
	r.ingest([]byte(" world\nnext"))

	if got := r.content(); got != "hello world\nnext" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestTailRendererCursorUp(t *testing.T) {
	r := newTailRenderer(100)
	r.ingest([]byte("line 1\nline 2\n"))
	r.ingest([]byte("\x1b[Aupdated line 2"))

	if got := r.content(); got != "line 1\nupdated line 2" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestTailRendererOverwriteKeepsTail(t *testing.T) {
	r := newTailRenderer(100)
	r.ingest([]byte("abcdef\rxy"))

	if got := r.content(); got != "xycdef" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestTailRendererUTF8AcrossChunks(t *testing.T) {
	r := newTailRenderer(100)
	block := []byte("█")
	r.ingest(block[:1])
	r.ingest(block[1:])

	if got := r.content(); got != "█" {
		t.Fatalf("unexpected utf8 content: %q", got)
	}
}

func TestTailRendererSoftWrap(t *testing.T) {
	r := newTailRenderer(100)
	r.ingest([]byte("123456789"))

	if got := r.contentWrapped(5); got != "12345\n6789" {
		t.Fatalf("unexpected wrapped content: %q", got)
	}
}
