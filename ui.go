package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	uiTickInterval   = 250 * time.Millisecond
	jobsRefreshEvery = 5 * time.Second
)

type model struct {
	width  int
	height int

	store JobStore
	jobs  []Job

	selectedIdx int
	selectedID  string

	focusArea int // 0 jobs, 1 stdout, 2 stderr/merged

	vpJobs   viewport.Model
	vpOut    viewport.Model
	vpErr    viewport.Model
	vpMerged viewport.Model
	vpReady  bool

	outFollower *logFollower
	errFollower *logFollower
	mergedBuf   mergedBuffer

	mergedMode bool
	follow     bool

	lastJobFetch       time.Time
	statusText         string
	statusColor        string
	err                error
	cancelConfirm      bool
	cancelConfirmJobID string

	outContentCache    string
	errContentCache    string
	mergedContentCache string
}

type jobMsg []Job
type errMsg error
type tickMsg time.Time
type statusMsg struct {
	text  string
	color string
}

func initialModel() model {
	return model{
		store:       NewJobStore(),
		selectedIdx: 0,
		focusArea:   0,
		follow:      true,
		mergedBuf:   newMergedBuffer(renderLineLimit),
	}
}

func waitForTick() tea.Cmd {
	return tea.Tick(uiTickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
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

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchJobsCmd(), waitForTick())
}

func getJobColor(state string) lipgloss.Color {
	switch state {
	case "RUNNING":
		return lipgloss.Color("42")
	case "PENDING":
		return lipgloss.Color("220")
	case "COMPLETED":
		return lipgloss.Color("246")
	case "FAILED", "CANCELLED", "TIMEOUT", "OUT_OF_MEMORY", "NODE_FAIL", "PREEMPTED":
		return lipgloss.Color("196")
	default:
		return lipgloss.Color("252")
	}
}

func updateViewportContent(vp *viewport.Model, content string, cache *string, follow bool) {
	if *cache == content {
		return
	}
	wasAtBottom := vp.AtBottom()
	yOffset := vp.YOffset
	vp.SetContent(content)
	if follow || wasAtBottom {
		vp.GotoBottom()
	} else {
		vp.SetYOffset(yOffset)
	}
	*cache = content
}

func (m *model) selectedJob() (Job, bool) {
	if len(m.jobs) == 0 {
		return Job{}, false
	}
	if m.selectedIdx < 0 {
		m.selectedIdx = 0
	}
	if m.selectedIdx >= len(m.jobs) {
		m.selectedIdx = len(m.jobs) - 1
	}
	return m.jobs[m.selectedIdx], true
}

func (m *model) ensureSelectionByID() {
	if m.selectedID == "" {
		if job, ok := m.selectedJob(); ok {
			m.selectedID = job.ID
		}
		return
	}
	for i, j := range m.jobs {
		if j.ID == m.selectedID {
			m.selectedIdx = i
			return
		}
	}
	if len(m.jobs) == 0 {
		m.selectedIdx = 0
		m.selectedID = ""
		return
	}
	if m.selectedIdx >= len(m.jobs) {
		m.selectedIdx = len(m.jobs) - 1
	}
	m.selectedID = m.jobs[m.selectedIdx].ID
}

func (m *model) switchToJob(job Job) {
	outPath := fmt.Sprintf("slurm_logs/%s.out", job.ID)
	errPath := fmt.Sprintf("slurm_logs/%s.err", job.ID)

	if m.outFollower == nil {
		m.outFollower = newLogFollower(outPath)
	} else {
		m.outFollower.reset(outPath)
	}
	if m.errFollower == nil {
		m.errFollower = newLogFollower(errPath)
	} else {
		m.errFollower.reset(errPath)
	}
	m.mergedBuf.reset()
	m.follow = true

	if m.vpReady {
		m.outContentCache = "\x00"
		m.errContentCache = "\x00"
		m.mergedContentCache = "\x00"
		updateViewportContent(&m.vpOut, "", &m.outContentCache, true)
		updateViewportContent(&m.vpErr, "", &m.errContentCache, true)
		updateViewportContent(&m.vpMerged, "", &m.mergedContentCache, true)
	}
}

func (m *model) armCancelConfirm(jobID string) {
	m.cancelConfirm = true
	m.cancelConfirmJobID = jobID
	m.statusText = fmt.Sprintf("cancel %s? [y/N]", jobID)
	m.statusColor = "220"
}

func (m *model) clearCancelConfirm() {
	m.cancelConfirm = false
	m.cancelConfirmJobID = ""
}

func (m *model) handleCancelConfirmKey(key string) (tea.Cmd, bool) {
	switch key {
	case "y", "Y", "enter":
		jobID := m.cancelConfirmJobID
		m.clearCancelConfirm()
		if err := cancelJob(jobID); err != nil {
			m.statusText = err.Error()
			m.statusColor = "196"
			return nil, true
		}
		m.statusText = fmt.Sprintf("cancel signal sent for %s", jobID)
		m.statusColor = "42"
		return fetchJobsCmd(), true
	case "n", "N", "esc", "c":
		jobID := m.cancelConfirmJobID
		m.clearCancelConfirm()
		m.statusText = fmt.Sprintf("cancel aborted for %s", jobID)
		m.statusColor = "244"
		return nil, true
	default:
		m.statusText = "cancel pending: press y to confirm or n/esc to abort"
		m.statusColor = "220"
		return nil, true
	}
}

func padOrTrimToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) > width {
		s = ansi.Truncate(s, width, "")
	}
	if pad := width - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func centerOverlay(base, overlay string, width, height int) string {
	if width <= 0 || height <= 0 {
		return base
	}

	baseLines := strings.Split(base, "\n")
	if len(baseLines) > height {
		baseLines = baseLines[:height]
	} else if len(baseLines) < height {
		baseLines = append(baseLines, make([]string, height-len(baseLines))...)
	}
	for i := range baseLines {
		baseLines[i] = padOrTrimToWidth(baseLines[i], width)
	}

	overlayLines := strings.Split(overlay, "\n")
	if len(overlayLines) > height {
		overlayLines = overlayLines[:height]
	}
	top := max(0, (height-len(overlayLines))/2)

	for i, line := range overlayLines {
		if top+i >= len(baseLines) {
			break
		}
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "")
		}
		left := max(0, (width-lipgloss.Width(line))/2)
		baseLine := baseLines[top+i]
		prefix := ansi.Cut(baseLine, 0, left)
		suffixStart := left + lipgloss.Width(line)
		suffix := ""
		if suffixStart < width {
			suffix = ansi.Cut(baseLine, suffixStart, width)
		}
		baseLines[top+i] = padOrTrimToWidth(prefix+line+suffix, width)
	}

	return strings.Join(baseLines, "\n")
}

func (m model) renderCancelModal(base string) string {
	if m.width <= 0 || m.height <= 0 {
		return base
	}

	modalWidth := min(68, max(40, m.width-8))
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render("Cancel Job")
	message := fmt.Sprintf("Send cancel signal to job %s?", m.cancelConfirmJobID)
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render("[y/enter] confirm    [n/esc] abort")

	body := strings.Join([]string{title, "", message, "", hint}, "\n")
	modal := lipgloss.NewStyle().
		Width(modalWidth).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("255")).
		Render(body)

	dimmed := lipgloss.NewStyle().Faint(true).Render(base)
	return centerOverlay(dimmed, modal, m.width, m.height)
}

func (m *model) pollSelectedLogs() {
	job, ok := m.selectedJob()
	if !ok {
		return
	}
	if m.outFollower == nil || m.errFollower == nil {
		m.switchToJob(job)
	}

	outChunk, outErr := m.outFollower.poll(streamOut)
	if outErr != nil {
		m.statusText = fmt.Sprintf("log read error (stdout): %v", outErr)
		m.statusColor = "196"
	}
	errChunk, errErr := m.errFollower.poll(streamErr)
	if errErr != nil {
		m.statusText = fmt.Sprintf("log read error (stderr): %v", errErr)
		m.statusColor = "196"
	}

	m.mergedBuf.applyChunk(outChunk)
	m.mergedBuf.applyChunk(errChunk)

	if !m.vpReady {
		return
	}

	outContent := m.outFollower.content(m.vpOut.Width)
	errContent := m.errFollower.content(m.vpErr.Width)
	if outChunk.Missing && outContent == "" {
		outContent = fmt.Sprintf("Waiting for output log for job %s...", job.ID)
	}
	if errChunk.Missing && errContent == "" {
		errContent = fmt.Sprintf("Waiting for error log for job %s...", job.ID)
	}

	updateViewportContent(&m.vpOut, outContent, &m.outContentCache, m.follow)
	updateViewportContent(&m.vpErr, errContent, &m.errContentCache, m.follow)
	updateViewportContent(&m.vpMerged, m.mergedBuf.content(), &m.mergedContentCache, m.follow)
}

func isScrollKey(k string) bool {
	switch k {
	case "up", "down", "pgup", "pgdown", "home", "end", "u", "d", "k", "j", "g", "G":
		return true
	default:
		return false
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 4
		footerHeight := 2
		bodyHeight := max(8, m.height-headerHeight-footerHeight)
		jobsHeight := max(5, bodyHeight/3)
		logsHeight := max(4, bodyHeight-jobsHeight)

		if !m.vpReady {
			m.vpJobs = viewport.New(max(20, m.width-4), jobsHeight)
			m.vpOut = viewport.New(max(20, (m.width/2)-4), logsHeight)
			m.vpErr = viewport.New(max(20, (m.width/2)-4), logsHeight)
			m.vpMerged = viewport.New(max(20, m.width-4), logsHeight)
			m.vpReady = true
		} else {
			m.vpJobs.Width = max(20, m.width-4)
			m.vpJobs.Height = jobsHeight
			m.vpOut.Width = max(20, (m.width/2)-4)
			m.vpOut.Height = logsHeight
			m.vpErr.Width = max(20, (m.width/2)-4)
			m.vpErr.Height = logsHeight
			m.vpMerged.Width = max(20, m.width-4)
			m.vpMerged.Height = logsHeight
		}
		m.outContentCache = "\x00"
		m.errContentCache = "\x00"
		m.mergedContentCache = "\x00"

	case jobMsg:
		now := time.Now()
		m.store.ApplySnapshot(msg, now)
		m.jobs = m.store.VisibleJobs()
		m.ensureSelectionByID()
		if job, ok := m.selectedJob(); ok && job.ID != m.selectedID {
			m.selectedID = job.ID
			m.switchToJob(job)
		}
		if m.selectedID == "" {
			if job, ok := m.selectedJob(); ok {
				m.selectedID = job.ID
				m.switchToJob(job)
			}
		}
		m.lastJobFetch = now
		m.statusText = fmt.Sprintf("jobs refreshed at %s", now.Format("15:04:05"))
		m.statusColor = "42"

	case errMsg:
		m.err = msg
		m.statusText = fmt.Sprintf("squeue error: %v", msg)
		m.statusColor = "196"

	case tickMsg:
		if m.lastJobFetch.IsZero() || time.Since(m.lastJobFetch) >= jobsRefreshEvery {
			cmds = append(cmds, fetchJobsCmd())
		}
		m.pollSelectedLogs()
		cmds = append(cmds, waitForTick())

	case tea.KeyMsg:
		key := msg.String()
		switch key {
		case "ctrl+c":
			return m, tea.Quit
		}

		if m.cancelConfirm {
			if cmd, consumed := m.handleCancelConfirmKey(key); consumed {
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				break
			}
		}

		switch key {
		case "q":
			return m, tea.Quit
		case "r":
			cmds = append(cmds, fetchJobsCmd())
		case "m":
			m.mergedMode = !m.mergedMode
		case "f":
			m.follow = !m.follow
			if m.follow && m.vpReady {
				m.vpOut.GotoBottom()
				m.vpErr.GotoBottom()
				m.vpMerged.GotoBottom()
			}
		case "tab":
			m.focusArea = (m.focusArea + 1) % 3
		case "shift+tab":
			m.focusArea = (m.focusArea + 2) % 3
		case "up", "k":
			if m.focusArea == 0 {
				if m.selectedIdx > 0 {
					m.selectedIdx--
					m.selectedID = m.jobs[m.selectedIdx].ID
					m.switchToJob(m.jobs[m.selectedIdx])
				}
			}
		case "down", "j":
			if m.focusArea == 0 {
				if m.selectedIdx < len(m.jobs)-1 {
					m.selectedIdx++
					m.selectedID = m.jobs[m.selectedIdx].ID
					m.switchToJob(m.jobs[m.selectedIdx])
				}
			}
		case "c":
			if job, ok := m.selectedJob(); ok {
				if !isActiveState(job.State) {
					m.statusText = "cancel only works for RUNNING/PENDING jobs"
					m.statusColor = "220"
					break
				}
				m.armCancelConfirm(job.ID)
			}
		case "d":
			if job, ok := m.selectedJob(); ok {
				if m.store.DismissIfTerminal(job.ID) {
					m.jobs = m.store.VisibleJobs()
					prev := m.selectedID
					m.ensureSelectionByID()
					if next, ok := m.selectedJob(); ok && next.ID != prev {
						m.selectedID = next.ID
						m.switchToJob(next)
					}
					m.statusText = fmt.Sprintf("dismissed %s", job.ID)
					m.statusColor = "244"
				} else {
					m.statusText = "dismiss only works for terminal jobs"
					m.statusColor = "220"
				}
			}
		case "D":
			m.store.ClearDismissedAndTerminal()
			m.jobs = m.store.VisibleJobs()
			prev := m.selectedID
			m.ensureSelectionByID()
			if next, ok := m.selectedJob(); ok && next.ID != prev {
				m.selectedID = next.ID
				m.switchToJob(next)
			}
			m.statusText = "cleared terminal jobs"
			m.statusColor = "244"
		}

		if m.vpReady {
			if m.focusArea == 0 {
				m.vpJobs, _ = m.vpJobs.Update(msg)
			} else if m.mergedMode {
				m.vpMerged, _ = m.vpMerged.Update(msg)
			} else if m.focusArea == 1 {
				m.vpOut, _ = m.vpOut.Update(msg)
			} else {
				m.vpErr, _ = m.vpErr.Update(msg)
			}

			if isScrollKey(key) && m.focusArea != 0 {
				m.follow = false
			}
			if !m.follow {
				if m.mergedMode && m.vpMerged.AtBottom() {
					m.follow = true
				} else if !m.mergedMode && ((m.focusArea == 1 && m.vpOut.AtBottom()) || (m.focusArea == 2 && m.vpErr.AtBottom())) {
					m.follow = true
				}
			}
		}
	}

	m.renderJobsViewport()
	return m, tea.Batch(cmds...)
}

func (m *model) renderJobsViewport() {
	if !m.vpReady {
		return
	}
	if len(m.jobs) == 0 {
		m.vpJobs.SetContent("No jobs yet. Press [r] to refresh.")
		return
	}

	head := fmt.Sprintf("%-2s %-9s %-18s %-11s %-10s %-14s", "", "JOB ID", "NAME", "STATE", "TIME", "NODE")
	rows := []string{head}
	for i, j := range m.jobs {
		marker := " "
		if i == m.selectedIdx {
			marker = ">"
		}
		name := j.Name
		if len(name) > 18 {
			name = name[:15] + "..."
		}
		rows = append(rows, fmt.Sprintf("%-2s %-9s %-18s %-11s %-10s %-14s", marker, j.ID, name, j.State, j.Time, j.Nodes))
	}
	m.vpJobs.SetContent(strings.Join(rows, "\n"))
}

func (m model) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69")).Render("slurm-tui")
	subtitle := "Queue + logs monitor"
	header := title + "  " + subtitle

	if m.err != nil {
		header += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  (degraded: squeue unavailable)")
	}

	if !m.vpReady {
		return header + "\n\nInitializing..."
	}

	jobsBorder := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if m.focusArea == 0 {
		jobsBorder = jobsBorder.BorderForeground(lipgloss.Color("69"))
	} else {
		jobsBorder = jobsBorder.BorderForeground(lipgloss.Color("240"))
	}

	jobsPanel := jobsBorder.Render(m.vpJobs.View())

	jobInfo := "No selection"
	if job, ok := m.selectedJob(); ok {
		state := lipgloss.NewStyle().Foreground(getJobColor(job.State)).Render(job.State)
		jobInfo = fmt.Sprintf("Job %s  %s  Node:%s", job.ID, state, job.Nodes)
	}

	var logsPanel string
	if m.mergedMode {
		border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
		if m.focusArea != 0 {
			border = border.BorderForeground(lipgloss.Color("69"))
		} else {
			border = border.BorderForeground(lipgloss.Color("240"))
		}
		logsPanel = border.Render(m.vpMerged.View())
	} else {
		left := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
		right := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
		if m.focusArea == 1 {
			left = left.BorderForeground(lipgloss.Color("69"))
		} else {
			left = left.BorderForeground(lipgloss.Color("240"))
		}
		if m.focusArea == 2 {
			right = right.BorderForeground(lipgloss.Color("69"))
		} else {
			right = right.BorderForeground(lipgloss.Color("240"))
		}
		logsPanel = lipgloss.JoinHorizontal(lipgloss.Top, left.Render(m.vpOut.View()), right.Render(m.vpErr.View()))
	}

	follow := "ON"
	followColor := lipgloss.Color("42")
	if !m.follow {
		follow = "PAUSED"
		followColor = lipgloss.Color("220")
	}

	mode := "split"
	if m.mergedMode {
		mode = "merged"
	}

	statusLine := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(
		fmt.Sprintf("Focus:%s  Mode:%s  Follow:%s", []string{"jobs", "stdout", "stderr"}[m.focusArea], mode, lipgloss.NewStyle().Foreground(followColor).Render(follow)),
	)
	actions := "[j/k] select  [tab] focus  [m] split/merged  [f] follow  [c] cancel (confirm)  [d] dismiss terminal  [D] clear terminal  [r] refresh  [q] quit"
	statusMsg := ""
	if m.statusText != "" {
		statusMsg = lipgloss.NewStyle().Foreground(lipgloss.Color(m.statusColor)).Render(m.statusText)
	}

	base := strings.Join([]string{
		header,
		jobInfo,
		jobsPanel,
		logsPanel,
		statusLine,
		actions,
		statusMsg,
	}, "\n")

	if m.cancelConfirm {
		return m.renderCancelModal(base)
	}
	return base
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
