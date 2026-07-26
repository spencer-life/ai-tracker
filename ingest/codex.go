package ingest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

const codexParserVersion = "codex-rollout-v2.1"

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"session_id"`
	ParentThreadID string          `json:"parent_thread_id"`
	ForkedFromID   string          `json:"forked_from_id"`
	ThreadSource   string          `json:"thread_source"`
	Timestamp      string          `json:"timestamp"`
	CWD            string          `json:"cwd"`
	Source         json.RawMessage `json:"source"`
}

type codexInterAgentMetadata struct {
	TriggerTurn bool `json:"trigger_turn"`
}

type codexTurnContext struct {
	Model  string `json:"model"`
	TurnID string `json:"turn_id"`
	CWD    string `json:"cwd"`
}

type codexTokenCount struct {
	Type string `json:"type"`
	Info *struct {
		Last  *codexTokenUsage `json:"last_token_usage"`
		Total *codexTokenUsage `json:"total_token_usage"`
	} `json:"info"`
}

type codexTokenUsage struct {
	Input      int64 `json:"input_tokens"`
	CacheRead  int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

type codexFileResult struct {
	batch       Batch
	skipped     bool
	diagnostics []string
}

// ingestCodex imports canonical token deltas from Codex rollout JSONL files.
// state_5.sqlite is intentionally not consulted for usage because its
// threads.tokens_used value has no trustworthy token-category breakdown.
func ingestCodex(ctx context.Context, repo *Repository, home string) (sourceResult, error) {
	paths := make([]string, 0)
	seenRollouts := make(map[string]string)
	var result sourceResult
	var sourceErrors []error
	for _, codexHome := range resolveCodexHomes(home) {
		root := filepath.Join(codexHome, "sessions")
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			if preferred, exists := seenRollouts[entry.Name()]; exists {
				result.diagnostics = append(result.diagnostics, "codex: ignored duplicate rollout "+entry.Name()+" from secondary store; preferred "+filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(preferred)))))+" store")
				return nil
			}
			seenRollouts[entry.Name()] = path
			paths = append(paths, path)
			return nil
		})
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			sourceErrors = append(sourceErrors, fmt.Errorf("walk Codex rollouts in %s: %w", filepath.Base(codexHome), err))
		}
	}
	sort.Strings(paths)

	for _, path := range paths {
		parsed, err := parseCodexRollout(ctx, repo, path)
		if err != nil {
			wrapped := fmt.Errorf("parse Codex rollout %s: %w", filepath.Base(path), err)
			sourceErrors = append(sourceErrors, wrapped)
			result.diagnostics = append(result.diagnostics, "codex: skipped unreadable rollout "+filepath.Base(path))
			continue
		}
		if parsed.skipped {
			result.skipped++
			continue
		}
		inserted, updated, err := repo.ApplyBatch(ctx, parsed.batch)
		if err != nil {
			wrapped := fmt.Errorf("commit Codex rollout %s: %w", filepath.Base(path), err)
			sourceErrors = append(sourceErrors, wrapped)
			result.diagnostics = append(result.diagnostics, "codex: skipped uncommitted rollout "+filepath.Base(path))
			continue
		}
		result.inserted += inserted
		result.updated += updated
		result.diagnostics = append(result.diagnostics, parsed.diagnostics...)
	}
	return result, errors.Join(sourceErrors...)
}

func parseCodexRollout(ctx context.Context, repo *Repository, path string) (codexFileResult, error) {
	info, err := os.Stat(path)
	if err != nil {
		return codexFileResult{}, err
	}
	pathHash := codexHash(path)
	device, inode := codexFileIdentity(info)
	cp, found, err := repo.Checkpoint(ctx, pathHash)
	if err != nil {
		return codexFileResult{}, fmt.Errorf("read checkpoint: %w", err)
	}
	if found && cp.ParserVersion == codexParserVersion && cp.Device == device && cp.Inode == inode && cp.Size == info.Size() && cp.MTimeNS == info.ModTime().UnixNano() {
		return codexFileResult{skipped: true}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return codexFileResult{}, err
	}
	defer func() { _ = file.Close() }()

	var session SessionRecord
	var parentSourceID string
	var copiedParentPrefix, childUsageStarted bool
	var currentModel, currentTurn, currentCWD string
	var previousCumulative, previousUsage string
	var firstAt, lastAt int64
	events := make([]UsageEvent, 0)
	diagnostics := make([]string, 0)
	reader := bufio.NewReader(file)
	var offset int64
	for {
		lineStart := offset
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' && (!errors.Is(readErr, io.EOF) || !json.Valid(line)) {
				// Leave an actively-written partial record uncommitted for the next sync.
				break
			}
			offset += int64(len(line))
			trimmed := strings.TrimSpace(string(line))
			if trimmed == "" {
				continue
			}
			var envelope codexEnvelope
			if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
				return codexFileResult{}, fmt.Errorf("invalid JSON at byte %d: %w", lineStart, err)
			}
			at, err := codexTimestampMS(envelope.Timestamp)
			if err != nil {
				return codexFileResult{}, fmt.Errorf("invalid timestamp at byte %d: %w", lineStart, err)
			}
			switch envelope.Type {
			case "session_meta":
				var meta codexSessionMeta
				if err := json.Unmarshal(envelope.Payload, &meta); err != nil {
					return codexFileResult{}, fmt.Errorf("invalid session metadata at byte %d: %w", lineStart, err)
				}
				sourceID := meta.ID
				if sourceID == "" {
					sourceID = meta.SessionID
				}
				if sourceID == "" {
					return codexFileResult{}, fmt.Errorf("missing session identity at byte %d", lineStart)
				}
				if session.ID != "" {
					if parentSourceID != "" && sourceID == parentSourceID {
						copiedParentPrefix = true
					}
					break
				}
				parentSourceID = meta.ParentThreadID
				if parentSourceID == "" {
					parentSourceID = meta.ForkedFromID
				}
				session = SessionRecord{
					ID:              codexSessionID(sourceID),
					Agent:           "codex",
					Provider:        "openai",
					SourceSessionID: sourceID,
					ParentSessionID: codexOptionalSessionID(parentSourceID),
					IsSubagent:      parentSourceID != "" || meta.ThreadSource == "subagent",
					Project:         codexOptionalHash(meta.CWD),
					Status:          "unknown",
					SourceKind:      "rollout_jsonl",
					SourcePathHash:  pathHash,
					Measurement:     coredb.MeasurementReported,
				}
				if meta.Timestamp != "" {
					if sessionAt, stampErr := codexTimestampMS(meta.Timestamp); stampErr == nil {
						session.StartedAtMS = sessionAt
					}
				}
				if session.StartedAtMS == 0 {
					session.StartedAtMS = at
				}
				currentCWD = meta.CWD
			case "inter_agent_communication_metadata":
				if copiedParentPrefix {
					var metadata codexInterAgentMetadata
					if err := json.Unmarshal(envelope.Payload, &metadata); err != nil {
						return codexFileResult{}, fmt.Errorf("invalid inter-agent metadata at byte %d: %w", lineStart, err)
					}
					if metadata.TriggerTurn {
						childUsageStarted = true
					}
				}
			case "turn_context":
				var turn codexTurnContext
				if err := json.Unmarshal(envelope.Payload, &turn); err != nil {
					return codexFileResult{}, fmt.Errorf("invalid turn context at byte %d: %w", lineStart, err)
				}
				currentModel, currentTurn = turn.Model, turn.TurnID
				if turn.CWD != "" {
					currentCWD = turn.CWD
				}
			case "event_msg":
				var count codexTokenCount
				if err := json.Unmarshal(envelope.Payload, &count); err != nil {
					return codexFileResult{}, fmt.Errorf("invalid event at byte %d: %w", lineStart, err)
				}
				if count.Type != "token_count" || count.Info == nil || count.Info.Last == nil {
					break
				}
				if copiedParentPrefix && !childUsageStarted {
					break
				}
				usageFingerprint := count.Info.Last.fingerprint()
				if count.Info.Total != nil {
					cumulative := count.Info.Total.fingerprint()
					if cumulative == previousCumulative {
						break
					}
					previousCumulative = cumulative
				} else {
					if usageFingerprint == previousUsage {
						break
					}
					previousUsage = usageFingerprint
				}
				if session.ID == "" {
					return codexFileResult{}, fmt.Errorf("token event precedes session metadata at byte %d", lineStart)
				}
				u := count.Info.Last
				if err := u.validate(); err != nil {
					return codexFileResult{}, fmt.Errorf("invalid token usage at byte %d: %w", lineStart, err)
				}
				inputUncached, variance := u.inputUncached()
				if variance != "" {
					diagnostics = append(diagnostics, fmt.Sprintf("codex %s byte %d: %s", filepath.Base(path), lineStart, variance))
				}
				eventIdentity := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d", pathHash, lineStart, at, currentTurn, currentModel, u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning, u.Total)
				events = append(events, UsageEvent{
					ID:           codexHash(eventIdentity),
					SessionID:    session.ID,
					TurnID:       currentTurn,
					Model:        currentModel,
					Provider:     "openai",
					OccurredAtMS: at,
					Tokens: coredb.TokenCounts{
						InputUncached: inputUncached,
						CacheRead:     u.CacheRead,
						CacheWrite:    u.CacheWrite,
						Output:        u.Output,
						Reasoning:     u.Reasoning,
						Total:         u.Total,
					},
					Measurement:    coredb.MeasurementReported,
					SourcePathHash: pathHash,
					SourceOffset:   lineStart,
					ParserVersion:  codexParserVersion,
				})
				if firstAt == 0 || at < firstAt {
					firstAt = at
				}
				if at > lastAt {
					lastAt = at
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return codexFileResult{}, readErr
			}
			break
		}
	}
	if session.ID == "" {
		return codexFileResult{}, errors.New("missing session metadata")
	}
	if session.Project == "" && currentCWD != "" {
		session.Project = codexHash(currentCWD)
	}
	if firstAt != 0 && (session.StartedAtMS == 0 || firstAt < session.StartedAtMS) {
		session.StartedAtMS = firstAt
	}
	if lastAt == 0 {
		lastAt = session.StartedAtMS
	}
	session.UpdatedAtMS = lastAt
	session.Model = currentModel
	return codexFileResult{batch: Batch{
		Session: session,
		Events:  events,
		Checkpoint: &SourceCheckpoint{
			Path:          pathHash,
			Device:        device,
			Inode:         inode,
			Size:          info.Size(),
			MTimeNS:       info.ModTime().UnixNano(),
			Offset:        offset,
			ParserVersion: codexParserVersion,
			Replace:       found,
		},
	}, diagnostics: diagnostics}, nil
}

func (u codexTokenUsage) fingerprint() string {
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d", u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning, u.Total)
}

func (u codexTokenUsage) validate() error {
	if u.Input < 0 || u.CacheRead < 0 || u.CacheWrite < 0 || u.Output < 0 || u.Reasoning < 0 || u.Total < 0 {
		return errors.New("negative token counter")
	}
	if u.CacheRead > u.Input {
		return errors.New("cached input exceeds input tokens")
	}
	return nil
}

func (u codexTokenUsage) inputUncached() (int64, string) {
	// Codex reports input_tokens inclusively. The normal partition removes both
	// cache categories; reasoning is tracked separately but may overlap output.
	if u.CacheRead+u.CacheWrite <= u.Input {
		uncached := u.Input - u.CacheRead - u.CacheWrite
		if uncached+u.CacheRead+u.CacheWrite+u.Output == u.Total {
			return uncached, ""
		}
	}
	// Some rollout schema versions report cache writes outside input_tokens.
	// Accept that shape only when its partition exactly reconciles to total.
	if u.CacheRead <= u.Input {
		uncached := u.Input - u.CacheRead
		if uncached+u.CacheRead+u.CacheWrite+u.Output == u.Total {
			return uncached, "cache-write tokens reported outside inclusive input tokens"
		}
	}
	if u.CacheRead+u.CacheWrite <= u.Input {
		return u.Input - u.CacheRead - u.CacheWrite, "reported token categories do not reconcile to total_tokens"
	}
	return u.Input - u.CacheRead, "cache-write tokens exceed the remaining inclusive input partition"
}

func codexTimestampMS(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("timestamp is empty")
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

func codexHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func codexSessionID(sourceID string) string { return "codex:" + codexHash(sourceID) }
func codexOptionalSessionID(sourceID string) string {
	if sourceID == "" {
		return ""
	}
	return codexSessionID(sourceID)
}
func codexOptionalHash(value string) string {
	if value == "" {
		return ""
	}
	return codexHash(value)
}

func codexFileIdentity(info os.FileInfo) (device, inode uint64) {
	return fileIdentity(info)
}
