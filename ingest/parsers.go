package ingest

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func IngestLogs() error {
	db, err := InitDB()
	if err != nil {
		return err
	}
	defer db.Close()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	agDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	fmt.Printf("Parsing %s logs...\n", agDir)
	parseAntigravityLogs(db, agDir)

	claudeDir := filepath.Join(home, ".claude")
	fmt.Printf("Parsing %s logs...\n", claudeDir)
	parseClaudeLogs(db, claudeDir)

	codexDir := filepath.Join(home, ".codex")
	fmt.Printf("Parsing %s logs...\n", codexDir)
	parseCodexLogs(db, codexDir)

	return nil
}

func parseAntigravityLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), "transcript.jsonl") {
			return nil
		}
		
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		const maxCapacity = 50 * 1024 * 1024
		buf := make([]byte, maxCapacity)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			raw := scanner.Bytes()
			hashBytes := sha256.Sum256(raw)
			hashStr := hex.EncodeToString(hashBytes[:])

			var data map[string]interface{}
			if err := json.Unmarshal(RedactSecrets(raw), &data); err != nil {
				continue
			}

			ts := extractTimestamp(data)
			inTokens, outTokens := extractTokenUsage(data)
			if inTokens > 0 || outTokens > 0 {
				cost := calculateCost("gemini-1.5-pro", float64(inTokens), float64(outTokens))
				insertLog(db, "antigravity", "gemini-1.5-pro", ts, inTokens, outTokens, cost, hashStr)
			}
		}
		return nil
	})
}

func parseClaudeLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		bytes, _ := io.ReadAll(file)
		hashBytes := sha256.Sum256(bytes)
		hashStr := hex.EncodeToString(hashBytes[:])

		var data map[string]interface{}
		if err := json.Unmarshal(RedactSecrets(bytes), &data); err == nil {
			ts := extractTimestamp(data)
			inTokens, outTokens := extractTokenUsage(data)
			if inTokens > 0 || outTokens > 0 {
				cost := calculateCost("claude-3.5-sonnet", float64(inTokens), float64(outTokens))
				insertLog(db, "claude", "claude-3.5-sonnet", ts, inTokens, outTokens, cost, hashStr)
			}
		}
		return nil
	})
}

func parseCodexLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		bytes, _ := io.ReadAll(file)
		hashBytes := sha256.Sum256(bytes)
		hashStr := hex.EncodeToString(hashBytes[:])

		var data map[string]interface{}
		if err := json.Unmarshal(RedactSecrets(bytes), &data); err == nil {
			ts := extractTimestamp(data)
			inTokens, outTokens := extractTokenUsage(data)
			if inTokens > 0 || outTokens > 0 {
				cost := calculateCost("claude-3.5-sonnet", float64(inTokens), float64(outTokens)) // Assuming Claude for Codex for now
				insertLog(db, "codex", "claude-3.5-sonnet", ts, inTokens, outTokens, cost, hashStr)
			}
		}
		return nil
	})
}

func extractTokenUsage(data interface{}) (int, int) {
	in, out := 0, 0
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			if strings.ToLower(key) == "usage" || strings.ToLower(key) == "token_usage" || strings.ToLower(key) == "metadata" {
				if usageMap, ok := val.(map[string]interface{}); ok {
					i, o := extractTokenUsage(usageMap)
					if i > 0 || o > 0 {
						return i, o
					}
				}
			}
			
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, "input") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					in = int(num)
				}
			} else if strings.Contains(keyLower, "prompt") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					in = int(num)
				}
			} else if strings.Contains(keyLower, "output") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					out = int(num)
				}
			} else if strings.Contains(keyLower, "completion") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					out = int(num)
				}
			}
		}
	}
	return in, out
}

func extractTimestamp(data map[string]interface{}) time.Time {
	if ts, ok := data["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t
		}
	}
	if ts, ok := data["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t
		}
	}
	return time.Now()
}

func calculateCost(model string, inTokens, outTokens float64) float64 {
	if model == "gemini-1.5-pro" {
		return (inTokens * 3.5 / 1000000.0) + (outTokens * 10.5 / 1000000.0)
	} else if model == "claude-3.5-sonnet" {
		return (inTokens * 3.0 / 1000000.0) + (outTokens * 15.0 / 1000000.0)
	}
	return 0
}

func insertLog(db *sql.DB, agent, model string, timestamp time.Time, inTokens, outTokens int, cost float64, hash string) {
	query := `INSERT OR IGNORE INTO token_logs (agent, timestamp, model, input_tokens, output_tokens, cost, log_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`
	db.Exec(query, agent, timestamp, model, inTokens, outTokens, cost, hash)
}
