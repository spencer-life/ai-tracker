package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SubagentInfo represents an active or past agent execution context.
type SubagentInfo struct {
	ID        string
	Name      string
	Model     string
	Status    string
	Tokens    int64
	LatencyMs int
	Duration  string
	Task      string
}

// TickMsg triggers periodic UI telemetry refresh.
type TickMsg time.Time

// Model is the Bubbletea application state.
type Model struct {
	activeTab     int
	width         int
	height        int
	agents        []SubagentInfo
	logs          []string
	selectedAgent int
	totalTokens   int64
	totalCost     float64
	avgLatency    int
	connected     bool
}

// NewModel creates a pre-populated Catppuccin Frappe TUI Model.
func NewModel() Model {
	return Model{
		activeTab: 0,
		width:     80,
		height:    24,
		connected: true,
		totalTokens: 4892150,
		totalCost:   18.42,
		avgLatency:  412,
		agents: []SubagentInfo{
			{
				ID:        "conv-8f92a",
				Name:      "Codebase Researcher",
				Model:     "claude-3-7-sonnet",
				Status:    "RUNNING",
				Tokens:    142800,
				LatencyMs: 380,
				Duration:  "2m 14s",
				Task:      "Parsing AST patterns and repository dependencies across 42 files",
			},
			{
				ID:        "conv-3a17c",
				Name:      "TUI Component Renderer",
				Model:     "gemini-2.5-pro",
				Status:    "RUNNING",
				Tokens:    89400,
				LatencyMs: 290,
				Duration:  "1m 05s",
				Task:      "Building Bubbletea views & Lipgloss Catppuccin color bindings",
			},
			{
				ID:        "conv-92b4e",
				Name:      "API Schema Validator",
				Model:     "gpt-4o",
				Status:    "QUEUED",
				Tokens:    12100,
				LatencyMs: 450,
				Duration:  "12s",
				Task:      "Validating WebSocket JSON telemetry schemas against Go structs",
			},
			{
				ID:        "conv-11a9f",
				Name:      "DB Migration Assister",
				Model:     "claude-3-7-sonnet",
				Status:    "RUNNING",
				Tokens:    210400,
				LatencyMs: 510,
				Duration:  "4m 50s",
				Task:      "Analyzing Postgres index performance for telemetry logs table",
			},
		},
		logs: []string{
			"[19:02:20] [SYSTEM] Telemetry engine initialized on port :8080 (Catppuccin Frappe mode)",
			"[19:02:22] [AGENT:conv-8f92a] Tool call grep_search query='Bubbletea' path='/home/mlpc/dev/ai-tracker'",
			"[19:02:24] [WEBSOCKET] Client subscriber connected from 127.0.0.1:49210",
			"[19:02:25] [METRICS] Token usage sync completed: +3,420 tokens ($0.012 est.)",
			"[19:02:28] [TUI] View state active tab set to [1: Overview]",
			"[19:02:30] [AGENT:conv-3a17c] Lipgloss styles compiled with Catppuccin Frappe colors",
		},
	}
}

func tickEvery() tea.Cmd {
	return tea.Every(time.Second*3, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Init initializes background commands.
func (m Model) Init() tea.Cmd {
	return tickEvery()
}

// Update handles messages and input events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case TickMsg:
		m.totalTokens += int64(120 + time.Now().Second()*5)
		m.logs = append(m.logs, fmt.Sprintf("[%s] [WEBSOCKET] Telemetry sync heartbeat OK", time.Now().Format("15:04:05")))
		if len(m.logs) > 50 {
			m.logs = m.logs[1:]
		}
		return m, tickEvery()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "l":
			m.activeTab = (m.activeTab + 1) % 4
		case "shift+tab", "left", "h":
			m.activeTab = (m.activeTab + 3) % 4
		case "1":
			m.activeTab = 0
		case "2":
			m.activeTab = 1
		case "3":
			m.activeTab = 2
		case "4":
			m.activeTab = 3
		case "j", "down":
			if m.selectedAgent < len(m.agents)-1 {
				m.selectedAgent++
			}
		case "k", "up":
			if m.selectedAgent > 0 {
				m.selectedAgent--
			}
		case "r":
			m.logs = append(m.logs, fmt.Sprintf("[%s] [USER] Manual refresh triggered", time.Now().Format("15:04:05")))
		}
	}
	return m, nil
}

// View renders the TUI layout.
func (m Model) View() string {
	var b strings.Builder

	// Top Banner & Tabs
	b.WriteString(m.renderHeader())
	b.WriteString("\n\n")

	// Main View Content depending on active tab
	switch m.activeTab {
	case 0:
		b.WriteString(m.renderOverviewTab())
	case 1:
		b.WriteString(m.renderAgentsTab())
	case 2:
		b.WriteString(m.renderLogsTab())
	case 3:
		b.WriteString(m.renderSettingsTab())
	}

	b.WriteString("\n\n")
	// Bottom Status Bar
	b.WriteString(m.renderFooter())

	return b.String()
}

func (m Model) renderHeader() string {
	title := TitleStyle.Render(" ⚡ AI TRACKER TUI ")
	sub := lipgloss.NewStyle().Foreground(ColorSubtext0).Render(" (Catppuccin Frappe) ")

	tabs := []string{"[1] Overview", "[2] Active Agents", "[3] Live Stream", "[4] Settings"}
	var tabRenders []string

	for i, tab := range tabs {
		if i == m.activeTab {
			tabRenders = append(tabRenders, ActiveTabStyle.Render(tab))
		} else {
			tabRenders = append(tabRenders, InactiveTabStyle.Render(tab))
		}
	}

	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabRenders...)
	return lipgloss.JoinHorizontal(lipgloss.Center, title, sub) + "\n\n" + tabBar
}

func (m Model) renderOverviewTab() string {
	kpi1 := CardStyle.Render(fmt.Sprintf("%s\n%s\n%s",
		MetricLabelStyle.Render("TOTAL TOKENS"),
		MetricValStyle.Render(fmt.Sprintf("%d", m.totalTokens)),
		lipgloss.NewStyle().Foreground(ColorOverlay1).Render("Prompt: 3.1M | Comp: 1.7M"),
	))

	kpi2 := CardStyle.Render(fmt.Sprintf("%s\n%s\n%s",
		MetricLabelStyle.Render("EST. COST"),
		lipgloss.NewStyle().Foreground(ColorPeach).Bold(true).Render(fmt.Sprintf("$%.2f", m.totalCost)),
		lipgloss.NewStyle().Foreground(ColorOverlay1).Render("Avg: $0.0037 / req"),
	))

	kpi3 := CardStyle.Render(fmt.Sprintf("%s\n%s\n%s",
		MetricLabelStyle.Render("ACTIVE AGENTS"),
		lipgloss.NewStyle().Foreground(ColorBlue).Bold(true).Render(fmt.Sprintf("%d Running", len(m.agents))),
		lipgloss.NewStyle().Foreground(ColorOverlay1).Render("12 Total Jobs Executed"),
	))

	kpi4 := CardStyle.Render(fmt.Sprintf("%s\n%s\n%s",
		MetricLabelStyle.Render("AVG LATENCY"),
		lipgloss.NewStyle().Foreground(ColorTeal).Bold(true).Render(fmt.Sprintf("%d ms", m.avgLatency)),
		lipgloss.NewStyle().Foreground(ColorOverlay1).Render("P95: 780ms"),
	))

	row1 := lipgloss.JoinHorizontal(lipgloss.Top, kpi1, "  ", kpi2, "  ", kpi3, "  ", kpi4)

	// Distribution breakdown
	distTitle := HeaderStyle.Render("Model Cost & Usage Breakdown:")
	anthropicBar := lipgloss.NewStyle().Foreground(ColorMauve).Render("■ Anthropic (Claude 3.7): 65% ($11.97)")
	openaiBar := lipgloss.NewStyle().Foreground(ColorTeal).Render("■ OpenAI (GPT-4o):       23% ($4.23)")
	googleBar := lipgloss.NewStyle().Foreground(ColorBlue).Render("■ Google (Gemini 2.5):    12% ($2.22)")

	distBox := CardStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n%s", distTitle, anthropicBar, openaiBar, googleBar))

	return row1 + "\n\n" + distBox
}

func (m Model) renderAgentsTab() string {
	var rows []string
	rows = append(rows, HeaderStyle.Render("Active Subagent Execution Sessions (j/k to select):"))
	rows = append(rows, "")

	for i, a := range m.agents {
		statusBadge := BadgeRunning.Render(a.Status)
		if a.Status == "QUEUED" {
			statusBadge = BadgeQueued.Render(a.Status)
		}

		line := fmt.Sprintf("%-22s %s  Model: %-18s Tokens: %-8d Latency: %dms",
			a.Name, statusBadge, a.Model, a.Tokens, a.LatencyMs)

		if i == m.selectedAgent {
			rows = append(rows, ActiveCardStyle.Render(fmt.Sprintf("👉 %s\n   Task: %s (id: %s, duration: %s)", line, a.Task, a.ID, a.Duration)))
		} else {
			rows = append(rows, CardStyle.Render(fmt.Sprintf("   %s\n   Task: %s", line, a.Task)))
		}
	}

	return strings.Join(rows, "\n")
}

func (m Model) renderLogsTab() string {
	var b strings.Builder
	b.WriteString(HeaderStyle.Render("Live Telemetry Event Log Stream:"))
	b.WriteString("\n\n")

	start := 0
	if len(m.logs) > 10 {
		start = len(m.logs) - 10
	}

	for _, l := range m.logs[start:] {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorText).Render(l))
		b.WriteString("\n")
	}

	return CardStyle.Render(b.String())
}

func (m Model) renderSettingsTab() string {
	info := fmt.Sprintf(`%s

  Web Dashboard URL:  %s
  WebSocket Endpoint: %s
  Telemetry Storage:  %s
  Theme Palette:      %s

  To view the web interface:
  Open http://localhost:8080/ in your browser.`,
		HeaderStyle.Render("System Configuration & Scaffolding"),
		lipgloss.NewStyle().Foreground(ColorGreen).Render("http://localhost:8080/"),
		lipgloss.NewStyle().Foreground(ColorMauve).Render("ws://localhost:8080/ws/telemetry"),
		lipgloss.NewStyle().Foreground(ColorSubtext0).Render("Memory / SQLite Scaffolding"),
		lipgloss.NewStyle().Foreground(ColorMauve).Bold(true).Render("Catppuccin Frappe"),
	)

	return CardStyle.Render(info)
}

func (m Model) renderFooter() string {
	conn := lipgloss.NewStyle().Foreground(ColorGreen).Render("● LIVE SYNC")
	help := HelpStyle.Render("1-4: Switch Tabs | Tab/Shift+Tab: Cycle | j/k: Select Agent | r: Refresh | q: Quit")
	return StatusBarStyle.Render(fmt.Sprintf(" %s | %s ", conn, help))
}
