package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

func TestInitDBBacksUpLegacyAndSecuresFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIT_DATA_DIR", dir)
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE token_logs(id INTEGER PRIMARY KEY, timestamp DATETIME); INSERT INTO token_logs(timestamp) VALUES('bad legacy time')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	_ = dbConn.Close()
	backups, err := filepath.Glob(filepath.Join(dir, "backups", "data-v1-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "data.db"), filepath.Join(dir, "backups"), backups[0]} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode=%o want=%o", path, info.Mode().Perm(), want)
		}
	}
}

func TestV2QueriesExcludeEstimatesAndZeroFill(t *testing.T) {
	t.Setenv("AIT_DATA_DIR", t.TempDir())
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dbConn.Close() }()
	repo := NewRepository(dbConn)
	ctx := context.Background()
	loc, err := time.LoadLocation("America/Phoenix")
	if err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 7, 1, 12, 0, 0, 0, loc)
	day3 := day1.AddDate(0, 0, 2)
	base := SessionRecord{ID: "codex:s1", Agent: "codex", Provider: "openai", SourceSessionID: "s1", StartedAtMS: day1.UnixMilli(), UpdatedAtMS: day3.UnixMilli(), Status: "unknown", Measurement: coredb.MeasurementReported, SourceKind: "fixture", SourcePathHash: "safe"}
	batch := Batch{Session: base, Events: []UsageEvent{
		{ID: "e1", SessionID: base.ID, OccurredAtMS: day1.UnixMilli(), Provider: "openai", Model: "m", Tokens: coredb.TokenCounts{InputUncached: 10, Output: 5, Total: 15}, Measurement: coredb.MeasurementReported, SourcePathHash: "safe", ParserVersion: "test"},
		{ID: "e2", SessionID: base.ID, OccurredAtMS: day3.UnixMilli(), Provider: "openai", Model: "m", Tokens: coredb.TokenCounts{InputUncached: 20, Output: 10, Total: 30}, Measurement: coredb.MeasurementEstimated, SourcePathHash: "safe", ParserVersion: "test"},
	}}
	if _, _, err := repo.ApplyBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	filter := coredb.QueryFilter{From: time.Date(2026, 7, 1, 0, 0, 0, 0, loc), To: time.Date(2026, 7, 4, 0, 0, 0, 0, loc), Timezone: loc}
	summary, err := repo.Summary(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.Total != 15 || summary.Sessions != 1 {
		t.Fatalf("summary=%+v", summary)
	}
	series, err := repo.Series(ctx, filter, "day")
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 3 || series[0].Tokens.Total != 15 || series[1].Tokens.Total != 0 || series[2].Tokens.Total != 0 {
		t.Fatalf("series=%+v", series)
	}
	filter.IncludeEstimates = true
	summary, err = repo.Summary(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Tokens.Total != 45 || summary.Quality.Estimated != 30 {
		t.Fatalf("summary with estimates=%+v", summary)
	}
}

func TestSessionsIncludeZeroUsageMetadata(t *testing.T) {
	t.Setenv("AIT_DATA_DIR", t.TempDir())
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dbConn.Close() }()
	repo := NewRepository(dbConn)
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := SessionRecord{ID: "agy:s1", Agent: "agy", Provider: "google", SourceSessionID: "s1", UpdatedAtMS: now.UnixMilli(), Status: "unknown", Measurement: coredb.MeasurementDerived, SourceKind: "fixture", SourcePathHash: "safe"}
	if _, _, err := repo.ApplyBatch(context.Background(), Batch{Session: s}); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListSessions(context.Background(), coredb.QueryFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), Timezone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].EventCount != 0 {
		t.Fatalf("page=%+v", page)
	}
}

func TestSessionFiltersUseMatchingEventsNotOnlySessionSummaryFields(t *testing.T) {
	t.Setenv("AIT_DATA_DIR", t.TempDir())
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dbConn.Close() }()
	repo := NewRepository(dbConn)
	now := time.Now().UTC().Truncate(time.Millisecond)
	s := SessionRecord{ID: "agy:s1", Agent: "agy", Provider: "google", SourceSessionID: "s1", UpdatedAtMS: now.UnixMilli(), Model: "", Measurement: coredb.MeasurementDerived, SourceKind: "fixture", SourcePathHash: "safe"}
	e := UsageEvent{ID: "e", SessionID: s.ID, OccurredAtMS: now.UnixMilli(), Provider: "google", Model: "estimated-model", Tokens: coredb.TokenCounts{Total: 10}, Measurement: coredb.MeasurementEstimated, SourcePathHash: "safe", ParserVersion: "test"}
	if _, _, err := repo.ApplyBatch(context.Background(), Batch{Session: s, Events: []UsageEvent{e}}); err != nil {
		t.Fatal(err)
	}
	filter := coredb.QueryFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), Timezone: time.UTC, Model: "estimated-model", Quality: coredb.MeasurementEstimated}
	page, err := repo.ListSessions(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].Tokens.Total != 10 {
		t.Fatalf("filtered page=%+v, want matching estimated event", page)
	}
	summary, err := repo.Summary(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Sessions != 1 || summary.Tokens.Total != 10 {
		t.Fatalf("filtered summary=%+v, want one matching session", summary)
	}
}

func TestAggregateCostIsUnavailableWhenAnyUsageHasUnknownPricing(t *testing.T) {
	t.Setenv("AIT_DATA_DIR", t.TempDir())
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dbConn.Close() }()
	repo := NewRepository(dbConn)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	s := SessionRecord{ID: "s", Agent: "claude", Provider: "anthropic", SourceSessionID: "s", UpdatedAtMS: now.UnixMilli(), Measurement: coredb.MeasurementReported, SourceKind: "fixture", SourcePathHash: "safe"}
	events := []UsageEvent{
		{ID: "known", SessionID: "s", OccurredAtMS: now.UnixMilli(), Provider: "anthropic", Model: "claude-3-5-sonnet-20241022", Tokens: coredb.TokenCounts{InputUncached: 10, Total: 10}, Measurement: coredb.MeasurementReported, SourcePathHash: "safe", ParserVersion: "test"},
		{ID: "unknown", SessionID: "s", OccurredAtMS: now.UnixMilli(), Provider: "anthropic", Model: "future-model", Tokens: coredb.TokenCounts{InputUncached: 10, Total: 10}, Measurement: coredb.MeasurementReported, SourcePathHash: "safe", ParserVersion: "test"},
	}
	if _, _, err := repo.ApplyBatch(context.Background(), Batch{Session: s, Events: events}); err != nil {
		t.Fatal(err)
	}
	filter := coredb.QueryFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), Timezone: time.UTC}
	summary, err := repo.Summary(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CostMicros != nil {
		t.Fatalf("mixed-price summary cost=%d, want unavailable", *summary.CostMicros)
	}
	series, err := repo.Series(context.Background(), filter, "day")
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].CostMicros != nil {
		t.Fatalf("mixed-price series=%+v, want unavailable cost", series)
	}
	page, err := repo.ListSessions(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].CostMicros != nil {
		t.Fatalf("mixed-price sessions=%+v, want unavailable cost", page.Sessions)
	}
	filter.Model = "claude-3-5-sonnet-20241022"
	knownOnly, err := repo.Summary(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if knownOnly.CostMicros == nil {
		t.Fatal("known-price filtered summary cost is unavailable")
	}
}

func TestClearAllRemovesFactsAndCheckpointsTogether(t *testing.T) {
	t.Setenv("AIT_DATA_DIR", t.TempDir())
	dbConn, err := InitDB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dbConn.Close() }()
	repo := NewRepository(dbConn)
	now := time.Now().UnixMilli()
	s := SessionRecord{ID: "s", Agent: "codex", Provider: "openai", SourceSessionID: "s", UpdatedAtMS: now, Measurement: coredb.MeasurementReported, SourceKind: "fixture", SourcePathHash: "source"}
	e := UsageEvent{ID: "e", SessionID: "s", OccurredAtMS: now, Provider: "openai", Tokens: coredb.TokenCounts{Total: 1}, Measurement: coredb.MeasurementReported, SourcePathHash: "source", ParserVersion: "test"}
	cp := SourceCheckpoint{Path: "source", Size: 1, MTimeNS: 1, Offset: 1, ParserVersion: "test"}
	if _, _, err := repo.ApplyBatch(context.Background(), Batch{Session: s, Events: []UsageEvent{e}, Checkpoint: &cp}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ClearAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "usage_events", "source_checkpoints"} {
		var n int
		if err := dbConn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", table, n, err)
		}
	}
}
