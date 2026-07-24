package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIngestClaudeCanonicalUsageAndSubagent(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "-sanitized-project")
	if err := os.MkdirAll(filepath.Join(projectDir, "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(projectDir, "session-main.jsonl")
	mainFixture := strings.Join([]string{
		`{"type":"user","sessionId":"session-main","uuid":"user-1","timestamp":"2026-07-01T09:59:00-07:00","message":{"content":"PRIVATE PROMPT MUST NOT PERSIST"}}`,
		`{"type":"assistant","sessionId":"session-main","uuid":"turn-1","requestId":"request-1","parentUuid":"user-1","timestamp":"2026-07-01T10:00:00-07:00","message":{"id":"message-1","role":"assistant","model":"claude-sonnet-test","usage":{"input_tokens":11,"cache_read_input_tokens":22,"cache_creation_input_tokens":33,"output_tokens":44,"cache_creation":{"ephemeral_5m_input_tokens":9999}},"content":[{"type":"text","text":"PRIVATE RESPONSE MUST NOT PERSIST"}]}}`,
		`{"type":"assistant","sessionId":"session-main","uuid":"turn-2","timestamp":"2026-07-01T10:01:00-07:00","message":{"role":"assistant","model":"claude-opus-test","usage":{"input_tokens":5,"cache_read_input_tokens":7,"cache_creation_input_tokens":9,"output_tokens":13}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(mainPath, []byte(mainFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	subagentPath := filepath.Join(projectDir, "subagents", "agent-child.jsonl")
	subagentFixture := `{"type":"assistant","sessionId":"session-main","agentId":"child-agent","isSidechain":true,"uuid":"child-turn","parentUuid":"parent-turn","timestamp":"2026-07-01T10:02:00-07:00","message":{"role":"assistant","model":"claude-haiku-test","usage":{"input_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4,"output_tokens":5}}}` + "\n"
	if err := os.WriteFile(subagentPath, []byte(subagentFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := newClaudeTestRepository(t)
	result, err := ingestClaude(context.Background(), repo, home)
	if err != nil {
		t.Fatalf("ingestClaude: %v", err)
	}
	if result.inserted != 3 {
		t.Fatalf("inserted = %d, want 3; diagnostics=%v", result.inserted, result.diagnostics)
	}

	var input, cacheRead, cacheWrite, output, total int64
	if err := repo.db.QueryRow(`SELECT SUM(input_uncached),SUM(cache_read),SUM(cache_write),SUM(output_tokens),SUM(total_tokens) FROM usage_events`).Scan(&input, &cacheRead, &cacheWrite, &output, &total); err != nil {
		t.Fatal(err)
	}
	if input != 18 || cacheRead != 32 || cacheWrite != 46 || output != 62 || total != 158 {
		t.Fatalf("token categories = (%d,%d,%d,%d,%d), want (18,32,46,62,158)", input, cacheRead, cacheWrite, output, total)
	}

	var childID, parentID, sourceID, project, sourcePathHash string
	var isSubagent int
	if err := repo.db.QueryRow(`SELECT id,parent_session_id,source_session_id,project,source_path_hash,is_subagent FROM sessions WHERE source_session_id='child-agent'`).Scan(&childID, &parentID, &sourceID, &project, &sourcePathHash, &isSubagent); err != nil {
		t.Fatal(err)
	}
	if sourceID != "child-agent" || parentID != claudeSessionID("session-main") || isSubagent != 1 {
		t.Fatalf("subagent identity mismatch: child=%q parent=%q source=%q isSubagent=%d", childID, parentID, sourceID, isSubagent)
	}
	if project == "" || strings.Contains(project, "sanitized") || len(sourcePathHash) != 64 || sourcePathHash == subagentPath {
		t.Fatalf("paths were not safely represented: project=%q sourcePathHash=%q", project, sourcePathHash)
	}

	var leaked int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE title LIKE '%PRIVATE%' OR project LIKE '%PRIVATE%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("private fixture content leaked into session fields")
	}

	again, err := ingestClaude(context.Background(), repo, home)
	if err != nil {
		t.Fatalf("second ingestClaude: %v", err)
	}
	if again.inserted != 0 {
		t.Fatalf("idempotent sync inserted %d events", again.inserted)
	}
	var eventCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 3 {
		t.Fatalf("event count after repeat = %d, want 3", eventCount)
	}
}

func TestIngestClaudeAcceptsValidFinalRowsAndDiagnosesUnknowns(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "partial.jsonl")
	completeUnknownModel := `{"type":"assistant","sessionId":"partial-session","uuid":"complete","timestamp":"2026-07-02T00:00:00Z","message":{"role":"assistant","usage":{"input_tokens":3,"output_tokens":4}}}` + "\n"
	partial := `{"type":"assistant","sessionId":"partial-session","uuid":"partial","timestamp":"2026-07-02T00:01:00Z","message":{"role":"assistant","model":"claude-test","usage":{"input_tokens":5,"output_tokens":6}}}`
	if err := os.WriteFile(path, []byte(completeUnknownModel+partial), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := newClaudeTestRepository(t)
	first, err := ingestClaude(context.Background(), repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if first.inserted != 2 || !containsClaudeDiagnostic(first.diagnostics, "unknown model") {
		t.Fatalf("first result = %+v, want two complete JSON events and unknown-model diagnostic", first)
	}
	if err := appendClaudeTestFile(path, "\n"); err != nil {
		t.Fatal(err)
	}
	second, err := ingestClaude(context.Background(), repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if second.inserted != 2 {
		t.Fatalf("atomic source refresh inserted %d events, want 2; diagnostics=%v", second.inserted, second.diagnostics)
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count = %d, want 2", count)
	}
}

func TestIngestClaudeSkipsMissingTimestampWithoutSubstitution(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"type":"user","sessionId":"missing-time","uuid":"user","timestamp":"2026-07-03T00:00:00Z","message":{"role":"user"}}`,
		`{"type":"assistant","sessionId":"missing-time","uuid":"assistant","message":{"role":"assistant","model":"claude-test","usage":{"input_tokens":100,"output_tokens":50}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "missing.jsonl"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	repo := newClaudeTestRepository(t)
	result, err := ingestClaude(context.Background(), repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if !containsClaudeDiagnostic(result.diagnostics, "missing valid timestamp") {
		t.Fatalf("diagnostics = %v, want missing timestamp", result.diagnostics)
	}
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing timestamp produced %d events, want 0", count)
	}
}

func TestIngestClaudeDeduplicatesRepeatedResponseSnapshots(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"type":"assistant","sessionId":"session","uuid":"row-1","parentUuid":"parent-1","timestamp":"2026-07-04T00:00:00Z","message":{"id":"msg-1","role":"assistant","model":"claude-test","usage":{"input_tokens":10,"output_tokens":2}}}`,
		`{"type":"assistant","sessionId":"session","uuid":"row-2","parentUuid":"parent-2","timestamp":"2026-07-04T00:00:01Z","message":{"id":"msg-1","role":"assistant","model":"claude-test","usage":{"input_tokens":10,"output_tokens":3}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := newClaudeTestRepository(t)
	if _, err := ingestClaude(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	var count, total int
	if err := repo.db.QueryRow(`SELECT COUNT(*),SUM(total_tokens) FROM usage_events`).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 1 || total != 13 {
		t.Fatalf("count=%d total=%d, want final response snapshot only", count, total)
	}
}

func TestIngestClaudeMultiSessionRewriteReplacesAllEventsAtomically(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "sessions.jsonl")
	fixture := func(a, b string) string {
		return strings.Join([]string{
			`{"type":"assistant","sessionId":"session-a","uuid":"row-a","timestamp":"2026-07-04T00:00:00Z","message":{"id":"msg-a","role":"assistant","model":"claude-test","usage":{"input_tokens":` + a + `}}}`,
			`{"type":"assistant","sessionId":"session-b","uuid":"row-b","timestamp":"2026-07-04T00:00:01Z","message":{"id":"msg-b","role":"assistant","model":"claude-test","usage":{"input_tokens":` + b + `}}}`,
		}, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(fixture("10", "20")), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := newClaudeTestRepository(t)
	if _, err := ingestClaude(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fixture("11", "21")), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestClaude(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	var sessions, events int
	var total int64
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*),SUM(total_tokens) FROM usage_events`).Scan(&events, &total); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 || events != 2 || total != 32 {
		t.Fatalf("rewrite = sessions %d events %d total %d, want 2/2/32", sessions, events, total)
	}
	oneSession := `{"type":"assistant","sessionId":"session-a","uuid":"row-a","timestamp":"2026-07-04T00:00:02Z","message":{"id":"msg-a","role":"assistant","model":"claude-test","usage":{"input_tokens":12}}}` + "\n"
	if err := os.WriteFile(path, []byte(oneSession), 0o600); err != nil {
		t.Fatal(err)
	}
	future = future.Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestClaude(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*),SUM(total_tokens) FROM usage_events`).Scan(&events, &total); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 || events != 1 || total != 12 {
		t.Fatalf("removed-session rewrite = sessions %d events %d total %d, want 1/1/12", sessions, events, total)
	}
}

func newClaudeTestRepository(t *testing.T) *Repository {
	t.Helper()
	dbConn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dbConn.SetMaxOpenConns(1)
	if err := migrate(dbConn); err != nil {
		_ = dbConn.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbConn.Close() })
	return NewRepository(dbConn)
}

func appendClaudeTestFile(path, value string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.WriteString(value)
	return err
}

func containsClaudeDiagnostic(diagnostics []string, fragment string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, fragment) {
			return true
		}
	}
	return false
}
