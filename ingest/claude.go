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
	"syscall"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

const claudeParserVersion = "claude-v2.1"

// claudeLogRow deliberately models only metadata and accounting fields. Prompt
// content and titles are never decoded, retained, or written to the database.
type claudeLogRow struct {
	Type        string        `json:"type"`
	SessionID   string        `json:"sessionId"`
	AgentID     string        `json:"agentId"`
	UUID        string        `json:"uuid"`
	RequestID   string        `json:"requestId"`
	ParentUUID  string        `json:"parentUuid"`
	Timestamp   string        `json:"timestamp"`
	IsSidechain bool          `json:"isSidechain"`
	Message     claudeMessage `json:"message"`
}

type claudeMessage struct {
	ID    string      `json:"id"`
	Role  string      `json:"role"`
	Model string      `json:"model"`
	Usage claudeUsage `json:"usage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type claudeSessionBatch struct {
	sourceID       string
	parentSourceID string
	pathHash       string
	projectHash    string
	isSubagent     bool
	startedAtMS    int64
	updatedAtMS    int64
	model          string
	events         []UsageEvent
}

func ingestClaude(ctx context.Context, repo *Repository, home string) (sourceResult, error) {
	var result sourceResult
	root := filepath.Join(home, ".claude", "projects")
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.diagnostics = append(result.diagnostics, "claude: unable to inspect a project entry")
			return nil
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("scan Claude project logs: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fileResult, fileErr := ingestClaudeFile(ctx, repo, root, path)
		result.inserted += fileResult.inserted
		result.updated += fileResult.updated
		result.skipped += fileResult.skipped
		result.diagnostics = append(result.diagnostics, fileResult.diagnostics...)
		if fileErr != nil {
			return result, fileErr
		}
	}
	return result, nil
}

func ingestClaudeFile(ctx context.Context, repo *Repository, root, path string) (sourceResult, error) {
	var result sourceResult
	info, err := os.Stat(path)
	if err != nil {
		return result, fmt.Errorf("stat Claude log: %w", err)
	}
	pathHash := claudeHash("source", path)
	projectHash := claudeProjectHash(root, path)
	device, inode := claudeFileIdentity(info)
	offset := int64(0)
	checkpoint, found, checkpointErr := repo.Checkpoint(ctx, pathHash)
	if checkpointErr != nil {
		return result, fmt.Errorf("read Claude checkpoint: %w", checkpointErr)
	}
	if found && checkpoint.ParserVersion == claudeParserVersion && checkpoint.Device == device && checkpoint.Inode == inode && checkpoint.Size == info.Size() && checkpoint.MTimeNS == info.ModTime().UnixNano() {
		result.skipped++
		return result, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("open Claude log: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return result, fmt.Errorf("seek Claude log: %w", err)
	}

	reader := bufio.NewReader(file)
	committedOffset := offset
	batches := make(map[string]*claudeSessionBatch)
	for {
		lineStart := committedOffset
		line, readErr := reader.ReadBytes('\n')
		if len(line) != 0 {
			if line[len(line)-1] != '\n' && (!errors.Is(readErr, io.EOF) || !json.Valid(line)) {
				// A writer may still be appending this row. Do not checkpoint it.
				break
			}
			committedOffset += int64(len(line))
			var row claudeLogRow
			if err := json.Unmarshal(line, &row); err != nil {
				result.skipped++
				result.diagnostics = append(result.diagnostics, "claude: skipped malformed JSONL row")
			} else {
				claudeAccumulateRow(&result, batches, row, pathHash, projectHash, lineStart)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return result, fmt.Errorf("read Claude log: %w", readErr)
			}
			break
		}
	}

	keys := make([]string, 0, len(batches))
	for key, batch := range batches {
		if batch.updatedAtMS != 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	dbBatches := make([]Batch, 0, len(keys))
	for index, key := range keys {
		batch := batches[key]
		parentID := ""
		if batch.parentSourceID != "" {
			parentID = claudeSessionID(batch.parentSourceID)
		}
		dbBatch := Batch{Session: SessionRecord{
			ID: claudeSessionID(batch.sourceID), Agent: "claude", Provider: "anthropic",
			SourceSessionID: batch.sourceID, ParentSessionID: parentID,
			Project: batch.projectHash, Status: "unknown", Model: batch.model,
			SourceKind: "claude_project_jsonl", SourcePathHash: batch.pathHash,
			StartedAtMS: batch.startedAtMS, UpdatedAtMS: batch.updatedAtMS,
			IsSubagent: batch.isSubagent, Measurement: coredb.MeasurementReported,
		}, Events: batch.events}
		if index == len(keys)-1 {
			dbBatch.Checkpoint = &SourceCheckpoint{
				Path: pathHash, Device: device, Inode: inode, Size: info.Size(),
				MTimeNS: info.ModTime().UnixNano(), Offset: committedOffset,
				ParserVersion: claudeParserVersion, Replace: found,
			}
		}
		dbBatches = append(dbBatches, dbBatch)
	}
	if len(dbBatches) > 0 {
		inserted, updated, err := repo.ApplyBatches(ctx, dbBatches)
		if err != nil {
			return result, fmt.Errorf("commit Claude log batches: %w", err)
		}
		result.inserted += inserted
		result.updated += updated
	}
	if len(keys) == 0 && committedOffset > offset {
		// A file containing no identifiable session cannot be transactionally
		// checkpointed through ApplyBatch, so leave it retryable and diagnose it.
		result.diagnostics = append(result.diagnostics, "claude: no timestamped session identity found in JSONL file")
	}
	return result, nil
}

func claudeAccumulateRow(result *sourceResult, batches map[string]*claudeSessionBatch, row claudeLogRow, pathHash, projectHash string, sourceOffset int64) {
	if row.SessionID == "" {
		if row.Type == "assistant" {
			result.skipped++
			result.diagnostics = append(result.diagnostics, "claude: assistant row missing session identity")
		}
		return
	}
	sourceID := row.SessionID
	parentSourceID := ""
	isSubagent := row.IsSidechain
	if row.IsSidechain && row.AgentID != "" {
		sourceID = row.AgentID
		parentSourceID = row.SessionID
	}
	batch := batches[sourceID]
	if batch == nil {
		batch = &claudeSessionBatch{sourceID: sourceID, parentSourceID: parentSourceID, pathHash: pathHash, projectHash: projectHash, isSubagent: isSubagent}
		batches[sourceID] = batch
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, row.Timestamp); err == nil {
		occurredAtMS := timestamp.UTC().UnixMilli()
		if batch.startedAtMS == 0 || occurredAtMS < batch.startedAtMS {
			batch.startedAtMS = occurredAtMS
		}
		if occurredAtMS > batch.updatedAtMS {
			batch.updatedAtMS = occurredAtMS
		}
	}
	if row.Type != "assistant" || (row.Message.Role != "" && row.Message.Role != "assistant") {
		return
	}
	timestamp, err := time.Parse(time.RFC3339Nano, row.Timestamp)
	if err != nil {
		result.skipped++
		result.diagnostics = append(result.diagnostics, "claude: assistant usage row missing valid timestamp")
		return
	}
	usage := row.Message.Usage
	if usage.InputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.CacheCreationInputTokens < 0 || usage.OutputTokens < 0 {
		result.skipped++
		result.diagnostics = append(result.diagnostics, "claude: assistant usage row has negative token count")
		return
	}
	if usage.InputTokens == 0 && usage.CacheReadInputTokens == 0 && usage.CacheCreationInputTokens == 0 && usage.OutputTokens == 0 {
		return
	}
	occurredAtMS := timestamp.UTC().UnixMilli()
	if row.Message.Model == "" {
		result.diagnostics = append(result.diagnostics, "claude: assistant usage row has unknown model")
	} else {
		batch.model = row.Message.Model
	}
	// Claude may emit multiple JSONL rows for one API response while its
	// content blocks evolve. message.id is the stable response identity; using
	// the per-row UUID would double-count the same reported usage snapshot.
	eventIdentity := row.Message.ID
	if eventIdentity == "" {
		eventIdentity = row.RequestID
	}
	if eventIdentity == "" {
		eventIdentity = row.UUID
	}
	if eventIdentity == "" {
		eventIdentity = fmt.Sprintf("offset:%d", sourceOffset)
	}
	tokens := coredb.TokenCounts{
		InputUncached: usage.InputTokens,
		CacheRead:     usage.CacheReadInputTokens,
		CacheWrite:    usage.CacheCreationInputTokens,
		Output:        usage.OutputTokens,
	}
	tokens.Total = tokens.InputUncached + tokens.CacheRead + tokens.CacheWrite + tokens.Output
	turnID := row.Message.ID
	if turnID == "" {
		turnID = row.RequestID
	}
	batch.events = append(batch.events, UsageEvent{
		ID:        claudeHash("event", pathHash, sourceID, eventIdentity),
		SessionID: claudeSessionID(sourceID), TurnID: turnID, Model: row.Message.Model,
		Provider: "anthropic", OccurredAtMS: occurredAtMS, Tokens: tokens,
		Measurement: coredb.MeasurementReported, SourcePathHash: pathHash,
		SourceOffset: sourceOffset, ParserVersion: claudeParserVersion,
	})
}

func claudeSessionID(sourceID string) string {
	return "claude-" + claudeHash("session", sourceID)[:32]
}

func claudeProjectHash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "project-" + claudeHash("project", filepath.Dir(path))[:16]
	}
	component := strings.Split(filepath.ToSlash(rel), "/")[0]
	return "project-" + claudeHash("project", component)[:16]
}

func claudeHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func claudeFileIdentity(info os.FileInfo) (uint64, uint64) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return uint64(stat.Dev), uint64(stat.Ino)
}
