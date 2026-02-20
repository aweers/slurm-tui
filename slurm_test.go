package main

import "testing"

func TestParseSqueueOutput(t *testing.T) {
	input := "101 alpha RUNNING 00:10 01:00 node-a\n102 beta PENDING 00:00 02:00 (Priority)\n"
	jobs := parseSqueueOutput(input)

	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].ID != "101" || jobs[0].Nodes != "node-a" {
		t.Fatalf("unexpected first job: %+v", jobs[0])
	}
	if jobs[1].ID != "102" || jobs[1].State != "PENDING" {
		t.Fatalf("unexpected second job: %+v", jobs[1])
	}
}

func TestParseSqueueOutputSkipsMalformed(t *testing.T) {
	input := "bad line\n103 gamma RUNNING 00:10 01:00\n"
	jobs := parseSqueueOutput(input)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 parsed row, got %d", len(jobs))
	}
	if jobs[0].ID != "103" {
		t.Fatalf("expected id 103, got %s", jobs[0].ID)
	}
}
