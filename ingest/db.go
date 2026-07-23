package ingest

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	`
	_, err = db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to create schema: %v", err)
	}

	return db, nil
}
