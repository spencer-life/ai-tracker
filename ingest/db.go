package ingest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spencer-life/ai-tracker/internal/db"

	_ "modernc.org/sqlite"
)

type TokenLog struct {
	ID        int
	Agent     string
	Timestamp time.Time
	Model     string
	Input     int
	Output    int
	Cost      float64
	LogHash   string
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func InitDB() (*sql.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(home, ".config", "ai-tracker", "data.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA busy_timeout=5000;
		PRAGMA synchronous=NORMAL;
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set pragmas: %v", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS token_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent TEXT,
		timestamp DATETIME,
		model TEXT,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cost REAL,
		log_hash TEXT UNIQUE
	);
	CREATE INDEX IF NOT EXISTS idx_timestamp ON token_logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_agent ON token_logs(agent);

	CREATE TABLE IF NOT EXISTS file_cursors (
		filepath TEXT UNIQUE PRIMARY KEY,
		last_read_offset INTEGER
	);
	`
	_, err = db.Exec(schema)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %v", err)
	}

	return db, nil
}

func (r *Repository) GetCursor(filepath string) int64 {
	var offset int64
	err := r.db.QueryRow("SELECT last_read_offset FROM file_cursors WHERE filepath = ?", filepath).Scan(&offset)
	if err != nil {
		return 0
	}
	return offset
}

func (r *Repository) UpdateCursor(filepath string, offset int64) error {
	_, err := r.db.Exec("INSERT INTO file_cursors (filepath, last_read_offset) VALUES (?, ?) ON CONFLICT(filepath) DO UPDATE SET last_read_offset = ?", filepath, offset, offset)
	return err
}

func (r *Repository) InsertLog(agent, model string, timestamp time.Time, inTokens, outTokens int, cost float64, hash string) error {
	query := `INSERT OR IGNORE INTO token_logs (agent, timestamp, model, input_tokens, output_tokens, cost, log_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := r.db.Exec(query, agent, timestamp, model, inTokens, outTokens, cost, hash)
	return err
}



func (r *Repository) GetAgentStats() ([]db.AgentStats, error) {
	rows, err := r.db.Query("SELECT agent, (SELECT model FROM token_logs WHERE agent = t.agent ORDER BY timestamp DESC LIMIT 1), SUM(input_tokens), SUM(output_tokens), SUM(cost), COUNT(id) FROM token_logs t GROUP BY agent")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []db.AgentStats
	for rows.Next() {
		var s db.AgentStats
		var model sql.NullString
		if err := rows.Scan(&s.Name, &model, &s.InputTokens, &s.OutputTokens, &s.Cost, &s.Jobs); err == nil {
			if model.Valid {
				s.Model = model.String
			}
			stats = append(stats, s)
		}
	}
	return stats, nil
}

func (r *Repository) GetRecentLogs(limit int) ([]string, error) {
	rows, err := r.db.Query("SELECT agent, timestamp, model, input_tokens, output_tokens, cost FROM token_logs ORDER BY timestamp DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []string
	for rows.Next() {
		var agent, model string
		var ts time.Time
		var inT, outT int
		var cost float64
		if err := rows.Scan(&agent, &ts, &model, &inT, &outT, &cost); err == nil {
			logs = append(logs, fmt.Sprintf("%s [%s] IN:%d OUT:%d COST:$%.4f", ts.Format(time.RFC3339), agent, inT, outT, cost))
		}
	}
	return logs, nil
}

func (r *Repository) Init() error {
    return nil // Handled by InitDB
}

func (r *Repository) Close() error {
    if r.db != nil {
        return r.db.Close()
    }
    return nil
}

func (r *Repository) GetDB() *sql.DB {
	return r.db
}
