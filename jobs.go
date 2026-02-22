package main

import "time"

type Job struct {
	ID        string
	Name      string
	State     string
	Time      string
	TimeLimit string
	Nodes     string
}

type JobRecord struct {
	Job       Job
	FirstSeen time.Time
	LastSeen  time.Time
	Terminal  bool
	Dismissed bool
}

type JobStore struct {
	records map[string]JobRecord
	order   []string
}

func NewJobStore() JobStore {
	return JobStore{records: make(map[string]JobRecord), order: []string{}}
}

func isActiveState(state string) bool {
	return state == "RUNNING" || state == "PENDING"
}

func isTerminalState(state string) bool {
	switch state {
	case "COMPLETED", "FAILED", "CANCELLED", "TIMEOUT", "NODE_FAIL", "OUT_OF_MEMORY", "PREEMPTED", "BOOT_FAIL", "DEADLINE":
		return true
	default:
		return false
	}
}

func (s *JobStore) ApplySnapshot(jobs []Job, now time.Time) {
	seen := make(map[string]bool, len(jobs))

	for _, incoming := range jobs {
		seen[incoming.ID] = true

		rec, exists := s.records[incoming.ID]
		if !exists {
			rec = JobRecord{Job: incoming, FirstSeen: now}
			s.order = append(s.order, incoming.ID)
		}

		rec.Job = incoming
		rec.LastSeen = now
		rec.Terminal = isTerminalState(incoming.State)
		s.records[incoming.ID] = rec
	}

	for id, rec := range s.records {
		if seen[id] {
			continue
		}
		if !rec.Terminal {
			rec.Job.State = "COMPLETED"
			rec.Terminal = true
			rec.LastSeen = now
			s.records[id] = rec
		}
	}
}

func (s *JobStore) VisibleJobs() []Job {
	jobs := make([]Job, 0, len(s.order))
	for _, id := range s.order {
		rec, ok := s.records[id]
		if !ok || rec.Dismissed {
			continue
		}
		jobs = append(jobs, rec.Job)
	}
	return jobs
}

func (s *JobStore) DismissIfTerminal(jobID string) bool {
	rec, ok := s.records[jobID]
	if !ok || !rec.Terminal {
		return false
	}
	rec.Dismissed = true
	s.records[jobID] = rec
	return true
}

func (s *JobStore) ClearDismissedAndTerminal() {
	for id, rec := range s.records {
		if rec.Terminal {
			rec.Dismissed = true
			s.records[id] = rec
		}
	}
}

func (s *JobStore) Record(jobID string) (JobRecord, bool) {
	rec, ok := s.records[jobID]
	return rec, ok
}
