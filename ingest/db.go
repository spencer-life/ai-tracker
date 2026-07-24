package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
	_ "modernc.org/sqlite"
)

type Repository struct {
	db *sql.DB
}

type SessionRecord struct {
	ID, Agent, Provider, SourceSessionID, ParentSessionID     string
	Project, Title, Status, Model, SourceKind, SourcePathHash string
	StartedAtMS, UpdatedAtMS                                  int64
	EndedAtMS                                                 *int64
	IsSubagent                                                bool
	Measurement                                               coredb.Measurement
}

type UsageEvent struct {
	ID, SessionID, TurnID, Model, Provider        string
	OccurredAtMS                                  int64
	Tokens                                        coredb.TokenCounts
	Measurement                                   coredb.Measurement
	CostMicros                                    *int64
	PricingVersion, SourcePathHash, ParserVersion string
	SourceOffset                                  int64
}

type SourceCheckpoint struct {
	Path                     string
	Device, Inode            uint64
	Size, MTimeNS, Offset    int64
	ParserVersion, LastError string
	// Replace requests source-wide event reconciliation in the same
	// transaction as all replacement sessions, events, and the checkpoint.
	Replace bool
}

type Batch struct {
	Session    SessionRecord
	Events     []UsageEvent
	Checkpoint *SourceCheckpoint
}

func DataDir() (string, error) {
	if configured := os.Getenv("AIT_DATA_DIR"); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ai-tracker"), nil
}

func InitDB() (*sql.DB, error) {
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "data.db")
	dbConn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err = dbConn.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA synchronous=NORMAL; PRAGMA foreign_keys=ON;`); err != nil {
		_ = dbConn.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}

	legacy, err := tableExists(dbConn, "token_logs")
	if err != nil {
		_ = dbConn.Close()
		return nil, err
	}
	migrated, err := tableExists(dbConn, "schema_migrations")
	if err != nil {
		_ = dbConn.Close()
		return nil, err
	}
	if legacy && !migrated {
		if _, err := backupDatabase(dbConn, dir, "v1"); err != nil {
			_ = dbConn.Close()
			return nil, fmt.Errorf("backup legacy database: %w", err)
		}
	}
	if err := migrate(dbConn); err != nil {
		_ = dbConn.Close()
		return nil, err
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, statErr := os.Stat(p); statErr == nil {
			_ = os.Chmod(p, 0o600)
		}
	}
	return dbConn, nil
}

func tableExists(dbConn *sql.DB, name string) (bool, error) {
	var found string
	err := dbConn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func backupDatabase(dbConn *sql.DB, dir, label string) (string, error) {
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(backupDir, fmt.Sprintf("data-%s-%s.db", label, time.Now().UTC().Format("20060102T150405.000000000Z")))
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := dbConn.Exec("VACUUM INTO '" + escaped + "'"); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func migrate(dbConn *sql.DB) error {
	tx, err := dbConn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	schema := `
	CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at_ms INTEGER NOT NULL);
	CREATE TABLE IF NOT EXISTS sessions(
		id TEXT PRIMARY KEY, agent TEXT NOT NULL, provider TEXT NOT NULL,
		source_session_id TEXT NOT NULL, parent_session_id TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
		started_at_ms INTEGER, updated_at_ms INTEGER NOT NULL, ended_at_ms INTEGER,
		status TEXT NOT NULL DEFAULT 'unknown', is_subagent INTEGER NOT NULL DEFAULT 0,
		model TEXT NOT NULL DEFAULT '', measurement TEXT NOT NULL,
		source_kind TEXT NOT NULL, source_path_hash TEXT NOT NULL,
		UNIQUE(agent, source_session_id)
	);
	CREATE TABLE IF NOT EXISTS usage_events(
		id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		occurred_at_ms INTEGER NOT NULL, turn_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL,
		input_uncached INTEGER NOT NULL DEFAULT 0, cache_read INTEGER NOT NULL DEFAULT 0,
		cache_write INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_output INTEGER NOT NULL DEFAULT 0, total_tokens INTEGER NOT NULL DEFAULT 0,
		measurement TEXT NOT NULL, cost_micros INTEGER,
		pricing_version TEXT NOT NULL DEFAULT '', source_path_hash TEXT NOT NULL,
		source_offset INTEGER NOT NULL DEFAULT 0, parser_version TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS session_relationships(
		parent_session_id TEXT NOT NULL, child_session_id TEXT NOT NULL,
		relation TEXT NOT NULL DEFAULT 'subagent', agent TEXT NOT NULL,
		PRIMARY KEY(parent_session_id, child_session_id, relation)
	);
	CREATE TABLE IF NOT EXISTS source_checkpoints(
		path TEXT PRIMARY KEY, device INTEGER NOT NULL DEFAULT 0, inode INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL, committed_offset INTEGER NOT NULL,
		parser_version TEXT NOT NULL, last_success_ms INTEGER, last_error TEXT NOT NULL DEFAULT '',
		last_seen_ms INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sync_runs(
		id INTEGER PRIMARY KEY AUTOINCREMENT, started_at_ms INTEGER NOT NULL,
		finished_at_ms INTEGER, status TEXT NOT NULL, inserted_count INTEGER NOT NULL DEFAULT 0,
		updated_count INTEGER NOT NULL DEFAULT 0, skipped_count INTEGER NOT NULL DEFAULT 0,
		error_count INTEGER NOT NULL DEFAULT 0, diagnostics_json TEXT NOT NULL DEFAULT '[]'
	);
	CREATE INDEX IF NOT EXISTS idx_usage_time ON usage_events(occurred_at_ms);
	CREATE INDEX IF NOT EXISTS idx_usage_session_time ON usage_events(session_id, occurred_at_ms);
	CREATE INDEX IF NOT EXISTS idx_usage_provider_time ON usage_events(provider, occurred_at_ms);
	CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at_ms DESC, id DESC);
	INSERT OR IGNORE INTO schema_migrations(version, applied_at_ms) VALUES(2, unixepoch('subsec') * 1000);
	`
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("create v2 schema: %w", err)
	}
	return tx.Commit()
}

func NewRepository(dbConn *sql.DB) *Repository { return &Repository{db: dbConn} }
func (r *Repository) GetDB() *sql.DB           { return r.db }
func (r *Repository) Close() error {
	if r.db == nil {
		return nil
	}
	return r.db.Close()
}
func (r *Repository) Init() error { return nil }

func (r *Repository) Backup(label string) (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return backupDatabase(r.db, dir, label)
}

func (r *Repository) BeginSync(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO sync_runs(started_at_ms,status) VALUES(?, 'running')`, time.Now().UTC().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) FinishSync(ctx context.Context, id int64, status string, inserted, updated, skipped, errorCount int64, diagnostics []string) error {
	b, err := json.Marshal(diagnostics)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE sync_runs SET finished_at_ms=?,status=?,inserted_count=?,updated_count=?,skipped_count=?,error_count=?,diagnostics_json=? WHERE id=?`,
		time.Now().UTC().UnixMilli(), status, inserted, updated, skipped, errorCount, string(b), id)
	return err
}

func (r *Repository) ApplyBatch(ctx context.Context, batch Batch) (inserted, updated int64, err error) {
	return r.ApplyBatches(ctx, []Batch{batch})
}

// ApplyBatches commits every session from one source atomically. A replacement
// checkpoint deletes prior events for that source before inserting the complete
// parsed snapshot, so a failed reparse cannot advance the checkpoint or leave a
// partially replaced multi-session file.
func (r *Repository) ApplyBatches(ctx context.Context, batches []Batch) (inserted, updated int64, err error) {
	if len(batches) == 0 {
		return 0, 0, errors.New("empty source batch")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var checkpoint *SourceCheckpoint
	for i := range batches {
		if batches[i].Checkpoint == nil {
			continue
		}
		if checkpoint != nil {
			return 0, 0, errors.New("multiple checkpoints in one source batch")
		}
		checkpoint = batches[i].Checkpoint
	}
	if cp := checkpoint; cp != nil {
		var oldDevice, oldInode uint64
		var oldSize, oldMTime, oldOffset int64
		var oldVersion string
		cpErr := tx.QueryRowContext(ctx, `SELECT device,inode,size,mtime_ns,committed_offset,parser_version FROM source_checkpoints WHERE path=?`, cp.Path).Scan(&oldDevice, &oldInode, &oldSize, &oldMTime, &oldOffset, &oldVersion)
		if cpErr != nil && !errors.Is(cpErr, sql.ErrNoRows) {
			return 0, 0, cpErr
		}
		identityChanged := (oldDevice != 0 && cp.Device != 0 && oldDevice != cp.Device) || (oldInode != 0 && cp.Inode != 0 && oldInode != cp.Inode)
		sameSizeRewrite := oldSize == cp.Size && oldMTime != cp.MTimeNS
		if cpErr == nil && (cp.Replace || oldVersion != cp.ParserVersion || identityChanged || oldSize > cp.Size || oldOffset > cp.Size || sameSizeRewrite) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM usage_events WHERE source_path_hash=?`, cp.Path); err != nil {
				return 0, 0, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM session_relationships WHERE child_session_id IN (SELECT id FROM sessions WHERE source_path_hash=?) OR parent_session_id IN (SELECT id FROM sessions WHERE source_path_hash=?)`, cp.Path, cp.Path); err != nil {
				return 0, 0, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE source_path_hash=?`, cp.Path); err != nil {
				return 0, 0, err
			}
		}
	}
	for _, batch := range batches {
		batch.Checkpoint = nil
		batchInserted, batchUpdated, applyErr := applyBatchTx(ctx, tx, batch)
		if applyErr != nil {
			return 0, 0, applyErr
		}
		inserted += batchInserted
		updated += batchUpdated
	}
	if cp := checkpoint; cp != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO source_checkpoints(path,device,inode,size,mtime_ns,committed_offset,parser_version,last_success_ms,last_error,last_seen_ms) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET device=excluded.device,inode=excluded.inode,size=excluded.size,mtime_ns=excluded.mtime_ns,committed_offset=excluded.committed_offset,parser_version=excluded.parser_version,last_success_ms=excluded.last_success_ms,last_error='',last_seen_ms=excluded.last_seen_ms`,
			cp.Path, cp.Device, cp.Inode, cp.Size, cp.MTimeNS, cp.Offset, cp.ParserVersion, time.Now().UTC().UnixMilli(), "", time.Now().UTC().UnixMilli())
		if err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return inserted, updated, nil
}

func applyBatchTx(ctx context.Context, tx *sql.Tx, batch Batch) (inserted, updated int64, err error) {
	s := batch.Session
	if s.ID == "" || s.Agent == "" || s.SourceSessionID == "" {
		return 0, 0, errors.New("invalid session identity")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,agent,provider,source_session_id,parent_session_id,project,title,started_at_ms,updated_at_ms,ended_at_ms,status,is_subagent,model,measurement,source_kind,source_path_hash)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,parent_session_id=excluded.parent_session_id,project=excluded.project,title=excluded.title,started_at_ms=COALESCE(excluded.started_at_ms,sessions.started_at_ms),updated_at_ms=MAX(excluded.updated_at_ms,sessions.updated_at_ms),ended_at_ms=excluded.ended_at_ms,status=excluded.status,is_subagent=excluded.is_subagent,model=excluded.model,measurement=excluded.measurement,source_kind=excluded.source_kind,source_path_hash=excluded.source_path_hash`,
		s.ID, s.Agent, s.Provider, s.SourceSessionID, s.ParentSessionID, s.Project, s.Title, nullZero(s.StartedAtMS), s.UpdatedAtMS, s.EndedAtMS, s.Status, boolInt(s.IsSubagent), s.Model, s.Measurement, s.SourceKind, s.SourcePathHash)
	if err != nil {
		return 0, 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		updated++
	}
	if s.ParentSessionID != "" {
		_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO session_relationships(parent_session_id,child_session_id,relation,agent) VALUES(?,?,?,?)`, s.ParentSessionID, s.ID, "subagent", s.Agent)
		if err != nil {
			return 0, 0, err
		}
	}
	for _, event := range batch.Events {
		if event.CostMicros == nil && event.Measurement != coredb.MeasurementEstimated {
			event.CostMicros, event.PricingVersion = CostFor(event.Model, event.Tokens)
		}
		res, err = tx.ExecContext(ctx, `INSERT INTO usage_events(id,session_id,occurred_at_ms,turn_id,model,provider,input_uncached,cache_read,cache_write,output_tokens,reasoning_output,total_tokens,measurement,cost_micros,pricing_version,source_path_hash,source_offset,parser_version)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id,occurred_at_ms=excluded.occurred_at_ms,turn_id=excluded.turn_id,model=excluded.model,provider=excluded.provider,input_uncached=excluded.input_uncached,cache_read=excluded.cache_read,cache_write=excluded.cache_write,output_tokens=excluded.output_tokens,reasoning_output=excluded.reasoning_output,total_tokens=excluded.total_tokens,measurement=excluded.measurement,cost_micros=excluded.cost_micros,pricing_version=excluded.pricing_version,source_path_hash=excluded.source_path_hash,source_offset=excluded.source_offset,parser_version=excluded.parser_version`,
			event.ID, event.SessionID, event.OccurredAtMS, event.TurnID, event.Model, event.Provider, event.Tokens.InputUncached, event.Tokens.CacheRead, event.Tokens.CacheWrite, event.Tokens.Output, event.Tokens.Reasoning, event.Tokens.Total, event.Measurement, event.CostMicros, event.PricingVersion, event.SourcePathHash, event.SourceOffset, event.ParserVersion)
		if err != nil {
			return 0, 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	return inserted, updated, nil
}

func (r *Repository) Checkpoint(ctx context.Context, path string) (SourceCheckpoint, bool, error) {
	var cp SourceCheckpoint
	err := r.db.QueryRowContext(ctx, `SELECT path,device,inode,size,mtime_ns,committed_offset,parser_version,last_error FROM source_checkpoints WHERE path=?`, path).Scan(&cp.Path, &cp.Device, &cp.Inode, &cp.Size, &cp.MTimeNS, &cp.Offset, &cp.ParserVersion, &cp.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return cp, false, nil
	}
	return cp, err == nil, err
}

func (r *Repository) ClearAll(ctx context.Context) error {
	legacyLogs, _ := tableExists(r.db, "token_logs")
	legacyCursors, _ := tableExists(r.db, "file_cursors")
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"usage_events", "session_relationships", "sessions", "source_checkpoints", "sync_runs"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	if legacyLogs {
		if _, err := tx.ExecContext(ctx, "DELETE FROM token_logs"); err != nil {
			return err
		}
	}
	if legacyCursors {
		if _, err := tx.ExecContext(ctx, "DELETE FROM file_cursors"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func nullZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
