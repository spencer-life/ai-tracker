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

	schema := `
	CREATE TABLE IF NOT EXISTS token_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent TEXT,
		timestamp DATETIME,
		model TEXT,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cost REAL
	);
	`
	_, err = db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("failed to create schema: %v", err)
	}

	return db, nil
}
