package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func parseSqueueOutput(output string) []Job {
	var jobs []Job
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 5 {
			continue
		}

		job := Job{
			ID:        parts[0],
			Name:      parts[1],
			State:     parts[2],
			Time:      parts[3],
			TimeLimit: parts[4],
			Nodes:     "",
		}
		if len(parts) >= 6 {
			job.Nodes = parts[5]
		}
		jobs = append(jobs, job)
	}

	return jobs
}

func checkSlurm() ([]Job, error) {
	cmd := exec.Command("squeue", "--me", "--noheader", "-o", "%i %j %T %M %L %N")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	return parseSqueueOutput(string(output)), nil
}

func cancelJob(jobID string) error {
	cmd := exec.Command("scancel", jobID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cancel %s: %s", jobID, msg)
	}
	return nil
}
