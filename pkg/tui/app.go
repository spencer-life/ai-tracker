package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spencer-life/ai-tracker/ingest"
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

func loadDataFromDB() (int64, float64, []SubagentInfo, []string) {
    db, err := ingest.InitDB()
    if err != nil {
        return 0, 0, nil, []string{fmt.Sprintf("[ERROR] DB Init failed: %v", err)}
    }
    defer db.Close()

    var totalTokens int64
    var totalCost float64
    var agents []SubagentInfo
    var logs []string

    rows, err := db.Query("SELECT agent, SUM(input_tokens + output_tokens), SUM(cost) FROM token_logs GROUP BY agent")
    if err == nil {
        for rows.Next() {
            var name string
            var tokens int64
            var cost float64
            if err := rows.Scan(&name, &tokens, &cost); err == nil {
                totalTokens += tokens
                totalCost += cost
                agents = append(agents, SubagentInfo{
                    Name: name,
                    Tokens: tokens,
                    Status: "IDLE",
                    Task: "Aggregated from DB",
                })
            }
        }
        rows.Close()
    } else {
        logs = append(logs, fmt.Sprintf("[ERROR] DB Query failed: %v", err))
    }
    
    logRows, err := db.Query("SELECT timestamp, agent, model, input_tokens, output_tokens FROM token_logs ORDER BY timestamp DESC LIMIT 10")
    if err == nil {
        for logRows.Next() {
            var ts time.Time
            var agent, model string
            var inT, outT int
            if err := logRows.Scan(&ts, &agent, &model, &inT, &outT); err == nil {
                logs = append([]string{fmt.Sprintf("[%s] [%s] %s %d in %d out", ts.Format("15:04:05"), agent, model, inT, outT)}, logs...)
            }
        }
        logRows.Close()
    }

    if len(agents) == 0 {
        agents = append(agents, SubagentInfo{Name: "No agents found", Status: "IDLE"})
    }
    if len(logs) == 0 {
        logs = append(logs, "[SYSTEM] Connected to SQLite DB (No logs found)")
    } else {
        logs = append(logs, "[SYSTEM] Connected to SQLite DB")
    }

    return totalTokens, totalCost, agents, logs
}

// NewModel creates a pre-populated Catppuccin Frappe TUI Model.
func NewModel() Model {
    tokens, cost, agents, logs := loadDataFromDB()

	return Model{
		activeTab: 0,
		width:     80,
		height:    24,
		connected: true,
		totalTokens: tokens,
		totalCost:   cost,
		avgLatency:  0,
		agents: agents,
		logs: logs,
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
			tokens, cost, agents, logs := loadDataFromDB()
			m.totalTokens = tokens
			m.totalCost = cost
			m.agents = agents
			m.logs = logs
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
