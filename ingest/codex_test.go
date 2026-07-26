package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
	_ "modernc.org/sqlite"
)

const (
	codexTestMeta   = `{"timestamp":"2026-07-01T01:02:03.004Z","type":"session_meta","payload":{"id":"child-session","session_id":"child-session","parent_thread_id":"parent-session","timestamp":"2026-07-01T01:02:03.004Z","cwd":"/private/project","source":{"subagent":{"other":"review"}}}}` + "\n"
	codexTestTurn   = `{"timestamp":"2026-07-01T01:03:00Z","type":"turn_context","payload":{"model":"gpt-test","turn_id":"turn-1","cwd":"/private/project"}}` + "\n"
	codexTestUsage1 = `{"timestamp":"2026-07-01T01:04:00.123456Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":30,"cache_write_input_tokens":7,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":120}}}}` + "\n"
	codexTestUsage2 = `{"timestamp":"2026-07-01T01:05:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":40,"cached_input_tokens":10,"cache_write_input_tokens":2,"output_tokens":8,"reasoning_output_tokens":3,"total_tokens":48}}}}` + "\n"
)

func TestIngestCodexReportedUsageAndRelationship(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+codexTestUsage1)

	result, err := ingestCodex(context.Background(), repo, home)
	if err != nil {
		t.Fatalf("ingestCodex() error = %v", err)
	}
	if result.inserted != 1 || result.updated != 1 || result.skipped != 0 {
		t.Fatalf("result = %+v, want inserted=1 updated=1 skipped=0", result)
	}

	var session SessionRecord
	var subagent int
	err = repo.db.QueryRow(`SELECT id, source_session_id, parent_session_id, project, title, model, source_path_hash, is_subagent, measurement FROM sessions`).Scan(
		&session.ID, &session.SourceSessionID, &session.ParentSessionID, &session.Project, &session.Title, &session.Model, &session.SourcePathHash, &subagent, &session.Measurement,
	)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != codexSessionID("child-session") || session.SourceSessionID != "child-session" {
		t.Fatalf("session identity = %q/%q", session.ID, session.SourceSessionID)
	}
	if session.ParentSessionID != codexSessionID("parent-session") || subagent != 1 {
		t.Fatalf("relationship = parent %q subagent %d", session.ParentSessionID, subagent)
	}
	if session.Project != codexHash("/private/project") || session.Project == "/private/project" {
		t.Fatalf("project was not safely hashed: %q", session.Project)
	}
	if session.SourcePathHash != codexHash(path) || session.SourcePathHash == path {
		t.Fatalf("source path was not safely hashed: %q", session.SourcePathHash)
	}
	if session.Title != "" || session.Model != "gpt-test" || session.Measurement != coredb.MeasurementReported {
		t.Fatalf("unexpected session metadata: %+v", session)
	}
	var checkpointPath string
	if err := repo.db.QueryRow(`SELECT path FROM source_checkpoints`).Scan(&checkpointPath); err != nil {
		t.Fatal(err)
	}
	if checkpointPath != codexHash(path) || checkpointPath == path {
		t.Fatalf("checkpoint path was not safely hashed: %q", checkpointPath)
	}

	var tokens coredb.TokenCounts
	var model, turnID, measurement string
	var occurredAt, sourceOffset int64
	err = repo.db.QueryRow(`SELECT occurred_at_ms,turn_id,model,input_uncached,cache_read,cache_write,output_tokens,reasoning_output,total_tokens,measurement,source_offset FROM usage_events`).Scan(
		&occurredAt, &turnID, &model, &tokens.InputUncached, &tokens.CacheRead, &tokens.CacheWrite, &tokens.Output, &tokens.Reasoning, &tokens.Total, &measurement, &sourceOffset,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := coredb.TokenCounts{InputUncached: 63, CacheRead: 30, CacheWrite: 7, Output: 20, Reasoning: 5, Total: 120}
	if tokens != want || model != "gpt-test" || turnID != "turn-1" || measurement != string(coredb.MeasurementReported) {
		t.Fatalf("event = tokens %+v model %q turn %q measurement %q", tokens, model, turnID, measurement)
	}
	if occurredAt != int64(1782867840123) || sourceOffset != int64(len(codexTestMeta)+len(codexTestTurn)) {
		t.Fatalf("event provenance = timestamp %d offset %d", occurredAt, sourceOffset)
	}

	var parentID, childID string
	if err := repo.db.QueryRow(`SELECT parent_session_id,child_session_id FROM session_relationships`).Scan(&parentID, &childID); err != nil {
		t.Fatal(err)
	}
	if parentID != codexSessionID("parent-session") || childID != codexSessionID("child-session") {
		t.Fatalf("stored relationship = %q -> %q", parentID, childID)
	}
}

func TestIngestCodexCheckpointIncrementalAndIdempotent(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+codexTestUsage1)
	ctx := context.Background()

	if _, err := ingestCodex(ctx, repo, home); err != nil {
		t.Fatal(err)
	}
	result, err := ingestCodex(ctx, repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.skipped != 1 || result.inserted != 0 {
		t.Fatalf("unchanged result = %+v", result)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(codexTestUsage2); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = ingestCodex(ctx, repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.inserted != 2 {
		t.Fatalf("incremental result = %+v", result)
	}

	var count int
	var input, cacheRead, output, total int64
	if err := repo.db.QueryRow(`SELECT COUNT(*),SUM(input_uncached),SUM(cache_read),SUM(output_tokens),SUM(total_tokens) FROM usage_events`).Scan(&count, &input, &cacheRead, &output, &total); err != nil {
		t.Fatal(err)
	}
	if count != 2 || input != 91 || cacheRead != 40 || output != 28 || total != 168 {
		t.Fatalf("aggregate = count %d input %d cache %d output %d total %d", count, input, cacheRead, output, total)
	}
}

func TestIngestCodexDeduplicatesRepeatedCumulativeSnapshots(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	usage := `{"timestamp":"2026-07-01T01:04:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}` + "\n"
	repeated := `{"timestamp":"2026-07-01T01:04:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}` + "\n"
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+usage+repeated)

	if _, err := ingestCodex(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	var count int
	var total int64
	if err := repo.db.QueryRow(`SELECT COUNT(*), SUM(total_tokens) FROM usage_events`).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 1 || total != 12 {
		t.Fatalf("aggregate = count %d total %d, want count 1 total 12", count, total)
	}
}

func TestIngestCodexSkipsCopiedParentPrefixInSubagentRollout(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions", "2026", "07", "01")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	parentMeta := `{"timestamp":"2026-07-01T01:00:00Z","type":"session_meta","payload":{"id":"parent-session","timestamp":"2026-07-01T01:00:00Z","cwd":"/private/project"}}` + "\n"
	parentTurn := `{"timestamp":"2026-07-01T01:01:00Z","type":"turn_context","payload":{"model":"gpt-parent","turn_id":"parent-turn"}}` + "\n"
	parentUsage := `{"timestamp":"2026-07-01T01:02:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}` + "\n"
	writeCodexTestFile(t, filepath.Join(root, "rollout-parent.jsonl"), parentMeta+parentTurn+parentUsage)

	childMeta := `{"timestamp":"2026-07-01T02:00:00Z","type":"session_meta","payload":{"id":"child-session","parent_thread_id":"parent-session","forked_from_id":"parent-session","thread_source":"subagent","timestamp":"2026-07-01T02:00:00Z","cwd":"/private/project"}}` + "\n"
	childTurn := `{"timestamp":"2026-07-01T02:01:00Z","type":"turn_context","payload":{"model":"gpt-child","turn_id":"child-turn"}}` + "\n"
	boundary := `{"timestamp":"2026-07-01T02:01:01Z","type":"inter_agent_communication_metadata","payload":{"trigger_turn":true}}` + "\n"
	childUsage := `{"timestamp":"2026-07-01T02:02:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":4,"total_tokens":24}}}}` + "\n"
	writeCodexTestFile(t, filepath.Join(root, "rollout-child.jsonl"), childMeta+parentMeta+parentTurn+parentUsage+childTurn+boundary+childUsage)

	if _, err := ingestCodex(context.Background(), repo, home); err != nil {
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
	if sessions != 2 || events != 2 || total != 36 {
		t.Fatalf("aggregate sessions=%d events=%d total=%d, want 2/2/36", sessions, events, total)
	}

	var parentID string
	var subagent int
	var model string
	if err := repo.db.QueryRow(`SELECT parent_session_id,is_subagent,model FROM sessions WHERE source_session_id='child-session'`).Scan(&parentID, &subagent, &model); err != nil {
		t.Fatal(err)
	}
	if parentID != codexSessionID("parent-session") || subagent != 1 || model != "gpt-child" {
		t.Fatalf("child relationship parent=%q subagent=%d model=%q", parentID, subagent, model)
	}
}

func TestIngestCodexCopiedParentPrefixWithoutChildBoundaryHasNoUsage(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	parentMeta := `{"timestamp":"2026-07-01T01:00:00Z","type":"session_meta","payload":{"id":"parent-session","timestamp":"2026-07-01T01:00:00Z"}}` + "\n"
	parentUsage := `{"timestamp":"2026-07-01T01:02:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}` + "\n"
	writeCodexTestFile(t, path, codexTestMeta+parentMeta+codexTestTurn+parentUsage)

	result, err := ingestCodex(context.Background(), repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.inserted != 0 || result.updated != 1 {
		t.Fatalf("result=%+v, copied parent usage should not be committed", result)
	}
	var events int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events=%d, want zero before a child trigger boundary", events)
	}
}

func TestIngestCodexContinuesAfterMalformedHistoricalRollout(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions", "2026", "07", "01")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCodexTestFile(t, filepath.Join(root, "rollout-000-bad.jsonl"), "{not-json}\n")
	writeCodexTestFile(t, filepath.Join(root, "rollout-999-good.jsonl"), codexTestMeta+codexTestTurn+codexTestUsage1)

	result, err := ingestCodex(context.Background(), repo, home)
	if err == nil {
		t.Fatal("ingestCodex() should report degraded historical input")
	}
	if result.inserted != 1 || result.updated != 1 {
		t.Fatalf("valid newer rollout was not committed: %+v", result)
	}
	var events int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("events=%d, want 1", events)
	}
}

func TestIngestCodexSameSizeRewriteReplacesSourceAtomically(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	before := strings.Replace(codexTestUsage1, `"total_tokens":120`, `"total_tokens":121`, 1)
	after := strings.Replace(before, `"total_tokens":121`, `"total_tokens":122`, 1)
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+before)
	if _, err := ingestCodex(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+after)
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := ingestCodex(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	var count int
	var total int64
	if err := repo.db.QueryRow(`SELECT COUNT(*),SUM(total_tokens) FROM usage_events`).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 1 || total != 122 {
		t.Fatalf("rewritten aggregate = count %d total %d, want count 1 total 122", count, total)
	}
}

func TestIngestCodexRejectsInvalidCountersWithoutAdvancingCheckpoint(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	path := codexTestRolloutPath(t, home)
	bad := `{"timestamp":"2026-07-01T01:04:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":11,"output_tokens":1,"total_tokens":11}}}}` + "\n"
	writeCodexTestFile(t, path, codexTestMeta+codexTestTurn+bad)

	if _, err := ingestCodex(context.Background(), repo, home); err == nil {
		t.Fatal("ingestCodex() succeeded with cached input exceeding input")
	}
	var checkpoints, events int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM source_checkpoints`).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 || events != 0 {
		t.Fatalf("failed batch committed checkpoints=%d events=%d", checkpoints, events)
	}
}

func TestCodexInputPartitionHandlesSchemaVariance(t *testing.T) {
	usage := codexTokenUsage{Input: 100, CacheRead: 20, CacheWrite: 10, Output: 20, Total: 130}
	uncached, diagnostic := usage.inputUncached()
	if uncached != 80 || diagnostic == "" {
		t.Fatalf("input partition = %d diagnostic %q", uncached, diagnostic)
	}
}

func newCodexTestRepository(t *testing.T) *Repository {
	t.Helper()
	t.Setenv("CODEX_HOME", "")
	db, err := sql.Open("sqlite", fmt.Sprintf("file:codex-%s?mode=memory&cache=shared", codexHash(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepository(db)
}

func TestResolveCodexHomeHonorsEnvironment(t *testing.T) {
	configured := t.TempDir()
	t.Setenv("CODEX_HOME", configured)
	if got := resolveCodexHome("/fallback/home"); got != configured {
		t.Fatalf("resolveCodexHome() = %q, want %q", got, configured)
	}

	t.Setenv("CODEX_HOME", "")
	want := filepath.Join("/fallback/home", ".codex")
	if got := resolveCodexHome("/fallback/home"); got != want {
		t.Fatalf("resolveCodexHome() fallback = %q, want %q", got, want)
	}
}

func TestIngestCodexIncludesConfiguredAndNativeStoresWithoutDuplicates(t *testing.T) {
	repo := newCodexTestRepository(t)
	home := t.TempDir()
	configured := t.TempDir()
	t.Setenv("CODEX_HOME", configured)

	fallbackPath := codexTestRolloutPath(t, home)
	configuredPath := filepath.Join(configured, "sessions", "2026", "07", "02", "rollout-configured.jsonl")
	if err := os.MkdirAll(filepath.Dir(configuredPath), 0o700); err != nil {
		t.Fatal(err)
	}
	fallbackMeta := strings.ReplaceAll(codexTestMeta, "child-session", "fallback-session")
	configuredMeta := strings.ReplaceAll(codexTestMeta, "child-session", "configured-session")
	writeCodexTestFile(t, fallbackPath, fallbackMeta+codexTestTurn+codexTestUsage1)
	writeCodexTestFile(t, configuredPath, configuredMeta+codexTestTurn+codexTestUsage1)

	result, err := ingestCodex(context.Background(), repo, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.inserted != 2 || result.updated != 2 {
		t.Fatalf("combined result = %+v, want two events and sessions", result)
	}

	duplicate := filepath.Join(home, ".codex", "sessions", "2026", "07", "03", "rollout-configured.jsonl")
	if err := os.MkdirAll(filepath.Dir(duplicate), 0o700); err != nil {
		t.Fatal(err)
	}
	writeCodexTestFile(t, duplicate, fallbackMeta+codexTestTurn+codexTestUsage2)
	if _, err := ingestCodex(context.Background(), repo, home); err != nil {
		t.Fatal(err)
	}
	var sessions int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 {
		t.Fatalf("sessions=%d, duplicate secondary rollout should be ignored", sessions)
	}
}

func codexTestRolloutPath(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "01", "rollout-sanitized.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCodexTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
