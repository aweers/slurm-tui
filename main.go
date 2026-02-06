package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	activeTab   int // 0 = queue, 1 = job details
	width       int
	height      int
	jobs        []Job
	vpOut       viewport.Model
	vpErr       viewport.Model
	vpReady     bool
	focusedPane int
	err         error
	confirming  bool
	dismissed   map[string]bool
}

type Job struct {
	ID        string
	Name      string
	State     string
	Time      string
	TimeLimit string
	Nodes     string
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

func updateViewportContent(vp *viewport.Model, content string) {
	wasAtBottom := vp.AtBottom()

	vp.SetContent(content)

	if wasAtBottom {
		vp.GotoBottom()
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 6
		footerHeight := 2
		paneHeight := m.height - headerHeight - footerHeight
		halfWidth := (m.width / 2) - 2

		if !m.vpReady {
			m.vpOut = viewport.New(halfWidth, paneHeight)
			m.vpErr = viewport.New(halfWidth, paneHeight)
			m.vpReady = true
		} else {
			m.vpOut.Width = halfWidth
			m.vpOut.Height = paneHeight
			m.vpErr.Width = halfWidth
			m.vpErr.Height = paneHeight
		}

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

		case "left":
			if m.activeTab > 0 {
				m.focusedPane = 0
			}

		case "right":
			if m.activeTab > 0 {
				m.focusedPane = 1
			}
		}

		if m.activeTab > 0 && m.vpReady {
			if m.focusedPane == 0 {
				m.vpOut, cmd = m.vpOut.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				m.vpErr, cmd = m.vpErr.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

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
		if m.vpReady {
			updateViewportContent(&m.vpOut, msg.out)
			updateViewportContent(&m.vpErr, msg.err)
		}

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
	baseTabStyle := lipgloss.NewStyle().Padding(0, 1)

	var tabs []string

	if m.activeTab == 0 {
		tabs = append(tabs, baseTabStyle.Copy().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Border(lipgloss.RoundedBorder()).
			Render("Summary"))
	} else {
		tabs = append(tabs, baseTabStyle.Copy().
			Foreground(lipgloss.Color("240")).
			Border(lipgloss.NormalBorder()).
			Render("Summary"))
	}

	for i, job := range m.jobs {
		c := getJobColor(job.State)
		title := fmt.Sprintf("%s (%s)", job.ID, job.State)
		style := baseTabStyle.Copy().Foreground(c)
		if m.activeTab == i+1 {
			style = style.Border(lipgloss.RoundedBorder()).Bold(true)
		} else {
			style = style.Border(lipgloss.NormalBorder())
		}
		tabs = append(tabs, style.Render(title))
	}

	tabRow := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	content := ""

	if m.activeTab == 0 {
		header := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Render(fmt.Sprintf("%-10s  %-15s  %-12s  %-12s  %-15s", "JOB ID", "NAME", "STATE", "TIME", "NODES"))

		rows := ""
		for _, j := range m.jobs {
			stateStyle := lipgloss.NewStyle().Foreground(getJobColor(j.State))
			rows += fmt.Sprintf("%-10s  %-15s  %s  %-12s  %-15s\n",
				j.ID,
				// Truncate name if too long
				func() string {
					if len(j.Name) > 15 {
						return j.Name[:12] + "..."
					} else {
						return j.Name
					}
				}(),
				stateStyle.Render(j.State),
				j.Time,
				j.Nodes)
		}

		content = "\n " + header + "\n\n " + strings.ReplaceAll(rows, "\n", "\n ")
		content += "\n  [r] Refresh  [q] Quit  [Tab] Cycle Jobs"

	} else {
		if !m.vpReady {
			content = "Initializing viewports..."
		} else {
			var leftBorder, rightBorder lipgloss.Style

			focusedStyle := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("69")).
				Padding(0, 1)

			unfocusedStyle := lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 1)

			if m.focusedPane == 0 {
				leftBorder = focusedStyle
				rightBorder = unfocusedStyle
			} else {
				leftBorder = unfocusedStyle
				rightBorder = focusedStyle
			}

			leftPane := leftBorder.Render(m.vpOut.View())
			rightPane := rightBorder.Render(m.vpErr.View())

			job := m.jobs[m.activeTab-1]
			header := lipgloss.NewStyle().
				Foreground(getJobColor(job.State)).
				Render(fmt.Sprintf("Viewing Job %s on Node %s [%s]", job.ID, job.Nodes, job.State))

			content = "\n  " + header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
		}
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
	cmd := exec.Command("squeue", "--me", "--noheader", "-o", "%i %j %T %M %L %N")

	output, err := cmd.CombinedOutput()
	if err != nil {
		// return dummy data (development)
		return []Job{
			{ID: "101", Name: "alpha_run", State: "RUNNING", Time: "05:00", TimeLimit: "24:00", Nodes: "gpu-node-01"},
			{ID: "102", Name: "beta_sim", State: "PENDING", Time: "00:00", TimeLimit: "02:00", Nodes: "(Priority)"},
			{ID: "103", Name: "gamma_train", State: "COMPLETED", Time: "01:30", TimeLimit: "02:00", Nodes: "cpu-node-05"},
		}, nil
	}

	var jobs []Job
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 6 {
			jobs = append(jobs, Job{
				ID:        parts[0],
				Name:      parts[1],
				State:     parts[2],
				Time:      parts[3],
				TimeLimit: parts[4],
				Nodes:     parts[5],
			})
		} else {
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
	}
	return jobs, nil
}

func getJobColor(state string) lipgloss.Color {
	switch state {
	case "RUNNING":
		return lipgloss.Color("42")
	case "PENDING":
		return lipgloss.Color("220")
	case "COMPLETED":
		return lipgloss.Color("240")
	case "FAILED", "CANCELLED", "TIMEOUT":
		return lipgloss.Color("196")
	default:
		return lipgloss.Color("255")
	}
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
