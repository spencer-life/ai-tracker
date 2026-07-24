package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spencer-life/ai-tracker/internal/inventory"
)

const (
	refreshInterval = 15 * time.Second
	sessionLimit    = 25
)

var tabNames = []string{"Overview", "Trends", "Sessions", "Agents/Models", "Inventory", "Data Health"}

var (
	scanInventory = inventory.Scan
	userHomeDir   = os.UserHomeDir
	workingDir    = os.Getwd
)

type TickMsg time.Time

type loadMsg struct {
	loadedAt  time.Time
	summary   db.Summary
	quality   db.QualityCoverage
	series    []db.SeriesPoint
	sessions  db.SessionPage
	agents    []db.BreakdownItem
	models    []db.BreakdownItem
	inventory []inventory.Component
	sync      db.SyncStatus
	syncKnown bool
	errors    []string
}

// Model is a read-only view over one Repository. The command which creates the
// repository owns its lifetime.
type Model struct {
	repo      db.Repository
	location  *time.Location
	now       func() time.Time
	activeTab int
	width     int
	height    int
	loading   bool
	data      loadMsg
}

// NewModel creates a TUI using the caller-owned repository. All usage views
// default to the trailing 30 days in the process' local timezone.
func NewModel(repo db.Repository) Model {
	return Model{repo: repo, location: time.Local, now: time.Now, width: 80, height: 24, loading: true}
}

func tickEvery() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return TickMsg(t) })
}

func loadDataCmd(repo db.Repository, now time.Time, loc *time.Location) tea.Cmd {
	return func() tea.Msg {
		if repo == nil {
			return loadMsg{loadedAt: now, errors: []string{"repository unavailable"}}
		}
		localNow := now.In(loc)
		dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
		to := dayStart.AddDate(0, 0, 1)
		filter := db.QueryFilter{From: dayStart.AddDate(0, 0, -29), To: to, Timezone: loc, Limit: sessionLimit}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		out := loadMsg{loadedAt: now}
		var err error
		out.summary, err = repo.Summary(ctx, filter)
		if err != nil {
			out.errors = append(out.errors, "summary: "+err.Error())
		}
		// Normal totals exclude estimates; Data Health still discloses them.
		coverageFilter := filter
		coverageFilter.IncludeEstimates = true
		coverage, coverageErr := repo.Summary(ctx, coverageFilter)
		if coverageErr != nil {
			out.errors = append(out.errors, "quality coverage: "+coverageErr.Error())
		} else {
			out.quality = coverage.Quality
		}
		out.series, err = repo.Series(ctx, filter, "day")
		if err != nil {
			out.errors = append(out.errors, "trends: "+err.Error())
		}
		out.sessions, err = repo.ListSessions(ctx, filter)
		if err != nil {
			out.errors = append(out.errors, "sessions: "+err.Error())
		}
		out.agents, err = repo.Breakdown(ctx, filter, "agent")
		if err != nil {
			out.errors = append(out.errors, "agent breakdown: "+err.Error())
		}
		out.models, err = repo.Breakdown(ctx, filter, "model")
		if err != nil {
			out.errors = append(out.errors, "model breakdown: "+err.Error())
		}
		out.sync, err = repo.LastSync(ctx)
		switch {
		case err == nil:
			out.syncKnown = out.sync.Status != "never"
		case errors.Is(err, sql.ErrNoRows):
			// An empty sync history is a valid, explicitly rendered state.
		default:
			out.errors = append(out.errors, "sync status: "+err.Error())
		}
		home, homeErr := userHomeDir()
		cwd, cwdErr := workingDir()
		if homeErr != nil || cwdErr != nil {
			out.errors = append(out.errors, "inventory: local roots unavailable")
		} else {
			out.inventory, err = scanInventory(ctx, home, cwd)
			if err != nil {
				out.errors = append(out.errors, "inventory: "+err.Error())
			}
		}
		return out
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickEvery(), loadDataCmd(m.repo, m.now(), m.location))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(40, msg.Width)
		m.height = max(12, msg.Height)
	case TickMsg:
		m.loading = true
		return m, tea.Batch(tickEvery(), loadDataCmd(m.repo, m.now(), m.location))
	case loadMsg:
		m.data = msg
		m.loading = false
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.activeTab = (m.activeTab + 1) % len(tabNames)
		case "shift+tab", "left", "h":
			m.activeTab = (m.activeTab + len(tabNames) - 1) % len(tabNames)
		case "1", "2", "3", "4", "5", "6":
			m.activeTab = int(msg.String()[0] - '1')
		case "r":
			m.loading = true
			return m, loadDataCmd(m.repo, m.now(), m.location)
		}
	}
	return m, nil
}

func (m Model) View() string {
	var content string
	switch m.activeTab {
	case 0:
		content = m.renderOverview()
	case 1:
		content = m.renderTrends()
	case 2:
		content = m.renderSessions()
	case 3:
		content = m.renderBreakdowns()
	case 4:
		content = m.renderInventory()
	case 5:
		content = m.renderHealth()
	}
	header, footer := m.renderHeader(), m.renderFooter()
	reserved := strings.Count(header, "\n") + 2 // header lines plus footer
	content = fitHeight(content, max(1, m.height-reserved))
	return header + "\n" + content + "\n" + footer
}

func (m Model) renderHeader() string {
	title := TitleStyle.Render("AI TRACKER")
	rangeText := HelpStyle.Render("30d · " + m.location.String())
	parts := make([]string, 0, len(tabNames))
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		style := InactiveTabStyle.Padding(0, 1)
		if i == m.activeTab {
			style = ActiveTabStyle.Padding(0, 1)
		}
		parts = append(parts, style.Render(label))
	}
	tabs := strings.Join(parts, " ")
	if lipgloss.Width(tabs) > m.width {
		parts = parts[:0]
		for i, name := range tabNames {
			label := fmt.Sprintf("%d %s", i+1, shortTab(name))
			style := InactiveTabStyle.Padding(0, 0)
			if i == m.activeTab {
				style = ActiveTabStyle.Padding(0, 0)
			}
			parts = append(parts, style.Render(label))
		}
		tabs = strings.Join(parts, " ")
	}
	return fitLine(title+" "+rangeText, m.width) + "\n" + fitLine(tabs, m.width)
}

func shortTab(name string) string {
	switch name {
	case "Agents/Models":
		return "A/M"
	case "Data Health":
		return "Health"
	default:
		return name
	}
}

func (m Model) renderOverview() string {
	if err := m.errorFor("summary:"); err != "" {
		return unavailable("Overview", err)
	}
	s := m.data.summary
	cost := "unavailable"
	if s.CostMicros != nil {
		cost = fmt.Sprintf("$%.4f known", float64(*s.CostMicros)/1e6)
	}
	lines := []string{
		HeaderStyle.Render("Overview · measured usage (estimates excluded)"),
		fmt.Sprintf("Sessions  %s    Events  %s", number(s.Sessions), number(s.Events)),
		fmt.Sprintf("Tokens    %s    API-equiv cost  %s", number(s.Tokens.Total), cost),
		fmt.Sprintf("Input     %s    Output          %s", number(s.Tokens.InputUncached), number(s.Tokens.Output)),
		fmt.Sprintf("Cache read %s   Cache write     %s", number(s.Tokens.CacheRead), number(s.Tokens.CacheWrite)),
		fmt.Sprintf("Reasoning %s", number(s.Tokens.Reasoning)),
		"", HeaderStyle.Render("Measurement coverage"), qualityLine(m.data.quality),
	}
	if s.LastSuccessfulSync == nil {
		lines = append(lines, ColorStyle(ColorYellow).Render("Last successful sync unavailable"))
	} else {
		lines = append(lines, "Last successful sync  "+s.LastSuccessfulSync.In(m.location).Format("2006-01-02 15:04 MST"))
	}
	return fitLines(lines, m.width)
}

func (m Model) renderTrends() string {
	if err := m.errorFor("trends:"); err != "" {
		return unavailable("Trends", err)
	}
	if len(m.data.series) == 0 {
		return empty("Trends", "No daily usage in this 30-day window.")
	}
	values := make([]int64, len(m.data.series))
	for i, point := range m.data.series {
		values[i] = point.Tokens.Total
	}
	lines := []string{HeaderStyle.Render("Daily token usage · estimates excluded"), sparkline(values, max(10, min(m.width-2, 60)))}
	start := max(0, len(m.data.series)-min(8, max(2, m.height-9)))
	peak := maxInt64(values)
	for _, point := range m.data.series[start:] {
		barWidth := max(1, min(24, m.width-31))
		lines = append(lines, fmt.Sprintf("%s  %-*s %s", point.Start.In(m.location).Format("Jan 02"), barWidth, bar(point.Tokens.Total, peak, barWidth), number(point.Tokens.Total)))
	}
	return fitLines(lines, m.width)
}

func (m Model) renderSessions() string {
	if err := m.errorFor("sessions:"); err != "" {
		return unavailable("Sessions", err)
	}
	if len(m.data.sessions.Sessions) == 0 {
		return empty("Sessions", "No sessions in this 30-day window.")
	}
	lines := []string{HeaderStyle.Render("Recent sessions · newest first")}
	limit := min(len(m.data.sessions.Sessions), max(2, m.height-7))
	for _, session := range m.data.sessions.Sessions[:limit] {
		kind := session.Agent
		if session.IsSubagent {
			kind += "/subagent"
		}
		model := session.Model
		if model == "" {
			model = "unknown model"
		}
		lines = append(lines, fmt.Sprintf("%s  %-15s %-20s %10s  %s", session.UpdatedAt.In(m.location).Format("Jul 23 15:04"), kind, model, number(session.Tokens.Total), measurement(session.Measurement)))
	}
	if m.data.sessions.NextCursor != "" {
		lines = append(lines, HelpStyle.Render("More sessions available; use CLI/API for pagination."))
	}
	return fitLines(lines, m.width)
}

func (m Model) renderBreakdowns() string {
	if err := m.errorFor("breakdown:"); err != "" {
		return unavailable("Agents / Models", err)
	}
	if len(m.data.agents) == 0 && len(m.data.models) == 0 {
		return empty("Agents / Models", "No measured usage to break down.")
	}
	lines := []string{HeaderStyle.Render("Agents")}
	lines = append(lines, breakdownLines(m.data.agents, max(2, min(6, (m.height-8)/2)))...)
	lines = append(lines, "", HeaderStyle.Render("Models"))
	lines = append(lines, breakdownLines(m.data.models, max(2, min(6, (m.height-8)/2)))...)
	return fitLines(lines, m.width)
}

func (m Model) renderInventory() string {
	if err := m.errorFor("inventory:"); err != "" {
		return unavailable("Inventory", err)
	}
	if len(m.data.inventory) == 0 {
		return empty("Inventory", "No supported customizations discovered.")
	}
	lines := []string{HeaderStyle.Render("Customization inventory · privacy-safe metadata")}
	limit := min(len(m.data.inventory), max(2, m.height-7))
	for _, item := range m.data.inventory[:limit] {
		lines = append(lines, fmt.Sprintf("%-7s %-13s %-22s %-10s %s", item.Provider, item.Kind, item.DisplayName, item.Scope, item.State))
	}
	if len(m.data.inventory) > limit {
		lines = append(lines, HelpStyle.Render(fmt.Sprintf("%d more items; use `ait inventory` for the complete list.", len(m.data.inventory)-limit)))
	}
	return fitLines(lines, m.width)
}

func breakdownLines(items []db.BreakdownItem, limit int) []string {
	if len(items) == 0 {
		return []string{"Unavailable or empty"}
	}
	items = append([]db.BreakdownItem(nil), items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Tokens.Total > items[j].Tokens.Total })
	limit = min(limit, len(items))
	lines := make([]string, 0, limit)
	for _, item := range items[:limit] {
		key := item.Key
		if key == "" {
			key = "unknown"
		}
		lines = append(lines, fmt.Sprintf("%-24s %12s tokens  %4d sessions", key, number(item.Tokens.Total), item.Sessions))
	}
	return lines
}

func (m Model) renderHealth() string {
	lines := []string{HeaderStyle.Render("Data Health"), "Coverage  " + qualityLine(m.data.quality)}
	if !m.data.syncKnown {
		lines = append(lines, ColorStyle(ColorYellow).Render("Sync status unavailable: no sync run has been recorded."))
	} else {
		s := m.data.sync
		finished := "in progress"
		if s.FinishedAt != nil {
			finished = s.FinishedAt.In(m.location).Format("2006-01-02 15:04 MST")
		}
		status := fmt.Sprintf("Last sync  %s · %s", s.Status, finished)
		if s.Status == "success" && s.FinishedAt != nil && m.now().Sub(*s.FinishedAt) > 24*time.Hour {
			status += " · stale (>24h)"
		}
		lines = append(lines, status, fmt.Sprintf("Inserted %d  Updated %d  Skipped %d  Errors %d", s.Inserted, s.Updated, s.Skipped, s.Errors))
		for _, diagnostic := range s.Diagnostics[:min(len(s.Diagnostics), max(0, m.height-11))] {
			lines = append(lines, "- "+diagnostic)
		}
	}
	if len(m.data.errors) > 0 {
		lines = append(lines, "", ColorStyle(ColorRed).Render("Unavailable datasets"))
		lines = append(lines, m.data.errors...)
	} else if !m.data.loadedAt.IsZero() {
		lines = append(lines, "", "Snapshot loaded "+m.data.loadedAt.In(m.location).Format("15:04:05 MST"))
	}
	return fitLines(lines, m.width)
}

func (m Model) renderFooter() string {
	state := "ready"
	if m.loading {
		state = "refreshing"
	} else if len(m.data.errors) > 0 {
		state = "degraded"
	}
	return StatusBarStyle.Width(max(0, m.width-2)).Render(fmt.Sprintf("%s · 1-6 switch · ←/→ cycle · r refresh · q quit", state))
}

func (m Model) errorFor(prefix string) string {
	for _, item := range m.data.errors {
		if strings.Contains(item, prefix) {
			return item
		}
	}
	return ""
}

func empty(title, detail string) string {
	return HeaderStyle.Render(title) + "\n" + HelpStyle.Render(detail)
}
func unavailable(title, detail string) string {
	return HeaderStyle.Render(title) + "\n" + ColorStyle(ColorRed).Render("Unavailable: "+detail)
}
func ColorStyle(color lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(color) }

func qualityLine(q db.QualityCoverage) string {
	return fmt.Sprintf("reported %s · derived %s · estimated %s (excluded) · legacy %s", number(q.Reported), number(q.Derived), number(q.Estimated), number(q.Legacy))
}
func measurement(m db.Measurement) string {
	if m == "" {
		return "unknown quality"
	}
	return string(m)
}
func number(v int64) string {
	negative := v < 0
	if negative {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if negative {
		return "-" + s
	}
	return s
}

func sparkline(values []int64, width int) string {
	if len(values) == 0 || width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	peak := maxInt64(values)
	var b strings.Builder
	for _, value := range values {
		level := 0
		if peak > 0 {
			level = int(value * 7 / peak)
		}
		b.WriteRune(levels[level])
	}
	return ColorStyle(ColorTeal).Render(b.String())
}
func bar(value, peak int64, width int) string {
	if peak <= 0 || value <= 0 || width <= 0 {
		return ""
	}
	n := max(1, int(value*int64(width)/peak))
	return strings.Repeat("█", min(n, width))
}
func maxInt64(values []int64) int64 {
	var out int64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}
func fitLines(lines []string, width int) string {
	for i := range lines {
		lines[i] = fitLine(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
func fitLine(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, max(1, width), "…")
}
func fitHeight(s string, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}
