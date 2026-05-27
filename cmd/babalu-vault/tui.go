package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CalypsoSys/babalu_vault/internal/backup"
	"github.com/CalypsoSys/babalu_vault/internal/config"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

type backupProgressMsg struct {
	row backup.SummaryRow
}

type backupStreamClosedMsg struct{}

type backupFinishedMsg struct {
	rows       []backup.SummaryRow
	err        error
	startedAt  time.Time
	finishedAt time.Time
	automatic  bool
}

type databaseStatus struct {
	Server      string
	Backup      string
	Database    string
	Method      string
	Retention   config.RetentionPolicy
	LastStatus  string
	LastRun     time.Time
	LastSize    int64
	LastError   string
	LastPaths   []string
	LastElapsed time.Duration
}

type paletteCommand struct {
	Key         string
	Label       string
	Description string
}

type eventEntry struct {
	At      time.Time
	Message string
	Level   string
}

type focusArea string

const (
	focusTargets  focusArea = "targets"
	focusActivity focusArea = "activity"
)

type model struct {
	configPath    string
	cfg           *config.Config
	configModTime time.Time
	configSize    int64
	logger        *slog.Logger
	dryRun        bool
	scheduleHour  int
	scheduleMin   int
	now           time.Time
	nextRun       time.Time
	paused        bool
	running       bool
	currentAction string
	lastRun       time.Time
	lastError     string
	width         int
	height        int
	statuses      []databaseStatus
	selected      int
	events        []eventEntry
	showPalette   bool
	showDetails   bool
	focus         focusArea
	targetsVP     viewport.Model
	activityVP    viewport.Model
	backupMsgs    chan tea.Msg
}

func newModel(configPath string, cfg *config.Config, dryRun bool, logger *slog.Logger) model {
	hour, minute, err := cfg.Settings.ScheduledTimeOfDay()
	if err != nil {
		hour = 2
		minute = 0
	}
	now := time.Now()
	state, stateErr := loadSchedulerState(cfg.Settings.StatePath)

	statuses := make([]databaseStatus, 0)
	for _, item := range cfg.Filter("", "", "") {
		statuses = append(statuses, databaseStatus{
			Server:     item.Server.Name,
			Backup:     item.Backup.Name,
			Database:   item.Target.Name,
			Method:     item.Backup.Engine,
			Retention:  item.Retention,
			LastStatus: "pending",
		})
	}
	if dryRun || cfg.Settings.DryRun {
		statuses = expandDryRunStatuses(statuses)
	}

	targetsVP := viewport.New(80, 12)
	activityVP := viewport.New(80, 8)
	targetsVP.MouseWheelEnabled = true
	activityVP.MouseWheelEnabled = true

	m := model{
		configPath:    configPath,
		cfg:           cfg,
		configModTime: configModTime(configPath),
		configSize:    configSize(configPath),
		logger:        logger,
		dryRun:        dryRun,
		scheduleHour:  hour,
		scheduleMin:   minute,
		now:           now,
		lastRun:       state.LastRunAt,
		statuses:      statuses,
		focus:         focusTargets,
		targetsVP:     targetsVP,
		activityVP:    activityVP,
	}
	m.recordEvent("info", fmt.Sprintf("loaded %d targets from %s", len(statuses), configPath))
	m.recordEvent("info", fmt.Sprintf("scheduler armed for daily run at %02d:%02d", hour, minute))
	if stateErr != nil {
		m.recordEvent("warn", fmt.Sprintf("scheduler state unavailable: %v", stateErr))
	}
	if dryRun {
		m.recordEvent("warn", "TUI dry-run mode enabled")
	}
	if shouldRunOnStartup(m.lastRun, now) {
		m.running = true
		m.currentAction = "scheduled backup"
		m.backupMsgs = make(chan tea.Msg, 1)
		m.recordEvent("info", "startup catch-up backup started")
	}
	m.nextRun = nextScheduledRun(m.lastRun, now, m.scheduleHour, m.scheduleMin)
	m.syncViewports()
	return m
}

func (m model) Init() tea.Cmd {
	if m.running {
		return tea.Batch(tickCmd(), waitBackupMsgCmd(m.backupMsgs), runBackupCmd(m.backupMsgs, m.cfg, m.logger, true, m.dryRun))
	}
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func waitBackupMsgCmd(msgs <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-msgs
		if !ok {
			return backupStreamClosedMsg{}
		}
		return msg
	}
}

func runBackupCmd(msgs chan<- tea.Msg, cfg *config.Config, logger *slog.Logger, automatic bool, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		started := time.Now()
		rows, err := executeBackupWithProgress(logger, cfg, "", "", "", dryRun || cfg.Settings.DryRun, func(row backup.SummaryRow) {
			msgs <- backupProgressMsg{row: row}
		})
		msgs <- backupFinishedMsg{
			rows:       rows,
			err:        err,
			startedAt:  started,
			finishedAt: time.Now(),
			automatic:  automatic,
		}
		close(msgs)
		return nil
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncViewports()
		return m, nil
	case tea.KeyMsg:
		if m.showPalette {
			switch msg.String() {
			case "/", "esc":
				m.showPalette = false
			case "q", "ctrl+c":
				return m, tea.Quit
			case "b":
				m.showPalette = false
				if !m.running {
					m.running = true
					m.currentAction = "manual backup"
					m.backupMsgs = make(chan tea.Msg, 1)
					m.recordEvent("info", "manual backup started")
					m.syncViewports()
					return m, tea.Batch(waitBackupMsgCmd(m.backupMsgs), runBackupCmd(m.backupMsgs, m.cfg, m.logger, false, m.dryRun))
				}
			case "p":
				m.showPalette = false
				m.togglePause()
			case "tab":
				m.toggleFocus()
			}
			m.syncViewports()
			return m, nil
		}
		if m.showDetails {
			switch msg.String() {
			case "enter", "esc":
				m.showDetails = false
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "/":
			m.showPalette = true
			m.syncViewports()
			return m, nil
		case "enter":
			if m.focus == focusTargets && m.selectedStatus() != nil {
				m.showDetails = true
				return m, nil
			}
		case "tab":
			m.toggleFocus()
			m.syncViewports()
			return m, nil
		case "b":
			if !m.running {
				m.running = true
				m.currentAction = "manual backup"
				m.backupMsgs = make(chan tea.Msg, 1)
				m.recordEvent("info", "manual backup started")
				m.syncViewports()
				return m, tea.Batch(waitBackupMsgCmd(m.backupMsgs), runBackupCmd(m.backupMsgs, m.cfg, m.logger, false, m.dryRun))
			}
		case "p":
			m.togglePause()
			m.syncViewports()
			return m, nil
		case "up", "k":
			if m.focus == focusTargets {
				if m.selected > 0 {
					m.selected--
				}
				m.syncViewports()
				return m, nil
			}
		case "down", "j":
			if m.focus == focusTargets {
				if m.selected < len(m.statuses)-1 {
					m.selected++
				}
				m.syncViewports()
				return m, nil
			}
		}
	case tickMsg:
		m.now = time.Time(msg)
		m.reloadConfigIfChanged()
		m.nextRun = nextScheduledRun(m.lastRun, m.now, m.scheduleHour, m.scheduleMin)
		if !m.paused && !m.running && shouldRunScheduledBackup(m.lastRun, m.now, m.scheduleHour, m.scheduleMin) {
			m.running = true
			m.currentAction = "scheduled backup"
			m.backupMsgs = make(chan tea.Msg, 1)
			m.recordEvent("info", "scheduled backup started")
			m.syncViewports()
			return m, tea.Batch(tickCmd(), waitBackupMsgCmd(m.backupMsgs), runBackupCmd(m.backupMsgs, m.cfg, m.logger, true, m.dryRun))
		}
		m.syncViewports()
		return m, tickCmd()
	case backupProgressMsg:
		m.markStatusRunning(msg.row)
		m.addActivity("info", fmt.Sprintf("%s/%s/%s [%s] backup started", msg.row.Server, msg.row.Backup, msg.row.Database, msg.row.Method))
		m.syncViewports()
		if m.backupMsgs != nil {
			return m, waitBackupMsgCmd(m.backupMsgs)
		}
		return m, nil
	case backupFinishedMsg:
		m.running = false
		m.currentAction = ""
		m.backupMsgs = nil
		m.lastRun = msg.finishedAt
		m.now = msg.finishedAt
		if !(m.dryRun || m.cfg.Settings.DryRun) {
			if err := saveSchedulerState(m.cfg.Settings.StatePath, schedulerState{LastRunAt: msg.finishedAt}); err != nil {
				m.recordEvent("warn", fmt.Sprintf("failed to persist scheduler state: %v", err))
				if m.lastError == "" {
					m.lastError = err.Error()
				}
			}
		}
		m.nextRun = nextScheduledRun(m.lastRun, m.now, m.scheduleHour, m.scheduleMin)
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.recordEvent("error", fmt.Sprintf("backup failed: %v", msg.err))
		} else {
			m.lastError = ""
			label := "manual"
			if msg.automatic {
				label = "scheduled"
			}
			m.recordEvent("info", fmt.Sprintf("%s backup completed in %s", label, msg.finishedAt.Sub(msg.startedAt).Round(time.Millisecond)))
		}
		for _, row := range msg.rows {
			m.updateStatus(row, msg.finishedAt)
			for _, op := range row.Operations {
				if op.Message == "backup started" {
					continue
				}
				m.addActivity(op.Level, fmt.Sprintf("%s/%s/%s [%s] %s", row.Server, row.Backup, row.Database, row.Method, op.Message))
			}
		}
		m.syncViewports()
		return m, nil
	case backupStreamClosedMsg:
		return m, nil
	}

	if m.focus == focusActivity {
		m.activityVP, cmd = m.activityVP.Update(msg)
	} else {
		m.targetsVP, cmd = m.targetsVP.Update(msg)
	}
	return m, cmd
}

func (m *model) reloadConfigIfChanged() {
	modTime := configModTime(m.configPath)
	size := configSize(m.configPath)
	if modTime.Equal(m.configModTime) && size == m.configSize {
		return
	}

	cfg, err := config.Load(m.configPath)
	if err != nil {
		m.configModTime = modTime
		m.configSize = size
		m.recordEvent("warn", fmt.Sprintf("config reload failed: %v", err))
		return
	}

	hour, minute, err := cfg.Settings.ScheduledTimeOfDay()
	if err != nil {
		m.configModTime = modTime
		m.configSize = size
		m.recordEvent("warn", fmt.Sprintf("config reload failed: %v", err))
		return
	}

	m.cfg = cfg
	m.configModTime = modTime
	m.configSize = size
	m.scheduleHour = hour
	m.scheduleMin = minute
	m.statuses = reconcileStatuses(m.statuses, cfg, m.dryRun || cfg.Settings.DryRun)
	if m.selected >= len(m.statuses) {
		m.selected = len(m.statuses) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	m.recordEvent("info", fmt.Sprintf("config reloaded from %s", m.configPath))
}

func (m *model) togglePause() {
	m.paused = !m.paused
	if m.paused {
		m.recordEvent("warn", "scheduler paused")
	} else {
		m.recordEvent("info", "scheduler resumed")
	}
}

func (m *model) toggleFocus() {
	if m.focus == focusTargets {
		m.focus = focusActivity
		return
	}
	m.focus = focusTargets
}

func (m *model) updateStatus(row backup.SummaryRow, at time.Time) {
	for i := range m.statuses {
		if m.statuses[i].Server == row.Server && m.statuses[i].Backup == row.Backup && m.statuses[i].Database == row.Database {
			m.statuses[i].LastStatus = row.Status
			m.statuses[i].LastRun = at
			m.statuses[i].LastSize = row.SizeBytes
			m.statuses[i].LastError = row.Error
			m.statuses[i].LastPaths = row.StoredPaths
			m.statuses[i].LastElapsed = row.Duration
			return
		}
	}
	m.statuses = append(m.statuses, databaseStatus{
		Server:      row.Server,
		Backup:      row.Backup,
		Database:    row.Database,
		Method:      row.Method,
		Retention:   row.Retention,
		LastStatus:  row.Status,
		LastRun:     at,
		LastSize:    row.SizeBytes,
		LastError:   row.Error,
		LastPaths:   row.StoredPaths,
		LastElapsed: row.Duration,
	})
}

func (m *model) markStatusRunning(row backup.SummaryRow) {
	for i := range m.statuses {
		if m.statuses[i].Server == row.Server && m.statuses[i].Backup == row.Backup && m.statuses[i].Database == row.Database {
			m.statuses[i].LastStatus = "running"
			m.statuses[i].Method = row.Method
			return
		}
	}
	m.statuses = append(m.statuses, databaseStatus{
		Server:     row.Server,
		Backup:     row.Backup,
		Database:   row.Database,
		Method:     row.Method,
		Retention:  row.Retention,
		LastStatus: "running",
	})
}

func (m *model) recordEvent(level, message string) {
	switch level {
	case "error":
		m.logger.Error(message)
	case "warn":
		m.logger.Warn(message)
	default:
		m.logger.Info(message)
	}
	m.addActivity(level, message)
}

func (m *model) addActivity(level, message string) {
	m.events = append([]eventEntry{{
		At:      time.Now(),
		Message: message,
		Level:   level,
	}}, m.events...)
	if len(m.events) > 200 {
		m.events = m.events[:200]
	}
}

func expandDryRunStatuses(base []databaseStatus) []databaseStatus {
	if len(base) == 0 {
		return base
	}

	expanded := make([]databaseStatus, 0, len(base)*7)
	for copyIndex := 0; copyIndex < 7; copyIndex++ {
		for _, status := range base {
			cloned := status
			if copyIndex > 0 {
				cloned.Database = fmt.Sprintf("%s_preview_%02d", status.Database, copyIndex)
				cloned.Server = fmt.Sprintf("%s-sim-%02d", status.Server, copyIndex)
				cloned.LastStatus = "dry-run"
				cloned.LastError = ""
			}
			expanded = append(expanded, cloned)
		}
	}
	return expanded
}

func reconcileStatuses(previous []databaseStatus, cfg *config.Config, dryRun bool) []databaseStatus {
	byKey := make(map[string]databaseStatus, len(previous))
	for _, status := range previous {
		byKey[status.Server+"\x00"+status.Backup+"\x00"+status.Database] = status
	}

	statuses := make([]databaseStatus, 0)
	for _, item := range cfg.Filter("", "", "") {
		key := item.Server.Name + "\x00" + item.Backup.Name + "\x00" + item.Target.Name
		status, ok := byKey[key]
		if !ok {
			status = databaseStatus{
				Server:     item.Server.Name,
				Backup:     item.Backup.Name,
				Database:   item.Target.Name,
				LastStatus: "pending",
			}
		}
		status.Server = item.Server.Name
		status.Backup = item.Backup.Name
		status.Database = item.Target.Name
		status.Method = item.Backup.Engine
		status.Retention = item.Retention
		statuses = append(statuses, status)
	}

	if dryRun {
		return expandDryRunStatuses(statuses)
	}
	return statuses
}

func configModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func configSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}

func (m *model) syncViewports() {
	targetsWidth, targetsHeight, activityWidth, activityHeight := m.panelSizes()
	m.targetsVP.Width = targetsWidth
	m.targetsVP.Height = targetsHeight
	m.activityVP.Width = activityWidth
	m.activityVP.Height = activityHeight

	targetsContent, selectedLine := renderStatusesContent(*m)
	m.targetsVP.SetContent(targetsContent)
	m.ensureTargetsSelectionVisible(selectedLine)

	m.activityVP.SetContent(renderEventsContent(*m))
}

func (m *model) ensureTargetsSelectionVisible(selectedLine int) {
	if selectedLine < m.targetsVP.YOffset {
		m.targetsVP.YOffset = selectedLine
	}
	if selectedLine >= m.targetsVP.YOffset+m.targetsVP.Height {
		m.targetsVP.YOffset = selectedLine - m.targetsVP.Height + 1
	}
	if m.targetsVP.YOffset < 0 {
		m.targetsVP.YOffset = 0
	}
}

func (m model) panelSizes() (targetsWidth, targetsHeight, activityWidth, activityHeight int) {
	bodyWidth := m.width
	if bodyWidth <= 0 {
		bodyWidth = 120
	}
	bodyHeight := m.height
	if bodyHeight <= 0 {
		bodyHeight = 34
	}

	palette := tuiPalette()
	topLeftWidth, topRightWidth := topCardWidths(bodyWidth)
	topHeight := maxInt(
		lipgloss.Height(palette.panel.Width(topLeftWidth).Render(renderSchedulerCard(m, palette))),
		lipgloss.Height(palette.panel.Width(topRightWidth).Render(renderHeaderCard(m, palette))),
	)
	commandHeight := lipgloss.Height(palette.commandBar.Width(bodyWidth - 2).Render(renderCommandBar(palette)))

	availableHeight := bodyHeight - topHeight - commandHeight - 2
	if availableHeight < 10 {
		availableHeight = 10
	}

	targetsOuter := int(float64(availableHeight) * 0.58)
	if targetsOuter < 8 {
		targetsOuter = 8
	}
	activityOuter := availableHeight - targetsOuter
	if activityOuter < 6 {
		activityOuter = 6
		targetsOuter = availableHeight - activityOuter
	}

	frameHeight := palette.panel.GetVerticalFrameSize() + 1
	frameWidth := palette.panel.GetHorizontalFrameSize()
	targetsHeight = maxInt(3, targetsOuter-frameHeight)
	activityHeight = maxInt(3, activityOuter-frameHeight)
	targetsWidth = maxInt(24, bodyWidth-frameWidth-4)
	activityWidth = maxInt(24, bodyWidth-frameWidth-4)

	return targetsWidth, targetsHeight, activityWidth, activityHeight
}

func (m model) View() string {
	palette := tuiPalette()

	bodyWidth := m.width
	if bodyWidth <= 0 {
		bodyWidth = 120
	}

	topLeftWidth, topRightWidth := topCardWidths(bodyWidth)

	headerContent := renderHeaderCard(m, palette)
	schedulerContent := renderSchedulerCard(m, palette)
	topInnerHeight := maxInt(lipgloss.Height(headerContent), lipgloss.Height(schedulerContent))

	headerCard := palette.panel.Width(topLeftWidth).Height(topInnerHeight).Render(headerContent)
	schedulerCard := palette.panel.Width(topRightWidth).Height(topInnerHeight).Render(schedulerContent)

	targetsBorder := palette.panel
	activityBorder := palette.panel
	if m.focus == focusTargets {
		targetsBorder = palette.focusedPanel
	} else {
		activityBorder = palette.focusedPanel
	}

	targetsPanel := targetsBorder.Render(strings.Join([]string{
		palette.section.Render(sectionTitle("Targets", false)),
		m.targetsVP.View(),
	}, "\n"))

	activityPanel := activityBorder.Render(strings.Join([]string{
		palette.section.Render(sectionTitle("Activity", false)),
		m.activityVP.View(),
	}, "\n"))

	footer := palette.commandBar.Width(bodyWidth - 2).Render(renderCommandBar(palette))

	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, headerCard, schedulerCard),
		targetsPanel,
		activityPanel,
		footer,
	)
	if m.showPalette {
		return renderPaletteOverlay(mainView, m, palette)
	}
	if m.showDetails {
		return renderDetailsOverlay(mainView, m, palette)
	}
	return mainView
}

func sectionTitle(label string, focused bool) string {
	if focused {
		return "> " + label
	}
	return "  " + label
}

func topCardWidths(bodyWidth int) (int, int) {
	leftWidth := int(float64(bodyWidth) * 0.58)
	if leftWidth < 42 {
		leftWidth = 42
	}
	rightWidth := bodyWidth - leftWidth - 6
	if rightWidth < 34 {
		rightWidth = 34
		leftWidth = bodyWidth - rightWidth - 6
	}
	if leftWidth < 34 {
		leftWidth = 34
	}
	return leftWidth, rightWidth
}

func renderHeaderCard(m model, palette styles) string {
	clock := m.now
	if clock.IsZero() {
		clock = time.Now()
	}
	identity := lipgloss.JoinHorizontal(
		lipgloss.Center,
		palette.title.Render("babalu-vault"),
		"  ",
		palette.muted.Render(clock.Local().Format("Monday, Jan 02 2006")),
		"  ",
		palette.clock.Render(clock.Local().Format("15:04:05")),
	)
	lines := []string{
		identity,
		"",
		palette.muted.Render(fmt.Sprintf("config %s", m.configPath)),
		palette.muted.Render(fmt.Sprintf("backup root %s", m.cfg.Settings.RootDir)),
		palette.muted.Render(fmt.Sprintf("daily time %02d:%02d", m.scheduleHour, m.scheduleMin)),
		palette.muted.Render(fmt.Sprintf("dry-run %t", m.dryRun || m.cfg.Settings.DryRun)),
	}
	return strings.Join(lines, "\n")
}

func renderSchedulerCard(m model, palette styles) string {
	schedulerState := "active"
	if m.paused {
		schedulerState = "paused"
	}
	if m.running {
		schedulerState = "running " + m.currentAction
	}

	nextRun := "now"
	if !m.nextRun.IsZero() {
		nextRun = m.nextRun.Local().Format("2006-01-02 15:04:05")
	}

	statusTone := palette.good
	if m.paused {
		statusTone = palette.warn
	}
	if m.lastError != "" {
		statusTone = palette.bad
	}

	successCount, failureCount := m.lastRunCounts()

	lines := []string{
		palette.section.Render("Scheduler"),
		palette.muted.Render(strings.Repeat("─", 18)),
		fmt.Sprintf("%s %s", palette.label.Render("state"), statusTone.Render(schedulerState)),
		fmt.Sprintf("%s %s", palette.label.Render("next run"), nextRun),
		fmt.Sprintf("%s %s", palette.label.Render("last run"), formatTime(m.lastRun)),
		fmt.Sprintf("%s %d  %s %d  %s %d",
			palette.label.Render("targets"), len(m.statuses),
			palette.good.Render("success"), successCount,
			palette.bad.Render("failure"), failureCount,
		),
	}
	if m.lastError != "" {
		lines = append(lines, fmt.Sprintf("%s %s", palette.label.Render("error"), m.lastError))
	}
	return strings.Join(lines, "\n")
}

func (m model) lastRunCounts() (successCount, failureCount int) {
	if m.lastRun.IsZero() {
		return 0, 0
	}
	for _, status := range m.statuses {
		if !status.LastRun.Equal(m.lastRun) {
			continue
		}
		switch status.LastStatus {
		case "ok", "dry-run":
			successCount++
		case "error":
			failureCount++
		}
	}
	return successCount, failureCount
}

func renderCommandBar(palette styles) string {
	return strings.Join([]string{
		palette.section.Render("Commands"),
		palette.muted.Render("Press / for palette. Use Tab to switch focus between Targets and Activity. Use Up/Down, PgUp/PgDn, Home/End to scroll. Press Enter for target details. Press b for backup now. Press Esc to close overlays. Press q to quit."),
	}, "\n")
}

func renderStatusesContent(m model) (string, int) {
	lines := make([]string, 0, len(m.statuses)*2+8)
	selectedLine := 0
	for i, status := range m.statuses {
		stateStyle := m.statusStyle(status.LastStatus)
		cursor := " "
		rowStyle := lipgloss.NewStyle()
		if i == m.selected {
			cursor = ">"
			rowStyle = lipgloss.NewStyle().Bold(true)
			selectedLine = len(lines)
		}

		lines = append(lines, rowStyle.Render(fmt.Sprintf("%s %s/%s/%s [%s] %s",
			cursor,
			status.Server,
			status.Backup,
			status.Database,
			status.Method,
			stateStyle.Render(status.LastStatus),
		)))

		detail := fmt.Sprintf("  last %s  size %s  retention d%d w%d m%d",
			formatTime(status.LastRun),
			formatBytes(status.LastSize),
			status.Retention.DailyKeep,
			status.Retention.WeeklyKeep,
			status.Retention.MonthlyKeep,
		)
		if status.LastError != "" {
			detail = "  error " + status.LastError
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(detail))
	}

	selected := m.selectedStatus()
	if selected != nil && len(selected.LastPaths) > 0 {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("221")).Render("Selected Output"))
		for _, path := range selected.LastPaths {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("151")).Render(filepath.Clean(path)))
		}
	}

	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render("No configured targets"))
	}
	return strings.Join(lines, "\n"), selectedLine
}

func renderEventsContent(m model) string {
	lines := make([]string, 0, len(m.events))
	for i, entry := range m.events {
		tone := lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
		switch entry.Level {
		case "error":
			tone = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
		case "warn":
			tone = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
		case "info":
			tone = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
		}
		prefix := "  "
		if i == 0 && m.focus == focusActivity {
			prefix = "> "
		}
		lines = append(lines, prefix+tone.Render(entry.At.Format("15:04:05"))+" "+entry.Message)
	}
	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render("No events yet"))
	}
	return strings.Join(lines, "\n")
}

func (m model) statusStyle(status string) lipgloss.Style {
	switch status {
	case "error":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	case "pending", "dry-run":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case "running":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	}
}

func renderPaletteOverlay(base string, m model, palette styles) string {
	lines := []string{palette.section.Render("Command Palette")}
	for _, cmd := range commandPaletteItems() {
		lines = append(lines, fmt.Sprintf("%s  %s", palette.good.Render(cmd.Key), cmd.Label))
		lines = append(lines, palette.muted.Render("   "+cmd.Description))
	}
	lines = append(lines, "")
	lines = append(lines, palette.muted.Render("Press shortcut key to run, or / / Esc to close"))

	box := palette.overlay.Width(58).Render(strings.Join(lines, "\n"))
	w := m.width
	if w <= 0 {
		w = 120
	}
	h := m.height
	if h <= 0 {
		h = 34
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceChars(" "))
}

func renderDetailsOverlay(base string, m model, palette styles) string {
	selected := m.selectedStatus()
	if selected == nil {
		return base
	}

	lines := []string{
		palette.section.Render("Selected Target"),
		fmt.Sprintf("%s/%s/%s [%s]", selected.Server, selected.Backup, selected.Database, selected.Method),
		palette.muted.Render(fmt.Sprintf("last run %s", formatTime(selected.LastRun))),
		palette.muted.Render(fmt.Sprintf("status %s", selected.LastStatus)),
		palette.muted.Render(fmt.Sprintf("retention d%d w%d m%d", selected.Retention.DailyKeep, selected.Retention.WeeklyKeep, selected.Retention.MonthlyKeep)),
	}
	if selected.LastError != "" {
		lines = append(lines, palette.bad.Render("error "+selected.LastError))
	}
	if len(selected.LastPaths) > 0 {
		lines = append(lines, "")
		lines = append(lines, palette.section.Render("Stored Output"))
		for _, path := range selected.LastPaths {
			lines = append(lines, palette.muted.Render(filepath.Clean(path)))
		}
	}
	if item := m.selectedDatabaseConfig(); item != nil {
		if preview, err := backup.CommandPreview(*item); err == nil {
			lines = append(lines, "")
			lines = append(lines, palette.section.Render("Command Preview"))
			lines = append(lines, palette.muted.Render(preview))
		}
	}
	lines = append(lines, "")
	lines = append(lines, palette.muted.Render("Press Enter or Esc to close"))

	box := palette.overlay.Width(72).Render(strings.Join(lines, "\n"))
	w := m.width
	if w <= 0 {
		w = 120
	}
	h := m.height
	if h <= 0 {
		h = 34
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box, lipgloss.WithWhitespaceChars(" "))
}

func commandPaletteItems() []paletteCommand {
	return []paletteCommand{
		{Key: "b", Label: "Run Backup Now", Description: "Trigger an immediate backup across all configured targets"},
		{Key: "p", Label: "Pause Or Resume Scheduler", Description: "Toggle automatic scheduled backups"},
		{Key: "tab", Label: "Switch Scroll Pane", Description: "Move focus between targets and activity"},
		{Key: "q", Label: "Quit", Description: "Exit the TUI"},
	}
}

func (m model) selectedStatus() *databaseStatus {
	if m.selected < 0 || m.selected >= len(m.statuses) {
		return nil
	}
	return &m.statuses[m.selected]
}

func (m model) selectedDatabaseConfig() *config.SelectedTarget {
	selected := m.selectedStatus()
	if selected == nil {
		return nil
	}
	for _, item := range m.cfg.Filter(selected.Server, selected.Backup, selected.Database) {
		if item.Server.Name == selected.Server && item.Backup.Name == selected.Backup && item.Target.Name == selected.Database {
			copy := item
			return &copy
		}
	}
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(n)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type styles struct {
	title        lipgloss.Style
	clock        lipgloss.Style
	section      lipgloss.Style
	label        lipgloss.Style
	muted        lipgloss.Style
	panel        lipgloss.Style
	focusedPanel lipgloss.Style
	commandBar   lipgloss.Style
	good         lipgloss.Style
	warn         lipgloss.Style
	bad          lipgloss.Style
	overlay      lipgloss.Style
}

func tuiPalette() styles {
	return styles{
		title:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("25")).Padding(0, 1),
		clock:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87")),
		section:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("221")),
		label:        lipgloss.NewStyle().Foreground(lipgloss.Color("117")),
		muted:        lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		panel:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1, 2),
		focusedPanel: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("117")).Padding(1, 2),
		commandBar:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1, 2),
		good:         lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		warn:         lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		bad:          lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		overlay:      lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(lipgloss.Color("221")).Background(lipgloss.Color("236")).Padding(1, 2),
	}
}
