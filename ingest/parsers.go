package ingest

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func IngestLogs(db *sql.DB) error {
	repo := NewRepository(db)

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	agDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	fmt.Printf("Parsing %s logs...\n", agDir)
	parseAntigravityLogs(repo, agDir)

	claudeDir := filepath.Join(home, ".claude")
	fmt.Printf("Parsing %s logs...\n", claudeDir)
	parseClaudeLogs(repo, claudeDir)

	codexDir := filepath.Join(home, ".codex")
	fmt.Printf("Parsing %s logs...\n", codexDir)
	parseCodexLogs(repo, codexDir)

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

func processFile(repo *Repository, path string, agent, defaultModel string, modelMap map[string]string) {
	fileModTimesMu.RLock()
	lastMod, exists := fileModTimes[path]
	fileModTimesMu.RUnlock()

	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if exists && info.ModTime().Equal(lastMod) {
		return
	}

	if strings.HasSuffix(path, ".json") {
		content, err := os.ReadFile(path)
		if err == nil {
			var data map[string]interface{}
			if err := json.Unmarshal(content, &data); err == nil {
				hashBytes := sha256.Sum256(content)
				hashStr := hex.EncodeToString(hashBytes[:])
				model := extractModel(data, defaultModel)
				if modelMap != nil {
					if sid, ok := data["session_id"].(string); ok {
						if m, exists := modelMap[sid]; exists {
							model = m
						}
					}
				}
				ts := extractTimestamp(data)
				inTokens, outTokens := extractTokenUsage(data)
				if inTokens > 0 || outTokens > 0 {
					cost := CalculateCost(model, float64(inTokens), float64(outTokens))
					repo.InsertLog(agent, model, ts, inTokens, outTokens, cost, hashStr)
				}
			}
		}
		fileModTimesMu.Lock()
		fileModTimes[path] = info.ModTime()
		fileModTimesMu.Unlock()
		return
	}

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	offset := repo.GetCursor(path)
	if info.Size() < offset {
		offset = 0
	}
	_, err = file.Seek(offset, 0)
	if err != nil {
		return
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				// Partial line, wait for next sync
				break
			}
			offset += int64(len(line))
			hashBytes := sha256.Sum256(line)
			hashStr := hex.EncodeToString(hashBytes[:])

			var data map[string]interface{}
			if errUnmarshal := json.Unmarshal(line, &data); errUnmarshal == nil {
				// redactMap removed because text is not saved
				model := extractModel(data, defaultModel)
				if modelMap != nil {
					if sid, ok := data["session_id"].(string); ok {
						if m, exists := modelMap[sid]; exists {
							model = m
						}
					}
				}
				ts := extractTimestamp(data)
				inTokens, outTokens := extractTokenUsage(data)
				if inTokens == 0 && outTokens == 0 && agent == "antigravity" {
					inTokens, outTokens = estimateAntigravityTokens(data)
				}
				if inTokens > 0 || outTokens > 0 {
					cost := CalculateCost(model, float64(inTokens), float64(outTokens))
					repo.InsertLog(agent, model, ts, inTokens, outTokens, cost, hashStr)
				}
			}
		}
		if err != nil {
			break
		}
	}

	repo.UpdateCursor(path, offset)

	fileModTimesMu.Lock()
	fileModTimes[path] = info.ModTime()
	fileModTimesMu.Unlock()
}

func parseAntigravityLogs(repo *Repository, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), "transcript.jsonl") {
			return nil
		}
		processFile(repo, path, "antigravity", "gemini-3.1-pro", nil)
		return nil
	})
}

func parseClaudeLogs(repo *Repository, dir string) {
	modelMap := buildClaudeModelMap(dir)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || (!strings.HasSuffix(info.Name(), ".json") && !strings.HasSuffix(info.Name(), ".jsonl")) {
			return nil
		}
		processFile(repo, path, "claude", "claude-5-sonnet", modelMap)
		return nil
	})
}

func buildClaudeModelMap(dir string) map[string]string {
	m := make(map[string]string)
	jobsDir := filepath.Join(dir, "jobs")
	filepath.Walk(jobsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "state.json" {
			content, err := os.ReadFile(path)
			if err == nil {
				var data map[string]interface{}
				if json.Unmarshal(content, &data) == nil {
					sid, _ := data["sessionId"].(string)
					if flags, ok := data["respawnFlags"].([]interface{}); ok {
						for i, v := range flags {
							if s, ok := v.(string); ok && s == "--model" && i+1 < len(flags) {
								if mName, ok := flags[i+1].(string); ok {
									if idx := strings.Index(mName, "["); idx != -1 {
										mName = mName[:idx]
									}
									m[sid] = mName
								}
							}
						}
					}
				}
			}
		}
		return nil
	})
	return m
}

func parseCodexLogs(repo *Repository, dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || (!strings.HasSuffix(info.Name(), ".json") && !strings.HasSuffix(info.Name(), ".jsonl")) {
			return nil
		}
		processFile(repo, path, "codex", "codex-core-v1", nil)
		return nil
	})
}

func extractTokenUsage(data interface{}) (int, int) {
	in, out := 0, 0
	switch v := data.(type) {
	case map[string]interface{}:
		if val, ok := v["input_tokens"].(float64); ok {
			in += int(val)
		} else if val, ok := v["prompt_tokens"].(float64); ok {
			in += int(val)
		}

		if val, ok := v["output_tokens"].(float64); ok {
			out += int(val)
		} else if val, ok := v["completion_tokens"].(float64); ok {
			out += int(val)
		}

		// Recurse into objects
		for _, val := range v {
			if a, ok := val.(map[string]interface{}); ok {
				i, o := extractTokenUsage(a)
				in += i
				out += o
			} else if a, ok := val.([]interface{}); ok {
				i, o := extractTokenUsage(a)
				in += i
				out += o
			}
		}
	case []interface{}:
		for _, item := range v {
			i, o := extractTokenUsage(item)
			in += i
			out += o
		}
	}
	return in, out
}

func estimateAntigravityTokens(data map[string]interface{}) (int, int) {
	in, out := 0, 0
	source, _ := data["source"].(string)

	strLen := func(key string) int {
		if s, ok := data[key].(string); ok {
			return len(s) / 4
		}
		return 0
	}

	tokens := strLen("content") + strLen("thinking")

	if tc, ok := data["tool_calls"].([]interface{}); ok {
		for _, call := range tc {
			if c, ok := call.(map[string]interface{}); ok {
				if args, ok := c["args"].(map[string]interface{}); ok {
					for _, v := range args {
						if s, ok := v.(string); ok {
							tokens += len(s) / 4
						}
					}
				}
			}
		}
	}

	// 1 token minimum if there's any content
	if tokens == 0 && (data["content"] != nil || data["thinking"] != nil) {
		tokens = 1
	}

	if source == "MODEL" {
		out += tokens
	} else {
		in += tokens
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
	if ts, ok := data["start_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			return t
		}
	}
	return time.Now()
}

