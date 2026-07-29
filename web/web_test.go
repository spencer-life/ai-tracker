package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spencer-life/ai-tracker/internal/inventory"
)

type fakeRepository struct {
	filter      db.QueryFilter
	interval    string
	dimension   string
	summary     db.Summary
	series      []db.SeriesPoint
	page        db.SessionPage
	session     db.Session
	breakdown   []db.BreakdownItem
	syncStatus  db.SyncStatus
	err         error
	requestedID string
}

func (f *fakeRepository) Summary(_ context.Context, filter db.QueryFilter) (db.Summary, error) {
	f.filter = filter
	return f.summary, f.err
}
func (f *fakeRepository) Series(_ context.Context, filter db.QueryFilter, interval string) ([]db.SeriesPoint, error) {
	f.filter, f.interval = filter, interval
	return f.series, f.err
}
func (f *fakeRepository) ListSessions(_ context.Context, filter db.QueryFilter) (db.SessionPage, error) {
	f.filter = filter
	return f.page, f.err
}
func (f *fakeRepository) GetSession(_ context.Context, id string) (db.Session, error) {
	f.requestedID = id
	return f.session, f.err
}
func (f *fakeRepository) Breakdown(_ context.Context, filter db.QueryFilter, dimension string) ([]db.BreakdownItem, error) {
	f.filter, f.dimension = filter, dimension
	return f.breakdown, f.err
}
func (f *fakeRepository) LastSync(context.Context) (db.SyncStatus, error) { return f.syncStatus, f.err }
func (f *fakeRepository) GetAgentStats() ([]db.AgentStats, error)         { return nil, nil }
func (f *fakeRepository) GetRecentLogs(int) ([]string, error)             { return nil, nil }
func (f *fakeRepository) Close() error                                    { return nil }

func request(t *testing.T, handler http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8080"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestValidateLoopbackAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "localhost:0", "[::1]:9000"} {
		if err := validateLoopbackAddr(addr); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	for _, addr := range []string{":8080", "0.0.0.0:8080", "192.168.1.2:8080", "missing-port"} {
		if err := validateLoopbackAddr(addr); err == nil {
			t.Errorf("expected %s to be rejected", addr)
		}
	}
}

func TestSummaryParsesConsistentFilters(t *testing.T) {
	repo := &fakeRepository{summary: db.Summary{Sessions: 2}}
	recorder := request(t, newHandler(repo, nil), http.MethodGet, "/api/v2/summary?range=custom&from=2026-07-01&to=2026-07-07&tz=America%2FPhoenix&agent=codex&provider=openai&model=gpt-5&project=tracker&quality=reported&includeEstimates=true&limit=25&cursor=abc")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	if repo.filter.Timezone.String() != "America/Phoenix" || repo.filter.Agent != "codex" || repo.filter.Provider != "openai" || repo.filter.Model != "gpt-5" || repo.filter.Project != "tracker" || repo.filter.Quality != db.MeasurementReported || !repo.filter.IncludeEstimates || repo.filter.Limit != 25 || repo.filter.Cursor != "abc" {
		t.Fatalf("unexpected filter: %+v", repo.filter)
	}
	if got := repo.filter.To.Sub(repo.filter.From); got != 6*24*time.Hour {
		t.Fatalf("custom range should use a half-open end boundary, got %s", got)
	}
	if recorder.Header().Get("Content-Security-Policy") == "" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}

func TestInvalidFiltersAreStructured(t *testing.T) {
	for _, target := range []string{
		"/api/v2/summary?tz=Nope/Nowhere", "/api/v2/summary?range=custom&from=2026-07-02&to=2026-07-01&tz=UTC",
		"/api/v2/summary?includeEstimates=maybe", "/api/v2/summary?limit=201",
	} {
		recorder := request(t, newHandler(&fakeRepository{}, nil), http.MethodGet, target)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", target, recorder.Code)
		}
		var body apiError
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error.Code != "invalid_filter" {
			t.Errorf("%s: invalid error body %s", target, recorder.Body.String())
		}
	}
}

func TestFromAndToImplyCustomRange(t *testing.T) {
	repo := &fakeRepository{}
	recorder := request(t, newHandler(repo, nil), http.MethodGet, "/api/v2/summary?from=2026-07-01&to=2026-07-02&tz=UTC")
	if recorder.Code != http.StatusOK || !repo.filter.From.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) || !repo.filter.To.Equal(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("status=%d from=%s to=%s", recorder.Code, repo.filter.From, repo.filter.To)
	}
}

func TestCollectionResponsesUseExplicitArrays(t *testing.T) {
	originalScan, originalHome, originalCWD := scanInventory, userHomeDir, workingDir
	t.Cleanup(func() { scanInventory, userHomeDir, workingDir = originalScan, originalHome, originalCWD })
	userHomeDir = func() (string, error) { return "/safe/home", nil }
	workingDir = func() (string, error) { return "/safe/repo", nil }
	scanInventory = func(context.Context, string, string) ([]inventory.Component, error) { return nil, nil }
	handler := newHandler(&fakeRepository{}, nil)
	cases := []struct{ target, fragment string }{
		{"/api/v2/series?tz=UTC", `"points":[]`},
		{"/api/v2/sessions?tz=UTC", `"sessions":[]`},
		{"/api/v2/breakdowns?tz=UTC", `"items":[]`},
		{"/api/v2/sync/status", `"diagnostics":[]`},
		{"/api/v2/inventory", `"items":[]`},
	}
	for _, tc := range cases {
		recorder := request(t, handler, http.MethodGet, tc.target)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), tc.fragment) {
			t.Errorf("%s: %d %s", tc.target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDimensionsAndIntervalsAreValidated(t *testing.T) {
	repo := &fakeRepository{}
	handler := newHandler(repo, nil)
	if got := request(t, handler, http.MethodGet, "/api/v2/series?tz=UTC&interval=hour"); got.Code != http.StatusBadRequest {
		t.Fatalf("interval status %d", got.Code)
	}
	if got := request(t, handler, http.MethodGet, "/api/v2/breakdowns?tz=UTC&dimension=secret"); got.Code != http.StatusBadRequest {
		t.Fatalf("dimension status %d", got.Code)
	}
	request(t, handler, http.MethodGet, "/api/v2/series?tz=UTC&interval=week")
	request(t, handler, http.MethodGet, "/api/v2/breakdowns?tz=UTC&dimension=model")
	if repo.interval != "week" || repo.dimension != "model" {
		t.Fatalf("interval=%q dimension=%q", repo.interval, repo.dimension)
	}
}

func TestSessionDetailAndRepositoryErrors(t *testing.T) {
	repo := &fakeRepository{session: db.Session{ID: "session/one"}}
	handler := newHandler(repo, nil)
	recorder := request(t, handler, http.MethodGet, "/api/v2/sessions/session%2Fone")
	if recorder.Code != http.StatusOK || repo.requestedID != "session/one" {
		t.Fatalf("status=%d id=%q", recorder.Code, repo.requestedID)
	}
	repo.err = errors.New("session not found")
	recorder = request(t, handler, http.MethodGet, "/api/v2/sessions/missing")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSyncRequiresSameOriginAndPublishesCommittedStatus(t *testing.T) {
	called := 0
	repo := &fakeRepository{syncStatus: db.SyncStatus{ID: 9, Status: "completed"}}
	handler := newHandler(repo, func(context.Context) error { called++; return nil })
	req := httptest.NewRequest(http.MethodPost, "/api/v2/sync", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("cross-origin status=%d called=%d", recorder.Code, called)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v2/sync", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "https://127.0.0.1:8080")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden || called != 0 {
		t.Fatalf("cross-scheme status=%d called=%d", recorder.Code, called)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v2/sync", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || called != 1 || !strings.Contains(recorder.Body.String(), `"status":"completed"`) {
		t.Fatalf("same-origin status=%d called=%d body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestAPIRouteErrorsAreStructured(t *testing.T) {
	handler := newHandler(&fakeRepository{}, nil)
	for _, tc := range []struct {
		method, path, code string
		status             int
	}{
		{http.MethodPost, "/api/v2/summary", "method_not_allowed", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v2/unknown", "not_found", http.StatusNotFound},
	} {
		recorder := request(t, handler, tc.method, tc.path)
		var body apiError
		if recorder.Code != tc.status || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Error.Code != tc.code {
			t.Errorf("%s %s: status=%d body=%s", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestEventHubDoesNotBlockOnSlowClients(t *testing.T) {
	hub := newEventHub()
	events, unsubscribe := hub.subscribe()
	defer unsubscribe()
	for i := 0; i < 100; i++ {
		hub.publish(map[string]int{"sequence": i})
	}
	count := 0
	for {
		select {
		case <-events:
			count++
		default:
			if count == 0 || count > 8 {
				t.Fatalf("bounded stream buffered %d events", count)
			}
			return
		}
	}
}

func TestDashboardIsSelfContainedAndTruthful(t *testing.T) {
	recorder := request(t, newHandler(&fakeRepository{}, nil), http.MethodGet, "/")
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d", recorder.Code)
	}
	for _, forbidden := range []string{"https://", "tailwind", "websocket", "Avg Response Time", "Active Subagents", "Budget cap"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
			t.Errorf("dashboard contains forbidden placeholder/dependency %q", forbidden)
		}
	}
	for _, expected := range []string{"Daily tokens", "Measurement coverage", `id="theme"`, "System", "Light", "Dark", "/assets/app.js"} {
		if !strings.Contains(body, expected) {
			t.Errorf("dashboard missing %q", expected)
		}
	}
	asset := request(t, newHandler(&fakeRepository{}, nil), http.MethodGet, "/assets/app.js")
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "localStorage") || !strings.Contains(asset.Body.String(), "dataset.theme") {
		t.Fatalf("dashboard theme persistence is missing from app.js")
	}
}
