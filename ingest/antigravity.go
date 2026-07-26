package ingest

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
	_ "modernc.org/sqlite"
)

const antigravityParserVersion = "antigravity-v2.1"

type antigravitySession struct {
	record SessionRecord
}

// antigravityTranscriptRow intentionally models only the fields necessary to
// derive an opt-in estimate. Text is counted and discarded; it is never put in
// a SessionRecord, UsageEvent, diagnostic, or stable identifier.
type antigravityTranscriptRow struct {
	Source    string `json:"source"`
	Role      string `json:"role"`
	Timestamp any    `json:"timestamp"`
	Content   string `json:"content"`
	Thinking  string `json:"thinking"`
}

// ingestAntigravity imports exact conversation metadata by default. Agy does
// not expose authoritative token accounting in its local SQLite stores, so the
// default path creates sessions without usage events. Character-derived token
// estimates are available only through includeEstimates.
func ingestAntigravity(ctx context.Context, repo *Repository, home string, includeEstimates bool) (sourceResult, error) {
	var result sourceResult
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	sessions := make(map[string]antigravitySession)

	summaryPath := filepath.Join(root, "conversation_summaries.db")
	if info, err := os.Stat(summaryPath); err == nil {
		loaded, diagnostics, loadErr := readAntigravitySummaries(ctx, summaryPath)
		result.diagnostics = append(result.diagnostics, diagnostics...)
		if loadErr != nil {
			return result, fmt.Errorf("read Antigravity conversation summaries: %w", loadErr)
		}
		for sourceID, session := range loaded {
			sessions[sourceID] = session
		}
		unchanged, checkpointErr := antigravityUnchanged(ctx, repo, summaryPath, info)
		if checkpointErr != nil {
			return result, checkpointErr
		}
		if unchanged {
			result.skipped++
		} else {
			keys := sortedAntigravityKeys(loaded)
			batches := make([]Batch, 0, len(keys))
			for index, sourceID := range keys {
				batch := Batch{Session: loaded[sourceID].record}
				if index == len(keys)-1 {
					batch.Checkpoint = antigravityCheckpoint(summaryPath, info)
					batch.Checkpoint.Replace = true
				}
				batches = append(batches, batch)
			}
			if len(batches) > 0 {
				inserted, updated, applyErr := repo.ApplyBatches(ctx, batches)
				if applyErr != nil {
					return result, fmt.Errorf("commit Antigravity summary sessions: %w", applyErr)
				}
				result.inserted += inserted
				result.updated += updated
			}
			if len(keys) == 0 {
				result.diagnostics = append(result.diagnostics, "agy: summary database contained no usable sessions")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("stat Antigravity summary database: %w", err)
	}

	fallbackResult, fallbackErr := ingestAntigravityConversationDBs(ctx, repo, filepath.Join(root, "conversations"), sessions)
	result.merge(fallbackResult)
	if fallbackErr != nil {
		return result, fallbackErr
	}

	if includeEstimates {
		estimateResult, estimateErr := ingestAntigravityTranscripts(ctx, repo, filepath.Join(root, "brain"), sessions)
		result.merge(estimateResult)
		if estimateErr != nil {
			return result, estimateErr
		}
	}
	return result, nil
}

func readAntigravitySummaries(ctx context.Context, path string) (map[string]antigravitySession, []string, error) {
	db, err := openAntigravityReadOnly(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = db.Close() }()
	columns, exists, err := antigravityColumns(ctx, db, "conversation_summaries")
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return map[string]antigravitySession{}, []string{"agy: summary database lacks conversation_summaries table"}, nil
	}
	if !columns["conversation_id"] {
		return map[string]antigravitySession{}, []string{"agy: summary schema lacks conversation identity"}, nil
	}

	selectExpr := func(column, fallback string) string {
		if columns[column] {
			return column
		}
		return fallback
	}
	query := `SELECT conversation_id, ` +
		selectExpr("last_modified_time", "NULL") + `, ` +
		selectExpr("last_user_input_time", "NULL") + `, ` +
		selectExpr("status", "''") + `, ` +
		selectExpr("project_id", "''") + `, ` +
		selectExpr("parent_conversation_id", "''") + `, ` +
		selectExpr("nesting_depth", "0") + `, ` +
		selectExpr("killed", "0") +
		` FROM conversation_summaries ORDER BY conversation_id`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	pathHash := hashString("agy-source\x00" + path)
	sessions := make(map[string]antigravitySession)
	var diagnostics []string
	for rows.Next() {
		var sourceValue, modifiedValue, inputValue, statusValue, projectValue, parentValue, nestingValue, killedValue any
		if err := rows.Scan(&sourceValue, &modifiedValue, &inputValue, &statusValue, &projectValue, &parentValue, &nestingValue, &killedValue); err != nil {
			return nil, diagnostics, err
		}
		sourceID := antigravityString(sourceValue)
		if sourceID == "" {
			diagnostics = append(diagnostics, "agy: skipped summary row without conversation identity")
			continue
		}
		modifiedAt, modifiedOK := antigravityTime(modifiedValue)
		inputAt, inputOK := antigravityTime(inputValue)
		updatedAt, ok := modifiedAt, modifiedOK
		if inputOK && (!ok || inputAt.After(updatedAt)) {
			updatedAt, ok = inputAt, true
		}
		if !ok {
			diagnostics = append(diagnostics, "agy: skipped summary session without a valid source timestamp")
			continue
		}
		status := antigravityString(statusValue)
		projectID := antigravityString(projectValue)
		parentID := antigravityString(parentValue)
		nestingDepth := intValue(nestingValue)
		killed := antigravityBool(killedValue)
		parentSessionID := ""
		if parentID != "" {
			parentSessionID = antigravitySessionID(parentID)
		}
		project := ""
		if projectID != "" {
			project = "project-" + stableID("agy-project", projectID)[:16]
		}
		record := SessionRecord{
			ID: antigravitySessionID(sourceID), Agent: "agy", Provider: "google",
			SourceSessionID: sourceID, ParentSessionID: parentSessionID,
			Project: project, Status: antigravityStatus(status, killed),
			UpdatedAtMS: updatedAt.UnixMilli(), IsSubagent: parentID != "" || nestingDepth > 0,
			Measurement: coredb.MeasurementReported, SourceKind: "antigravity_summary_db",
			SourcePathHash: pathHash,
		}
		sessions[sourceID] = antigravitySession{record: record}
	}
	if err := rows.Err(); err != nil {
		return nil, diagnostics, err
	}
	for _, optional := range []string{"last_modified_time", "status", "project_id", "parent_conversation_id", "nesting_depth"} {
		if !columns[optional] {
			diagnostics = append(diagnostics, "agy: summary schema lacks optional "+optional+" metadata")
		}
	}
	return sessions, diagnostics, nil
}

func ingestAntigravityConversationDBs(ctx context.Context, repo *Repository, root string, known map[string]antigravitySession) (sourceResult, error) {
	var result sourceResult
	paths, err := filepath.Glob(filepath.Join(root, "*.db"))
	if err != nil {
		return result, fmt.Errorf("locate Antigravity conversation databases: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		filenameID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if filenameID == "" {
			result.diagnostics = append(result.diagnostics, "agy: skipped conversation database without identity")
			continue
		}
		if _, exists := known[filenameID]; exists {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return result, fmt.Errorf("stat Antigravity conversation database: %w", statErr)
		}
		sourceID, schemaDiagnostics, schemaErr := validateAntigravityConversationDB(ctx, path)
		result.diagnostics = append(result.diagnostics, schemaDiagnostics...)
		if schemaErr != nil {
			return result, fmt.Errorf("inspect Antigravity conversation database: %w", schemaErr)
		}
		if sourceID == "" {
			result.skipped++
			continue
		}
		if _, exists := known[sourceID]; exists {
			continue
		}
		record := SessionRecord{
			ID: antigravitySessionID(sourceID), Agent: "agy", Provider: "google",
			SourceSessionID: sourceID, UpdatedAtMS: info.ModTime().UTC().UnixMilli(),
			Status: "unknown", Measurement: coredb.MeasurementDerived,
			SourceKind:     "antigravity_conversation_db",
			SourcePathHash: hashString("agy-source\x00" + path),
		}
		known[sourceID] = antigravitySession{record: record}
		unchanged, checkpointErr := antigravityUnchanged(ctx, repo, path, info)
		if checkpointErr != nil {
			return result, checkpointErr
		}
		if unchanged {
			result.skipped++
			continue
		}
		inserted, updated, applyErr := repo.ApplyBatch(ctx, Batch{Session: record, Checkpoint: antigravityCheckpoint(path, info)})
		if applyErr != nil {
			return result, fmt.Errorf("commit Antigravity conversation session: %w", applyErr)
		}
		result.inserted += inserted
		result.updated += updated
	}
	return result, nil
}

// validateAntigravityConversationDB reads only table and column metadata plus a
// row count. In particular, it never selects the metadata, payload, render,
// parent-reference, or other opaque blob columns used by Antigravity.
func validateAntigravityConversationDB(ctx context.Context, path string) (string, []string, error) {
	db, err := openAntigravityReadOnly(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = db.Close() }()
	trajectoryColumns, hasTrajectory, err := antigravityColumns(ctx, db, "trajectory_meta")
	if err != nil {
		return "", nil, err
	}
	_, hasSteps, err := antigravityColumns(ctx, db, "steps")
	if err != nil {
		return "", nil, err
	}
	if !hasTrajectory || !trajectoryColumns["trajectory_id"] {
		return "", []string{"agy: conversation schema lacks safe trajectory identity metadata"}, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT trajectory_id FROM trajectory_meta WHERE trajectory_id IS NOT NULL AND trajectory_id<>'' ORDER BY trajectory_id LIMIT 2`)
	if err != nil {
		return "", nil, err
	}
	var identities []string
	for rows.Next() {
		var identity string
		if err := rows.Scan(&identity); err != nil {
			_ = rows.Close()
			return "", nil, err
		}
		identities = append(identities, identity)
	}
	if err := rows.Close(); err != nil {
		return "", nil, err
	}
	if len(identities) == 0 {
		return "", []string{"agy: conversation database has no safe trajectory identity"}, nil
	}
	if len(identities) > 1 {
		return "", []string{"agy: conversation database has multiple trajectory identities"}, nil
	}
	if !hasSteps {
		return identities[0], []string{"agy: conversation schema lacks safe step metadata"}, nil
	}
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steps`).Scan(&count); err != nil {
		return "", nil, err
	}
	return identities[0], nil, nil
}

func ingestAntigravityTranscripts(ctx context.Context, repo *Repository, root string, known map[string]antigravitySession) (sourceResult, error) {
	var result sourceResult
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.diagnostics = append(result.diagnostics, "agy: unable to inspect a transcript entry")
			return nil
		}
		if !entry.IsDir() && entry.Name() == "transcript.jsonl" {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("scan Antigravity transcripts: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fileResult, fileErr := ingestAntigravityTranscript(ctx, repo, path, known)
		result.merge(fileResult)
		if fileErr != nil {
			return result, fileErr
		}
	}
	return result, nil
}

func ingestAntigravityTranscript(ctx context.Context, repo *Repository, path string, known map[string]antigravitySession) (sourceResult, error) {
	var result sourceResult
	sourceID := filepath.Base(filepath.Dir(path))
	if sourceID == "" || sourceID == "." {
		result.diagnostics = append(result.diagnostics, "agy: skipped transcript without conversation identity")
		return result, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return result, fmt.Errorf("stat Antigravity transcript: %w", err)
	}
	unchanged, err := antigravityUnchanged(ctx, repo, path, info)
	if err != nil {
		return result, err
	}
	if unchanged {
		result.skipped++
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("open Antigravity transcript: %w", err)
	}
	defer func() { _ = file.Close() }()

	pathHash := hashString("agy-source\x00" + path)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	scanner.Split(antigravityCompleteLines)
	var offset int64
	var firstAt, lastAt int64
	var events []UsageEvent
	var malformed, missingTime, unknownDirection int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		lineStart := offset
		line := scanner.Bytes()
		offset += int64(len(line))
		if offset < info.Size() {
			offset++
		}
		var row antigravityTranscriptRow
		if err := json.Unmarshal(line, &row); err != nil {
			result.skipped++
			malformed++
		} else {
			at, ok := antigravityTime(row.Timestamp)
			if !ok {
				result.skipped++
				missingTime++
			} else if event, ok := antigravityEstimatedEvent(sourceID, pathHash, lineStart, at, row); ok {
				events = append(events, event)
				atMS := at.UnixMilli()
				if firstAt == 0 || atMS < firstAt {
					firstAt = atMS
				}
				if atMS > lastAt {
					lastAt = atMS
				}
			} else if row.Content != "" || row.Thinking != "" {
				result.skipped++
				unknownDirection++
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read Antigravity transcript (maximum row size 4 MiB): %w", err)
	}
	for count, message := range map[int64]string{
		malformed:        "agy: skipped %d malformed transcript rows",
		missingTime:      "agy: skipped %d estimated rows without valid source timestamps",
		unknownDirection: "agy: skipped %d estimated rows with unknown direction",
	} {
		if count > 0 {
			result.diagnostics = append(result.diagnostics, fmt.Sprintf(message, count))
		}
	}

	session, exists := known[sourceID]
	if !exists {
		if lastAt == 0 {
			result.diagnostics = append(result.diagnostics, "agy: transcript supplied no usable session timestamp")
			return result, nil
		}
		session = antigravitySession{record: SessionRecord{
			ID: antigravitySessionID(sourceID), Agent: "agy", Provider: "google",
			SourceSessionID: sourceID, StartedAtMS: firstAt, UpdatedAtMS: lastAt,
			Status: "unknown", Measurement: coredb.MeasurementEstimated,
			SourceKind: "antigravity_transcript", SourcePathHash: pathHash,
		}}
		known[sourceID] = session
	}
	if session.record.StartedAtMS == 0 && firstAt != 0 {
		session.record.StartedAtMS = firstAt
	}
	if lastAt > session.record.UpdatedAtMS {
		session.record.UpdatedAtMS = lastAt
	}
	if session.record.UpdatedAtMS == 0 {
		result.diagnostics = append(result.diagnostics, "agy: transcript session has no valid source timestamp")
		return result, nil
	}
	inserted, updated, err := applyAntigravityEstimateBatch(ctx, repo, session.record, events, antigravityCheckpointWithOffset(path, info, offset))
	if err != nil {
		return result, fmt.Errorf("commit Antigravity estimated transcript: %w", err)
	}
	result.inserted += inserted
	result.updated += updated
	return result, nil
}

func antigravityEstimatedEvent(sourceID, pathHash string, offset int64, at time.Time, row antigravityTranscriptRow) (UsageEvent, bool) {
	characters := utf8.RuneCountInString(row.Content) + utf8.RuneCountInString(row.Thinking)
	if characters == 0 {
		return UsageEvent{}, false
	}
	estimate := int64((characters + 3) / 4)
	tokens := coredb.TokenCounts{Total: estimate}
	direction := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "model", "assistant":
			return "output"
		case "user", "human":
			return "input"
		default:
			return ""
		}
	}(row.Source)
	if direction == "" {
		direction = func(value string) string {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "model", "assistant":
				return "output"
			case "user", "human":
				return "input"
			default:
				return ""
			}
		}(row.Role)
	}
	switch direction {
	case "output":
		tokens.Output = estimate
	case "input":
		tokens.InputUncached = estimate
	default:
		return UsageEvent{}, false
	}
	return UsageEvent{
		ID:        stableID("agy-estimate", pathHash, fmt.Sprint(offset)),
		SessionID: antigravitySessionID(sourceID), Provider: "google",
		OccurredAtMS: at.UTC().UnixMilli(), Tokens: tokens,
		Measurement: coredb.MeasurementEstimated, SourcePathHash: pathHash,
		SourceOffset: offset, ParserVersion: antigravityParserVersion,
	}, true
}

func applyAntigravityEstimateBatch(ctx context.Context, repo *Repository, session SessionRecord, events []UsageEvent, checkpoint *SourceCheckpoint) (int64, int64, error) {
	_, updated, err := repo.ApplyBatch(ctx, Batch{Session: session})
	if err != nil {
		return 0, 0, err
	}
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, updated, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE source_path_hash=? AND measurement=?`, session.SourcePathHash, coredb.MeasurementEstimated); err != nil {
		// Summary-backed sessions use a different source hash from their
		// transcript, so reconcile by the event/checkpoint path instead.
		return 0, updated, err
	}
	if checkpoint != nil {
		transcriptHash := checkpoint.Path
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE source_path_hash=? AND measurement=?`, transcriptHash, coredb.MeasurementEstimated); err != nil {
			return 0, updated, err
		}
	}
	for _, event := range events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_events(id,session_id,occurred_at_ms,turn_id,model,provider,input_uncached,cache_read,cache_write,output_tokens,reasoning_output,total_tokens,measurement,cost_micros,pricing_version,source_path_hash,source_offset,parser_version)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			event.ID, event.SessionID, event.OccurredAtMS, event.TurnID, event.Model, event.Provider,
			event.Tokens.InputUncached, event.Tokens.CacheRead, event.Tokens.CacheWrite, event.Tokens.Output,
			event.Tokens.Reasoning, event.Tokens.Total, event.Measurement, event.CostMicros,
			event.PricingVersion, event.SourcePathHash, event.SourceOffset, event.ParserVersion); err != nil {
			return 0, updated, err
		}
	}
	if checkpoint != nil {
		now := time.Now().UTC().UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_checkpoints(path,device,inode,size,mtime_ns,committed_offset,parser_version,last_success_ms,last_error,last_seen_ms)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET device=excluded.device,inode=excluded.inode,size=excluded.size,mtime_ns=excluded.mtime_ns,committed_offset=excluded.committed_offset,parser_version=excluded.parser_version,last_success_ms=excluded.last_success_ms,last_error='',last_seen_ms=excluded.last_seen_ms`,
			checkpoint.Path, checkpoint.Device, checkpoint.Inode, checkpoint.Size, checkpoint.MTimeNS,
			checkpoint.Offset, checkpoint.ParserVersion, now, "", now); err != nil {
			return 0, updated, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, updated, err
	}
	return int64(len(events)), updated, nil
}

func antigravityCompleteLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index], nil
	}
	// Accept a syntactically complete final JSON object at EOF, but retain an
	// incomplete writer buffer for the next sync without checkpointing it.
	if atEOF {
		if len(data) > 0 && json.Valid(data) {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
	return 0, nil, nil
}

func antigravityColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, bool, error) {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]bool{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	// Callers pass only fixed internal table names.
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, false, err
		}
		columns[name] = true
	}
	return columns, true, rows.Err()
}

func openAntigravityReadOnly(path string) (*sql.DB, error) {
	// immutable avoids SQLite trying to create lock or shared-memory files next
	// to an agent-owned database. The connection is short-lived and reopened on
	// every sync, so each run still observes the current database snapshot.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1&_pragma=query_only(1)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func antigravityTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return time.Time{}, false
		}
		return typed.UTC(), true
	case string:
		typed = strings.TrimSpace(typed)
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00",
			"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.UTC(), true
			}
		}
		return time.Time{}, false
	case int64:
		return antigravityUnixTime(typed)
	case int:
		return antigravityUnixTime(int64(typed))
	case float64:
		return antigravityUnixTime(int64(typed))
	case []byte:
		return antigravityTime(string(typed))
	default:
		return time.Time{}, false
	}
}

func antigravityString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func antigravityBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case int:
		return typed != 0
	case float64:
		return typed != 0
	case []byte:
		return antigravityBool(string(typed))
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "y":
			return true
		}
	}
	return false
}

func antigravityUnixTime(value int64) (time.Time, bool) {
	if value <= 0 {
		return time.Time{}, false
	}
	switch {
	case value > 100_000_000_000_000_000:
		return time.Unix(0, value).UTC(), true
	case value > 100_000_000_000_000:
		return time.Unix(0, value*1_000).UTC(), true
	case value > 100_000_000_000:
		return time.UnixMilli(value).UTC(), true
	default:
		return time.Unix(value, 0).UTC(), true
	}
}

func antigravityStatus(source string, killed bool) string {
	if killed {
		return "completed"
	}
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "completed", "complete", "done", "finished", "killed", "cancelled", "canceled":
		return "completed"
	default:
		return "unknown"
	}
}

func antigravitySessionID(sourceID string) string {
	return "agy-" + stableID("agy-session", sourceID)[:32]
}

func sortedAntigravityKeys(sessions map[string]antigravitySession) []string {
	keys := make([]string, 0, len(sessions))
	for key := range sessions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func antigravityUnchanged(ctx context.Context, repo *Repository, path string, info os.FileInfo) (bool, error) {
	pathHash := hashString("agy-source\x00" + path)
	checkpoint, found, err := repo.Checkpoint(ctx, pathHash)
	if err != nil {
		return false, fmt.Errorf("read Antigravity checkpoint: %w", err)
	}
	device, inode := antigravityFileIdentity(info)
	return found && checkpoint.ParserVersion == antigravityParserVersion &&
		checkpoint.Device == device && checkpoint.Inode == inode &&
		checkpoint.Size == info.Size() && checkpoint.Offset == info.Size() &&
		checkpoint.MTimeNS == info.ModTime().UnixNano(), nil
}

func antigravityCheckpoint(path string, info os.FileInfo) *SourceCheckpoint {
	return antigravityCheckpointWithOffset(path, info, info.Size())
}

func antigravityCheckpointWithOffset(path string, info os.FileInfo, offset int64) *SourceCheckpoint {
	device, inode := antigravityFileIdentity(info)
	return &SourceCheckpoint{
		Path: hashString("agy-source\x00" + path), Device: device, Inode: inode,
		Size: info.Size(), MTimeNS: info.ModTime().UnixNano(), Offset: offset,
		ParserVersion: antigravityParserVersion,
	}
}

func antigravityFileIdentity(info os.FileInfo) (uint64, uint64) {
	return fileIdentity(info)
}
