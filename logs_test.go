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
