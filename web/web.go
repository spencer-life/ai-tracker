package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spencer-life/ai-tracker/internal/inventory"
)

// FS contains the dependency-free dashboard.
//
//go:embed index.html assets/app.css assets/app.js
var FS embed.FS

const (
	defaultRange = "30d"
	maxPageSize  = 200
)

var (
	scanInventory = inventory.Scan
	userHomeDir   = os.UserHomeDir
	workingDir    = os.Getwd
)

type apiServer struct {
	repo   db.Repository
	syncFn func(context.Context) error
	events *eventHub
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[chan []byte]struct{})}
}

func (h *eventHub) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.clients[ch]; ok {
			delete(h.clients, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func (h *eventHub) publish(event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- payload:
		default: // A slow browser must not block sync or other clients.
		}
	}
}

// StartServer serves the local dashboard and v2 API. It refuses non-loopback
// listeners because the dashboard intentionally has no remote authentication.
func StartServer(addr string, repo db.Repository, syncFn func(context.Context) error) error {
	if err := validateLoopbackAddr(addr); err != nil {
		return err
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           newHandler(repo, syncFn),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("dashboard listening on http://%s", addr)
	return server.ListenAndServe()
}

func validateLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid dashboard address %q: %w", addr, err)
	}
	if port == "" {
		return errors.New("dashboard address requires a port")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("dashboard address %q is not loopback-only", addr)
	}
	return nil
}

func newHandler(repo db.Repository, syncFn func(context.Context) error) http.Handler {
	s := &apiServer{repo: repo, syncFn: syncFn, events: newEventHub()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/summary", s.summary)
	mux.HandleFunc("GET /api/v2/series", s.series)
	mux.HandleFunc("GET /api/v2/sessions", s.sessions)
	mux.HandleFunc("GET /api/v2/sessions/{id}", s.session)
	mux.HandleFunc("GET /api/v2/breakdowns", s.breakdowns)
	mux.HandleFunc("GET /api/v2/sync/status", s.syncStatus)
	mux.HandleFunc("GET /api/v2/inventory", s.inventory)
	mux.HandleFunc("POST /api/v2/sync", s.sync)
	mux.HandleFunc("GET /api/v2/events", s.eventStream)
	static, err := fs.Sub(FS, ".")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	return securityHeaders(structuredAPIRoutes(mux))
}

func structuredAPIRoutes(next http.Handler) http.Handler {
	methods := map[string]string{
		"/api/v2/summary": "GET", "/api/v2/series": "GET", "/api/v2/sessions": "GET",
		"/api/v2/breakdowns": "GET", "/api/v2/sync/status": "GET", "/api/v2/inventory": "GET", "/api/v2/sync": "POST", "/api/v2/events": "GET",
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		allowed, exists := methods[r.URL.Path]
		if !exists && strings.HasPrefix(r.URL.Path, "/api/v2/sessions/") && len(strings.TrimPrefix(r.URL.Path, "/api/v2/sessions/")) > 0 {
			allowed, exists = http.MethodGet, true
		}
		if !exists {
			writeError(w, http.StatusNotFound, "not_found", "API endpoint was not found")
			return
		}
		if r.Method != allowed {
			w.Header().Set("Allow", allowed)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this endpoint")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *apiServer) summary(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	result, err := s.repo.Summary(r.Context(), filter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *apiServer) series(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	interval := valueOr(r.URL.Query().Get("interval"), "day")
	if !oneOf(interval, "day", "week", "month") {
		writeError(w, http.StatusBadRequest, "invalid_interval", "interval must be day, week, or month")
		return
	}
	points, err := s.repo.Series(r.Context(), filter, interval)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if points == nil {
		points = []db.SeriesPoint{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"interval": interval, "points": points, "rangeFrom": filter.From, "rangeTo": filter.To, "timezone": filter.Timezone.String(), "generatedAt": time.Now().UTC()})
}

func (s *apiServer) sessions(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	page, err := s.repo.ListSessions(r.Context(), filter)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if page.Sessions == nil {
		page.Sessions = []db.Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": page.Sessions, "nextCursor": page.NextCursor, "rangeFrom": filter.From, "rangeTo": filter.To, "timezone": filter.Timezone.String(), "generatedAt": time.Now().UTC()})
}

func (s *apiServer) session(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "session id is required")
		return
	}
	result, err := s.repo.GetSession(r.Context(), id)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *apiServer) breakdowns(w http.ResponseWriter, r *http.Request) {
	filter, err := parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	dimension := valueOr(r.URL.Query().Get("dimension"), "provider")
	if !oneOf(dimension, "agent", "provider", "model", "project", "quality") {
		writeError(w, http.StatusBadRequest, "invalid_dimension", "dimension must be agent, provider, model, project, or quality")
		return
	}
	items, err := s.repo.Breakdown(r.Context(), filter, dimension)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if items == nil {
		items = []db.BreakdownItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dimension": dimension, "items": items, "rangeFrom": filter.From, "rangeTo": filter.To, "timezone": filter.Timezone.String(), "generatedAt": time.Now().UTC()})
}

func (s *apiServer) syncStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.repo.LastSync(r.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []string{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *apiServer) inventory(w http.ResponseWriter, r *http.Request) {
	home, err := userHomeDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "inventory_failed", "user home is unavailable")
		return
	}
	cwd, err := workingDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "inventory_failed", "working directory is unavailable")
		return
	}
	items, err := scanInventory(r.Context(), home, cwd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "inventory_failed", err.Error())
		return
	}
	if items == nil {
		items = []inventory.Component{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "generatedAt": time.Now().UTC()})
}

func (s *apiServer) sync(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_rejected", "request origin must match the dashboard")
		return
	}
	if s.syncFn == nil {
		writeError(w, http.StatusNotImplemented, "sync_unavailable", "sync is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	defer func() { _ = r.Body.Close() }()
	if err := s.syncFn(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "sync_failed", err.Error())
		return
	}
	status, err := s.repo.LastSync(r.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	if status.Diagnostics == nil {
		status.Diagnostics = []string{}
	}
	s.events.publish(map[string]any{"type": "sync", "status": status})
	writeJSON(w, http.StatusOK, status)
}

func (s *apiServer) eventStream(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_rejected", "request origin must match the dashboard")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	events, unsubscribe := s.events.subscribe()
	defer unsubscribe()
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			_, _ = fmt.Fprintf(w, "event: update\ndata: %s\n\n", event)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func parseFilter(r *http.Request) (db.QueryFilter, error) {
	q := r.URL.Query()
	tzName := valueOr(q.Get("tz"), time.Local.String())
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return db.QueryFilter{}, fmt.Errorf("invalid timezone %q", tzName)
	}
	now := time.Now().In(loc)
	from, to, err := parseRange(q, now, loc)
	if err != nil {
		return db.QueryFilter{}, err
	}
	quality := db.Measurement(q.Get("quality"))
	if quality != "" && !oneOf(string(quality), string(db.MeasurementReported), string(db.MeasurementDerived), string(db.MeasurementEstimated), string(db.MeasurementLegacy)) {
		return db.QueryFilter{}, fmt.Errorf("invalid quality %q", quality)
	}
	includeEstimates := false
	if raw := q.Get("includeEstimates"); raw != "" {
		includeEstimates, err = strconv.ParseBool(raw)
		if err != nil {
			return db.QueryFilter{}, errors.New("includeEstimates must be true or false")
		}
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxPageSize {
			return db.QueryFilter{}, fmt.Errorf("limit must be between 1 and %d", maxPageSize)
		}
	}
	return db.QueryFilter{
		From:             from,
		To:               to,
		Timezone:         loc,
		Agent:            q.Get("agent"),
		Provider:         q.Get("provider"),
		Model:            q.Get("model"),
		Project:          q.Get("project"),
		Quality:          quality,
		IncludeEstimates: includeEstimates,
		Limit:            limit,
		Cursor:           q.Get("cursor"),
	}, nil
}

func parseRange(q url.Values, now time.Time, loc *time.Location) (time.Time, time.Time, error) {
	rangeName := q.Get("range")
	if rangeName == "" && (q.Get("from") != "" || q.Get("to") != "") {
		rangeName = "custom"
	}
	rangeName = valueOr(rangeName, defaultRange)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var from, to time.Time
	switch rangeName {
	case "today":
		from, to = dayStart, dayStart.AddDate(0, 0, 1)
	case "7d":
		from, to = dayStart.AddDate(0, 0, -6), dayStart.AddDate(0, 0, 1)
	case "30d":
		from, to = dayStart.AddDate(0, 0, -29), dayStart.AddDate(0, 0, 1)
	case "mtd":
		from, to = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc), dayStart.AddDate(0, 0, 1)
	case "custom":
		var err error
		from, err = parseTimeValue(q.Get("from"), loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
		to, err = parseTimeValue(q.Get("to"), loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("range must be today, 7d, 30d, mtd, or custom")
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	return from, to, nil
}

func parseTimeValue(value string, loc *time.Location) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("value is required for a custom range")
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, loc)
	if err != nil {
		return time.Time{}, errors.New("use YYYY-MM-DD or RFC3339")
	}
	return parsed, nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && strings.EqualFold(u.Host, r.Host) && u.Scheme == scheme
}

func writeRepositoryError(w http.ResponseWriter, err error) {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "not found") || errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not_found", "requested resource was not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "repository_error", err.Error())
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	response := apiError{}
	response.Error.Code = code
	response.Error.Message = message
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
