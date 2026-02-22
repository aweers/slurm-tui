package main

import (
	"testing"
	"time"
)

func TestJobStoreKeepsTerminalUntilDismissed(t *testing.T) {
	now := time.Now()
	store := NewJobStore()

	store.ApplySnapshot([]Job{{ID: "1", Name: "train", State: "RUNNING"}}, now)
	store.ApplySnapshot([]Job{}, now.Add(5*time.Second))

	jobs := store.VisibleJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].State != "COMPLETED" {
		t.Fatalf("expected completed fallback, got %s", jobs[0].State)
	}

	if ok := store.DismissIfTerminal("1"); !ok {
		t.Fatalf("expected dismiss to succeed for terminal job")
	}
	jobs = store.VisibleJobs()
	if len(jobs) != 0 {
		t.Fatalf("expected dismissed job hidden, got %d visible", len(jobs))
	}
}

func TestJobStoreDoesNotDismissActive(t *testing.T) {
	store := NewJobStore()
	store.ApplySnapshot([]Job{{ID: "1", Name: "train", State: "RUNNING"}}, time.Now())

	if ok := store.DismissIfTerminal("1"); ok {
		t.Fatalf("expected active job dismiss to fail")
	}
}

func TestJobStoreMarksMissingCompletingAsTerminal(t *testing.T) {
	now := time.Now()
	store := NewJobStore()

	store.ApplySnapshot([]Job{{ID: "1", Name: "train", State: "COMPLETING"}}, now)
	store.ApplySnapshot([]Job{}, now.Add(5*time.Second))

	rec, ok := store.Record("1")
	if !ok {
		t.Fatalf("expected record to exist")
	}
	if !rec.Terminal {
		t.Fatalf("expected missing completing job to become terminal")
	}
	if rec.Job.State != "COMPLETED" {
		t.Fatalf("expected completed fallback, got %s", rec.Job.State)
	}
	if ok := store.DismissIfTerminal("1"); !ok {
		t.Fatalf("expected dismiss to succeed for terminal job")
	}
}
