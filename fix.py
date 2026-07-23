import os
import re

# 1. ingest/parsers.go
parsers_code = """package ingest

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
	"sync"
	"time"
)

var (
	fileModTimesMu sync.RWMutex
	fileModTimes   = make(map[string]time.Time)
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
	fmt.Printf("Parsing %s logs...\\n", agDir)
	parseAntigravityLogs(db, agDir)

	claudeDir := filepath.Join(home, ".claude")
	fmt.Printf("Parsing %s logs...\\n", claudeDir)
	parseClaudeLogs(db, claudeDir)

	codexDir := filepath.Join(home, ".codex")
	fmt.Printf("Parsing %s logs...\\n", codexDir)
	parseCodexLogs(db, codexDir)

	return nil
}

func extractModel(data map[string]interface{}, defaultModel string) string {
	if m, ok := data["model"].(string); ok && m != "" {
		return m
	}
	if req, ok := data["request"].(map[string]interface{}); ok {
		if m, ok := req["model"].(string); ok && m != "" {
			return m
		}
	}
	return defaultModel
}

func parseAntigravityLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), "transcript.jsonl") {
			return nil
		}
		
		fileModTimesMu.RLock()
		lastMod, exists := fileModTimes[path]
		fileModTimesMu.RUnlock()
		if exists && info.ModTime().Equal(lastMod) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		const maxCapacity = 50 * 1024 * 1024
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			raw := scanner.Bytes()
			hashBytes := sha256.Sum256(raw)
			hashStr := hex.EncodeToString(hashBytes[:])

			var data map[string]interface{}
			if err := json.Unmarshal(RedactSecrets(raw), &data); err != nil {
				continue
			}

			model := extractModel(data, "gemini-1.5-pro")
			ts := extractTimestamp(data)
			inTokens, outTokens := extractTokenUsage(data)
			if inTokens > 0 || outTokens > 0 {
				cost := calculateCost(model, float64(inTokens), float64(outTokens))
				insertLog(db, "antigravity", model, ts, inTokens, outTokens, cost, hashStr)
			}
		}
		
		if err := scanner.Err(); err != nil {
			fmt.Printf("Error scanning file %s: %v\\n", path, err)
		} else {
			fileModTimesMu.Lock()
			fileModTimes[path] = info.ModTime()
			fileModTimesMu.Unlock()
		}
		
		return nil
	})
}

func parseClaudeLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		fileModTimesMu.RLock()
		lastMod, exists := fileModTimes[path]
		fileModTimesMu.RUnlock()
		if exists && info.ModTime().Equal(lastMod) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		const maxCapacity = 50 * 1024 * 1024
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			raw := scanner.Bytes()
			hashBytes := sha256.Sum256(raw)
			hashStr := hex.EncodeToString(hashBytes[:])

			var data map[string]interface{}
			if err := json.Unmarshal(RedactSecrets(raw), &data); err == nil {
				model := extractModel(data, "claude-3.5-sonnet")
				ts := extractTimestamp(data)
				inTokens, outTokens := extractTokenUsage(data)
				if inTokens > 0 || outTokens > 0 {
					cost := calculateCost(model, float64(inTokens), float64(outTokens))
					insertLog(db, "claude", model, ts, inTokens, outTokens, cost, hashStr)
				}
			}
		}
		
		if err := scanner.Err(); err != nil {
			fmt.Printf("Error scanning file %s: %v\\n", path, err)
		} else {
			fileModTimesMu.Lock()
			fileModTimes[path] = info.ModTime()
			fileModTimesMu.Unlock()
		}
		return nil
	})
}

func parseCodexLogs(db *sql.DB, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		fileModTimesMu.RLock()
		lastMod, exists := fileModTimes[path]
		fileModTimesMu.RUnlock()
		if exists && info.ModTime().Equal(lastMod) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		const maxCapacity = 50 * 1024 * 1024
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, maxCapacity)

		for scanner.Scan() {
			raw := scanner.Bytes()
			hashBytes := sha256.Sum256(raw)
			hashStr := hex.EncodeToString(hashBytes[:])

			var data map[string]interface{}
			if err := json.Unmarshal(RedactSecrets(raw), &data); err == nil {
				model := extractModel(data, "claude-3.5-sonnet") // Assuming Claude for Codex for now
				ts := extractTimestamp(data)
				inTokens, outTokens := extractTokenUsage(data)
				if inTokens > 0 || outTokens > 0 {
					cost := calculateCost(model, float64(inTokens), float64(outTokens))
					insertLog(db, "codex", model, ts, inTokens, outTokens, cost, hashStr)
				}
			}
		}
		
		if err := scanner.Err(); err != nil {
			fmt.Printf("Error scanning file %s: %v\\n", path, err)
		} else {
			fileModTimesMu.Lock()
			fileModTimes[path] = info.ModTime()
			fileModTimesMu.Unlock()
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
						in += i
						out += o
						continue
					}
				}
			}
			
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, "input") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					in += int(num)
				}
			} else if strings.Contains(keyLower, "prompt") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					in += int(num)
				}
			} else if strings.Contains(keyLower, "output") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					out += int(num)
				}
			} else if strings.Contains(keyLower, "completion") && strings.Contains(keyLower, "token") {
				if num, ok := val.(float64); ok {
					out += int(num)
				}
			} else if m, ok := val.(map[string]interface{}); ok {
				i, o := extractTokenUsage(m)
				if i > 0 { in += i }
				if o > 0 { out += o }
			} else if a, ok := val.([]interface{}); ok {
				i, o := extractTokenUsage(a)
				if i > 0 { in += i }
				if o > 0 { out += o }
			}
		}
	case []interface{}:
		for _, item := range v {
			i, o := extractTokenUsage(item)
			if i > 0 { in += i }
			if o > 0 { out += o }
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
	_, err := db.Exec(query, agent, timestamp, model, inTokens, outTokens, cost, hash)
	if err != nil {
		fmt.Printf("Error inserting log: %v\\n", err)
	}
}
"""
with open('/home/mlpc/dev/ai-tracker/ingest/parsers.go', 'w') as f:
    f.write(parsers_code)

# 2. cmd/sync.go
with open('/home/mlpc/dev/ai-tracker/cmd/sync.go', 'r') as f:
    sync_code = f.read()

sync_code = sync_code.replace('''			if err := ingest.IngestLogs(); err != nil {
				log.Fatalf("Sync failed: %v", err)
			}''', '''			if err := ingest.IngestLogs(); err != nil {
				log.Printf("Sync failed: %v", err)
				if !syncWatch {
					break
				}
				time.Sleep(5 * time.Second)
				continue
			}''')
with open('/home/mlpc/dev/ai-tracker/cmd/sync.go', 'w') as f:
    f.write(sync_code)

# 3. pkg/tui/app.go
with open('/home/mlpc/dev/ai-tracker/pkg/tui/app.go', 'r') as f:
    tui_code = f.read()

tui_code = tui_code.replace('m.totalTokens += int64(120 + time.Now().Second()*5)', '')
tui_code = tui_code.replace('var totalTokens int64', 'var totalIn int64\n    var totalOut int64\n    var totalJobs int64\n    var totalTokens int64')

tui_code = tui_code.replace('db.Query("SELECT agent, SUM(input_tokens + output_tokens), SUM(cost) FROM token_logs GROUP BY agent")', 'db.Query("SELECT agent, SUM(input_tokens), SUM(output_tokens), SUM(cost), COUNT(*) FROM token_logs GROUP BY agent")')

tui_code = tui_code.replace('var tokens int64\n            var cost float64\n            if err := rows.Scan(&name, &tokens, &cost); err == nil {', 'var inT, outT int64\n            var cost float64\n            var count int64\n            if err := rows.Scan(&name, &inT, &outT, &cost, &count); err == nil {\n                tokens := inT + outT')

tui_code = tui_code.replace('totalTokens += tokens', 'totalTokens += tokens\n                totalIn += inT\n                totalOut += outT\n                totalJobs += count')

tui_code = tui_code.replace('totalTokens   int64\n\ttotalCost     float64', 'totalTokens   int64\n\ttotalIn       int64\n\ttotalOut      int64\n\ttotalJobs     int64\n\ttotalCost     float64')

tui_code = tui_code.replace('func loadDataFromDB() (int64, float64, []SubagentInfo, []string) {', 'func loadDataFromDB() (int64, int64, int64, int64, float64, []SubagentInfo, []string) {')
tui_code = tui_code.replace('return totalTokens, totalCost, agents, logs', 'return totalTokens, totalIn, totalOut, totalJobs, totalCost, agents, logs')

tui_code = tui_code.replace('tokens, cost, agents, logs := loadDataFromDB()', 'tokens, inT, outT, jobs, cost, agents, logs := loadDataFromDB()')
tui_code = tui_code.replace('totalTokens: tokens,\n\t\ttotalCost:   cost,', 'totalTokens: tokens,\n\t\ttotalIn:     inT,\n\t\ttotalOut:    outT,\n\t\ttotalJobs:   jobs,\n\t\ttotalCost:   cost,')

tui_code = tui_code.replace('m.totalTokens = tokens\n\t\t\tm.totalCost = cost', 'm.totalTokens = tokens\n\t\t\tm.totalIn = inT\n\t\t\tm.totalOut = outT\n\t\t\tm.totalJobs = jobs\n\t\t\tm.totalCost = cost')

tui_code = tui_code.replace('lipgloss.NewStyle().Foreground(ColorOverlay1).Render("Prompt: 3.1M | Comp: 1.7M"),', 'lipgloss.NewStyle().Foreground(ColorOverlay1).Render(fmt.Sprintf("Prompt: %.1fK | Comp: %.1fK", float64(m.totalIn)/1000, float64(m.totalOut)/1000)),')

tui_code = tui_code.replace('lipgloss.NewStyle().Foreground(ColorOverlay1).Render("12 Total Jobs Executed"),', 'lipgloss.NewStyle().Foreground(ColorOverlay1).Render(fmt.Sprintf("%d Total Jobs Executed", m.totalJobs)),')

tui_code = tui_code.replace('lipgloss.NewStyle().Foreground(ColorOverlay1).Render("Avg: $0.0037 / req"),', 'lipgloss.NewStyle().Foreground(ColorOverlay1).Render(fmt.Sprintf("Avg: $%.4f / req", func() float64 { if m.totalJobs == 0 { return 0 }; return m.totalCost/float64(m.totalJobs) }())),')

# Remove the fake breakdown strings
tui_code = re.sub(r'anthropicBar := lipgloss\.NewStyle\(\)\.Foreground\(ColorMauve\)\.Render\(.*?\)', 'anthropicBar := lipgloss.NewStyle().Foreground(ColorMauve).Render("■ Real data collected for active agents")', tui_code)
tui_code = re.sub(r'openaiBar := lipgloss\.NewStyle\(\)\.Foreground\(ColorTeal\)\.Render\(.*?\)', 'openaiBar := lipgloss.NewStyle().Foreground(ColorTeal).Render("")', tui_code)
tui_code = re.sub(r'googleBar := lipgloss\.NewStyle\(\)\.Foreground\(ColorBlue\)\.Render\(.*?\)', 'googleBar := lipgloss.NewStyle().Foreground(ColorBlue).Render("")', tui_code)


with open('/home/mlpc/dev/ai-tracker/pkg/tui/app.go', 'w') as f:
    f.write(tui_code)

print("Done")
