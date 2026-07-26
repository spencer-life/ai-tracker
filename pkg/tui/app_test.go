package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spencer-life/ai-tracker/internal/inventory"
)

type fakeRepository struct {
	summaryCalls []db.QueryFilter
	seriesCalls  []db.QueryFilter
	sessionCalls []db.QueryFilter
	breakdowns   []string
	summary      db.Summary
	coverage     db.QualityCoverage
	series       []db.SeriesPoint
	sessions     db.SessionPage
	agents       []db.BreakdownItem
	models       []db.BreakdownItem
	sync         db.SyncStatus
	err          error
}

func (f *fakeRepository) Summary(_ context.Context, filter db.QueryFilter) (db.Summary, error) {
	f.summaryCalls = append(f.summaryCalls, filter)
	if f.err != nil {
		return db.Summary{}, f.err
	}
	out := f.summary
	if filter.IncludeEstimates {
		out.Quality = f.coverage
	}
	return out, nil
}
func (f *fakeRepository) Series(_ context.Context, filter db.QueryFilter, _ string) ([]db.SeriesPoint, error) {
	f.seriesCalls = append(f.seriesCalls, filter)
	return f.series, f.err
}
func (f *fakeRepository) ListSessions(_ context.Context, filter db.QueryFilter) (db.SessionPage, error) {
	f.sessionCalls = append(f.sessionCalls, filter)
	return f.sessions, f.err
}
func (f *fakeRepository) GetSession(context.Context, string) (db.Session, error) {
	return db.Session{}, errors.New("not used")
}
func (f *fakeRepository) Breakdown(_ context.Context, _ db.QueryFilter, group string) ([]db.BreakdownItem, error) {
	f.breakdowns = append(f.breakdowns, group)
	if group == "agent" {
		return f.agents, f.err
	}
	return f.models, f.err
}
func (f *fakeRepository) LastSync(context.Context) (db.SyncStatus, error) { return f.sync, f.err }
func (f *fakeRepository) GetAgentStats() ([]db.AgentStats, error)         { return nil, errors.New("not used") }
func (f *fakeRepository) GetRecentLogs(int) ([]string, error)             { return nil, errors.New("not used") }
func (f *fakeRepository) Close() error                                    { return nil }

func TestLoadDataUsesThirtyDayMeasuredWindowAndAllQueries(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.FixedZone("Phoenix", -7*60*60))
	repo := &fakeRepository{
		coverage: db.QualityCoverage{Reported: 100, Estimated: 12},
		agents:   []db.BreakdownItem{{Key: "codex"}},
		models:   []db.BreakdownItem{{Key: "gpt"}},
		sync:     db.SyncStatus{Status: "success"},
	}
	originalScan, originalHome, originalCWD := scanInventory, userHomeDir, workingDir
	t.Cleanup(func() { scanInventory, userHomeDir, workingDir = originalScan, originalHome, originalCWD })
	userHomeDir = func() (string, error) { return "/safe/home", nil }
	workingDir = func() (string, error) { return "/safe/repo", nil }
	scanInventory = func(_ context.Context, home, cwd string) ([]inventory.Component, error) {
		if home != "/safe/home" || cwd != "/safe/repo" {
			t.Fatalf("unexpected inventory roots: %q %q", home, cwd)
		}
		return []inventory.Component{{Provider: "codex", Kind: "skill", DisplayName: "review"}}, nil
	}

	msg := loadDataCmd(repo, now, now.Location())().(loadMsg)
	if len(msg.errors) != 0 {
		t.Fatalf("unexpected load errors: %v", msg.errors)
	}
	if len(repo.summaryCalls) != 2 || repo.summaryCalls[0].IncludeEstimates || !repo.summaryCalls[1].IncludeEstimates {
		t.Fatalf("expected measured and coverage summaries, got %#v", repo.summaryCalls)
	}
	filter := repo.summaryCalls[0]
	if got := filter.To.Sub(filter.From); got != 30*24*time.Hour {
		t.Fatalf("expected trailing 30 days, got %s", got)
	}
	if filter.Timezone != now.Location() || filter.Limit != sessionLimit {
		t.Fatalf("unexpected filter: %#v", filter)
	}
	if len(repo.seriesCalls) != 1 || len(repo.sessionCalls) != 1 || strings.Join(repo.breakdowns, ",") != "agent,model" {
		t.Fatalf("not all query surfaces loaded: series=%d sessions=%d breakdowns=%v", len(repo.seriesCalls), len(repo.sessionCalls), repo.breakdowns)
	}
	if msg.quality.Estimated != 12 || len(msg.inventory) != 1 || !msg.syncKnown {
		t.Fatalf("coverage, inventory, or sync missing: %#v", msg)
	}
}

func TestTickAndManualRefreshLoadRepository(t *testing.T) {
	repo := &fakeRepository{}
	m := NewModel(repo)
	m.now = func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local) }

	updated, cmd := m.Update(TickMsg(m.now()))
	tickModel := updated.(Model)
	if !tickModel.loading || cmd == nil {
		t.Fatal("tick did not enter refreshing state and schedule work")
	}
	updated, cmd = tickModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	manualModel := updated.(Model)
	if !manualModel.loading || cmd == nil {
		t.Fatal("manual refresh did not schedule a repository load")
	}
}

func TestViewsAreTruthfulAndFitSmallTerminal(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.Local)
	finished := now.Add(-25 * time.Hour)
	cost := int64(123400)
	m := NewModel(&fakeRepository{})
	m.now = func() time.Time { return now }
	m.width, m.height, m.loading = 80, 24, false
	m.data = loadMsg{
		loadedAt: now,
		summary:  db.Summary{Sessions: 2, Events: 3, Tokens: db.TokenCounts{InputUncached: 1000, Output: 200, Total: 1200}, CostMicros: &cost},
		quality:  db.QualityCoverage{Reported: 1000, Estimated: 200},
		series: []db.SeriesPoint{
			{Start: now.AddDate(0, 0, -1), Tokens: db.TokenCounts{Total: 100}},
			{Start: now, Tokens: db.TokenCounts{Total: 1200}},
		},
		sessions:  db.SessionPage{Sessions: []db.Session{{Agent: "codex", Model: "gpt-5", UpdatedAt: now, Tokens: db.TokenCounts{Total: 1200}, Measurement: db.MeasurementReported}}},
		agents:    []db.BreakdownItem{{Key: "codex", Tokens: db.TokenCounts{Total: 1200}, Sessions: 2}},
		models:    []db.BreakdownItem{{Key: "gpt-5", Tokens: db.TokenCounts{Total: 1200}, Sessions: 2}},
		inventory: []inventory.Component{{Provider: "codex", Kind: "skill", DisplayName: "review", Scope: "global", State: inventory.StateConfiguredEnabled}},
		syncKnown: true,
		sync:      db.SyncStatus{Status: "success", FinishedAt: &finished},
	}

	var combined strings.Builder
	for tab := range tabNames {
		m.activeTab = tab
		view := m.View()
		combined.WriteString(view)
		for lineNo, line := range strings.Split(view, "\n") {
			if width := lipgloss.Width(line); width > m.width {
				t.Fatalf("tab %d line %d width %d > %d: %q", tab, lineNo, width, m.width, line)
			}
		}
		if lines := strings.Count(view, "\n") + 1; lines > m.height {
			t.Fatalf("tab %d rendered %d lines > %d", tab, lines, m.height)
		}
	}
	text := combined.String()
	for _, want := range []string{"measured usage", "Daily token usage", "Recent sessions", "Jul 23 12:00", "Agents", "Customization inventory", "stale (>24h)", "estimated 200 (excluded)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing truthful view text %q", want)
		}
	}
	for _, forbidden := range []string{"WEBSOCKET", "ACTIVE AGENTS", "AVG LATENCY", "P95", "heartbeat", "Total Jobs", "budget"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("found fabricated telemetry label %q", forbidden)
		}
	}
}

func TestInventoryOverflowNamesExecutable(t *testing.T) {
	m := NewModel(&fakeRepository{})
	m.width, m.height, m.loading, m.activeTab = 100, 12, false, 4
	for i := 0; i < 20; i++ {
		m.data.inventory = append(m.data.inventory, inventory.Component{Provider: "codex", Kind: "skill", DisplayName: "item", Scope: "global", State: inventory.StateConfiguredEnabled})
	}
	view := m.View()
	if !strings.Contains(view, "ai-tracker inventory") || strings.Contains(view, "`ait inventory`") {
		t.Fatalf("inventory overflow help names wrong executable: %s", view)
	}
}

func TestUnavailableAndEmptyStatesAreExplicit(t *testing.T) {
	m := NewModel(nil)
	m.loading = false
	m.data = loadMsg{errors: []string{"summary: database offline", "inventory: access denied"}}
	if view := m.View(); !strings.Contains(view, "Unavailable") || !strings.Contains(view, "database offline") {
		t.Fatalf("summary failure not visible: %s", view)
	}
	m.activeTab = 2
	m.data.errors = nil
	if view := m.View(); !strings.Contains(view, "No sessions") {
		t.Fatalf("empty sessions state not visible: %s", view)
	}
	m.activeTab = 4
	m.data.errors = []string{"inventory: access denied"}
	if view := m.View(); !strings.Contains(view, "access denied") {
		t.Fatalf("inventory failure not visible: %s", view)
	}
}
