package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
	_ "modernc.org/sqlite"
)

func TestIngestAntigravityImportsExactSessionsWithoutFakeUsage(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(root, "conversation_summaries.db")
	source := openFixtureDB(t, summaryPath)
	createSummaryFixture(t, source)
	if _, err := source.Exec(`INSERT INTO conversation_summaries
		(conversation_id,last_modified_time,last_user_input_time,status,project_id,parent_conversation_id,nesting_depth,killed,title,preview)
		VALUES
		('parent','2026-07-20T10:30:00Z','2026-07-20T10:00:00Z','done','private-project','',0,0,'private title','private preview'),
		('child','2026-07-20T11:30:00Z','2026-07-20T11:00:00Z','running','private-project','parent',1,0,'another title','another preview')`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	repo, target := newFixtureRepository(t)
	result, err := ingestAntigravity(context.Background(), repo, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.updated != 2 {
		t.Fatalf("updated sessions = %d, want 2", result.updated)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM sessions WHERE agent='agy'`, 2)
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events`, 0)
	assertCount(t, target, `SELECT COUNT(*) FROM session_relationships WHERE relation='subagent'`, 1)

	var parentID, title, model, project, measurement string
	var isSubagent bool
	if err := target.QueryRow(`SELECT parent_session_id,title,model,project,measurement,is_subagent FROM sessions WHERE source_session_id='child'`).
		Scan(&parentID, &title, &model, &project, &measurement, &isSubagent); err != nil {
		t.Fatal(err)
	}
	if parentID != antigravitySessionID("parent") || !isSubagent {
		t.Fatalf("parent relationship = (%q, %v)", parentID, isSubagent)
	}
	if title != "" || model != "" {
		t.Fatalf("private title or invented model was stored: title=%q model=%q", title, model)
	}
	if project == "" || project == "private-project" {
		t.Fatalf("project identity was not safely hashed: %q", project)
	}
	if measurement != string(coredb.MeasurementReported) {
		t.Fatalf("measurement = %q", measurement)
	}
	var checkpointPath string
	if err := target.QueryRow(`SELECT path FROM source_checkpoints`).Scan(&checkpointPath); err != nil {
		t.Fatal(err)
	}
	if checkpointPath == summaryPath || strings.Contains(checkpointPath, home) {
		t.Fatalf("checkpoint exposed source path: %q", checkpointPath)
	}

	second, err := ingestAntigravity(context.Background(), repo, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.skipped == 0 {
		t.Fatal("unchanged summary source was not skipped")
	}
	assertCount(t, target, `SELECT COUNT(*) FROM sessions`, 2)

	source = openFixtureDB(t, summaryPath)
	if _, err := source.Exec(`DELETE FROM conversation_summaries WHERE conversation_id='child'; UPDATE conversation_summaries SET last_modified_time='2026-07-20T12:30:00Z' WHERE conversation_id='parent'`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(summaryPath, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestAntigravity(context.Background(), repo, home, false); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM sessions WHERE agent='agy'`, 1)
	assertCount(t, target, `SELECT COUNT(*) FROM sessions WHERE source_session_id='parent'`, 1)
	assertCount(t, target, `SELECT COUNT(*) FROM session_relationships`, 0)
}

func TestIngestAntigravityEstimatesAreOptInAndSeparated(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := openFixtureDB(t, filepath.Join(root, "conversation_summaries.db"))
	createSummaryFixture(t, source)
	if _, err := source.Exec(`INSERT INTO conversation_summaries
		(conversation_id,last_modified_time,last_user_input_time,status,project_id,parent_conversation_id,nesting_depth,killed,title,preview)
		VALUES ('session-a','2026-07-21T10:00:00Z','2026-07-21T09:00:00Z','','','',0,0,'','')`); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(root, "brain", "session-a")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := "{\"timestamp\":\"2026-07-21T09:01:00Z\",\"source\":\"USER\",\"content\":\"abcdefgh\"}\n" +
		"{\"timestamp\":\"2026-07-21T09:02:00Z\",\"source\":\"MODEL\",\"content\":\"abcde\",\"thinking\":\"fgh\"}\n"
	if err := os.WriteFile(filepath.Join(transcriptDir, "transcript.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	repo, target := newFixtureRepository(t)
	if _, err := ingestAntigravity(context.Background(), repo, home, false); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM sessions`, 1)
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events`, 0)

	if _, err := ingestAntigravity(context.Background(), repo, home, true); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events WHERE measurement='estimated'`, 2)
	var input, output, total int64
	if err := target.QueryRow(`SELECT SUM(input_uncached),SUM(output_tokens),SUM(total_tokens) FROM usage_events`).Scan(&input, &output, &total); err != nil {
		t.Fatal(err)
	}
	if input != 2 || output != 2 || total != 4 {
		t.Fatalf("estimated counts = input %d output %d total %d", input, output, total)
	}
	var nonEmptyModels, nonNullCosts int64
	if err := target.QueryRow(`SELECT COUNT(*) FILTER (WHERE model<>''), COUNT(cost_micros) FROM usage_events`).Scan(&nonEmptyModels, &nonNullCosts); err != nil {
		t.Fatal(err)
	}
	if nonEmptyModels != 0 || nonNullCosts != 0 {
		t.Fatalf("estimated events invented model or cost: models=%d costs=%d", nonEmptyModels, nonNullCosts)
	}
	var storedText int64
	if err := target.QueryRow(`SELECT COUNT(*) FROM sessions WHERE title LIKE '%abcdefgh%' OR project LIKE '%abcdefgh%'`).Scan(&storedText); err != nil {
		t.Fatal(err)
	}
	if storedText != 0 {
		t.Fatal("transcript text was retained in session metadata")
	}
	if rerun, err := ingestAntigravity(context.Background(), repo, home, true); err != nil {
		t.Fatal(err)
	} else if rerun.skipped == 0 {
		t.Fatal("unchanged estimated transcript was not skipped")
	}
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events`, 2)

	// Replacing the transcript reconciles the complete estimated event set;
	// removed rows must not remain as stale usage.
	oneRow := "{\"timestamp\":\"2026-07-21T09:03:00Z\",\"source\":\"USER\",\"content\":\"abcdefghijkl\"}\n"
	transcriptPath := filepath.Join(transcriptDir, "transcript.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(oneRow), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestAntigravity(context.Background(), repo, home, true); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events`, 1)
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events WHERE input_uncached=3 AND total_tokens=3`, 1)

	// A complete final JSON row is accepted even without a trailing newline.
	partial := "{\"timestamp\":\"2026-07-21T09:04:00Z\",\"role\":\"user\",\"content\":\"abcdefghijklmnopqrstuvwxyzabcdefghijklmn\"}"
	if err := os.WriteFile(transcriptPath, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestAntigravity(context.Background(), repo, home, true); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events WHERE input_uncached=10 AND total_tokens=10`, 1)
	if err := os.WriteFile(transcriptPath, []byte(partial+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestAntigravity(context.Background(), repo, home, true); err != nil {
		t.Fatal(err)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events WHERE input_uncached=10 AND total_tokens=10`, 1)
}

func TestIngestAntigravityToleratesSummaryVariantAndUsesSafeFallback(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(root, "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	summary := openFixtureDB(t, filepath.Join(root, "conversation_summaries.db"))
	if _, err := summary.Exec(`CREATE TABLE conversation_summaries(other_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := summary.Close(); err != nil {
		t.Fatal(err)
	}
	conversation := openFixtureDB(t, filepath.Join(root, "conversations", "fallback-session.db"))
	if _, err := conversation.Exec(`
		CREATE TABLE trajectory_meta(trajectory_id TEXT PRIMARY KEY, cascade_id TEXT, trajectory_type INTEGER, source INTEGER);
		CREATE TABLE steps(idx INTEGER PRIMARY KEY, metadata BLOB, step_payload BLOB);
		INSERT INTO trajectory_meta VALUES('fallback-session','opaque-cascade',1,1);
		INSERT INTO steps VALUES(1, X'00FF', X'FF00');`); err != nil {
		t.Fatal(err)
	}
	if err := conversation.Close(); err != nil {
		t.Fatal(err)
	}

	repo, target := newFixtureRepository(t)
	result, err := ingestAntigravity(context.Background(), repo, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsDiagnostic(result.diagnostics, "lacks conversation identity") {
		t.Fatalf("missing schema diagnostic: %v", result.diagnostics)
	}
	assertCount(t, target, `SELECT COUNT(*) FROM sessions WHERE source_session_id='fallback-session' AND measurement='derived'`, 1)
	assertCount(t, target, `SELECT COUNT(*) FROM usage_events`, 0)
}

func TestIngestAntigravityNormalizesNullableSummaryMetadata(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	summary := openFixtureDB(t, filepath.Join(root, "conversation_summaries.db"))
	if _, err := summary.Exec(`CREATE TABLE conversation_summaries(
		conversation_id, last_modified_time, last_user_input_time, status,
		project_id, parent_conversation_id, nesting_depth, killed);
		INSERT INTO conversation_summaries VALUES(
		'n-1','2026-07-21T08:00:00Z','2026-07-21T12:00:00Z',NULL,NULL,NULL,NULL,NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := summary.Close(); err != nil {
		t.Fatal(err)
	}
	repo, target := newFixtureRepository(t)
	if _, err := ingestAntigravity(context.Background(), repo, home, false); err != nil {
		t.Fatal(err)
	}
	var updatedAt int64
	var status, project, parent string
	if err := target.QueryRow(`SELECT updated_at_ms,status,project,parent_session_id FROM sessions WHERE source_session_id='n-1'`).Scan(&updatedAt, &status, &project, &parent); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC).UnixMilli()
	if updatedAt != want || status != "unknown" || project != "" || parent != "" {
		t.Fatalf("normalized metadata = updated %d status %q project %q parent %q", updatedAt, status, project, parent)
	}
}

func TestAntigravityTimeSupportsObservedSQLiteFormats(t *testing.T) {
	want := time.Date(2026, 7, 21, 10, 30, 0, 0, time.UTC)
	for _, value := range []any{"2026-07-21T10:30:00Z", "2026-07-21 10:30:00", want.Unix(), want.UnixMilli()} {
		got, ok := antigravityTime(value)
		if !ok || !got.Equal(want) {
			t.Fatalf("antigravityTime(%v) = %v, %v", value, got, ok)
		}
	}
	if _, ok := antigravityTime("not-a-time"); ok {
		t.Fatal("invalid time was accepted")
	}
}

func newFixtureRepository(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(db), db
}

func openFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func createSummaryFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE conversation_summaries(
		conversation_id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', preview TEXT NOT NULL DEFAULT '',
		last_modified_time DATETIME, last_user_input_time DATETIME, status TEXT NOT NULL DEFAULT '',
		project_id TEXT NOT NULL DEFAULT '', parent_conversation_id TEXT NOT NULL DEFAULT '',
		nesting_depth INTEGER NOT NULL DEFAULT 0, killed NUMERIC NOT NULL DEFAULT false
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}

func containsDiagnostic(diagnostics []string, needle string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, needle) {
			return true
		}
	}
	return false
}
