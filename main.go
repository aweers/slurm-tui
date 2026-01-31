package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	activeTab  int // 0 = queue, 1 = job details
	width      int
	height     int
	jobs       []Job
	logsOut    string
	logsErr    string
	err        error
	confirming bool
	dismissed  map[string]bool
}

type Job struct {
	ID        string
	Name      string
	State     string
	Time      string
	TimeLimit string
}

type jobMsg []Job
type errMsg error

type tickMsg time.Time

type logMsg struct {
	out string
	err string
}

func initialModel() model {
	return model{
		activeTab: 0,
		dismissed: make(map[string]bool),
	}
}

func waitForTick() tea.Cmd {
	return tea.Tick(time.Second*5, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchJobsCmd(), waitForTick())
}

func (m *model) mergeJobs(newJobs []Job) {
	m.jobs = newJobs
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		if m.confirming {
			switch msg.String() {
			case "y", "Y":
				m.confirming = false

				if m.activeTab > 0 && m.activeTab <= len(m.jobs) {
					job := m.jobs[m.activeTab-1]

					if job.State == "RUNNING" || job.State == "PENDING" {
						cmds = append(cmds, cancelJobCmd(job.ID))
					} else {
						m.dismissed[job.ID] = true
						cmds = append(cmds, fetchJobsCmd())
						m.activeTab = 0
					}
				}
			default:
				m.confirming = false
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "r":
			return m, fetchJobsCmd()

		case "d":
			if m.activeTab > 0 {
				m.confirming = true
			}

		case "tab":
			m.activeTab++
			if m.activeTab > len(m.jobs) {
				m.activeTab = 0
			}
			if m.activeTab > 0 {
				job := m.jobs[m.activeTab-1]
				cmds = append(cmds, readLogCmd(job.ID))
			}

		case "shift+tab":
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.jobs)
			}
			if m.activeTab > 0 {
				job := m.jobs[m.activeTab-1]
				cmds = append(cmds, readLogCmd(job.ID))
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case jobMsg:
		var visibleJobs []Job
		for _, j := range msg {
			if !m.dismissed[j.ID] {
				visibleJobs = append(visibleJobs, j)
			}
		}

		m.jobs = msg
		if m.activeTab > len(m.jobs) {
			m.activeTab = 0
		}

	case logMsg:
		m.logsOut = msg.out
		m.logsErr = msg.err

	case tickMsg:
		cmds = append(cmds, fetchJobsCmd())

		if m.activeTab > 0 && m.activeTab <= len(m.jobs) {
			job := m.jobs[m.activeTab-1]
			cmds = append(cmds, readLogCmd(job.ID))
		}
		cmds = append(cmds, waitForTick())

	case errMsg:
		m.err = msg
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	activeTabStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("205")).
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	inactiveTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Border(lipgloss.NormalBorder()).
		Padding(0, 1)

	var tabs []string

	if m.activeTab == 0 {
		tabs = append(tabs, activeTabStyle.Render("Summary"))
	} else {
		tabs = append(tabs, inactiveTabStyle.Render("Summary"))
	}

	for i, job := range m.jobs {
		title := fmt.Sprintf("%s (%s)", job.ID, job.State)
		if m.activeTab == i+1 {
			tabs = append(tabs, activeTabStyle.Render(title))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(title))
		}
	}

	tabRow := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	content := ""

	if m.activeTab == 0 {
		content = "\n  Summary of " + fmt.Sprintf("%d", len(m.jobs)) + " jobs.\n\n"
		for _, j := range m.jobs {
			content += fmt.Sprintf("  ID: %-10s | %-10s | %s\n", j.ID, j.Name, j.State)
		}
		content += "\n  [r] Refresh  [q] Quit  [Tab] Cycle Jobs"

	} else {
		halfWidth := (m.width / 2) - 4
		paneHeight := m.height - 10

		paneStyle := lipgloss.NewStyle().
			Width(halfWidth).
			Height(paneHeight).
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

		// Create the two boxes
		leftPane := paneStyle.Render(fmt.Sprintf("STDOUT:\n\n%s", m.logsOut))
		rightPane := paneStyle.Render(fmt.Sprintf("STDERR:\n\n%s", m.logsErr))

		content = lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
		content += "\n  [d] Cancel/Dismiss (Not impl)  [r] Refresh"
	}

	if m.confirming {
		job := m.jobs[m.activeTab-1]
		action := "DISMISS"
		if job.State == "RUNNING" || job.State == "PENDING" {
			action = "CANCEL (scancel)"
		}

		alertStyle := lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("196")). // Red
			Bold(true).
			Padding(1, 2).
			Align(lipgloss.Center)

		alertBox := alertStyle.Render(fmt.Sprintf(
			"WARNING: Are you sure you want to %s job %s?\n\n[y] Yes    [n] No",
			action, job.ID,
		))

		content = "\n\n" + alertBox + "\n\n"
	}

	return tabRow + "\n" + content
}

func checkSlurm() ([]Job, error) {
	// We use -o to specify format: %i (ID) %j (Name) %T (State) %M (Time Used) %L (Time Left)
	cmd := exec.Command("squeue", "--me", "--noheader", "-o", "%i %j %T %M %L")

	output, err := cmd.CombinedOutput()
	if err != nil {
		// return dummy data (development)
		return []Job{
			{ID: "101", Name: "alpha_run", State: "RUNNING", Time: "05:00", TimeLimit: "24:00"},
			{ID: "102", Name: "beta_sim", State: "PENDING", Time: "00:00", TimeLimit: "02:00"},
			{ID: "103", Name: "gamma_train", State: "COMPLETED", Time: "01:30", TimeLimit: "02:00"},
		}, nil
	}

	var jobs []Job
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 5 {
			jobs = append(jobs, Job{
				ID:        parts[0],
				Name:      parts[1],
				State:     parts[2],
				Time:      parts[3],
				TimeLimit: parts[4],
			})
		}
	}
	return jobs, nil
}

func fetchJobsCmd() tea.Cmd {
	return func() tea.Msg {
		jobs, err := checkSlurm()
		if err != nil {
			return errMsg(err)

		}
		return jobMsg(jobs)
	}
}

func readLogCmd(jobID string) tea.Cmd {
	return func() tea.Msg {
		outBytes, outErr := os.ReadFile(fmt.Sprintf("slurm_logs/%s.out", jobID))
		errBytes, errErr := os.ReadFile(fmt.Sprintf("slurm_logs/%s.err", jobID))

		outStr := ""
		errStr := ""

		if outErr == nil {
			outStr = string(outBytes)
		} else {
			outStr = fmt.Sprintf("Waiting for output log for job %s...\n(File not found or empty)", jobID)
		}

		if errErr == nil {
			errStr = string(errBytes)
		} else {
			errStr = fmt.Sprintf("Waiting for error log for job %s...\n(File not found or empty)", jobID)
		}

		return logMsg{out: outStr, err: errStr}

	}
}

func cancelJobCmd(id string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("scancel", id)
		err := cmd.Run()

		if err != nil {
			return errMsg(fmt.Errorf("failed to cancel job %s: %v", id, err))
		}
		return nil
	}
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("There has been an error: %v", err)
		os.Exit(1)
	}
}
