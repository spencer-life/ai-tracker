package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourceResult struct {
	inserted    int64
	updated     int64
	skipped     int64
	diagnostics []string
}

func (r *sourceResult) merge(other sourceResult) {
	r.inserted += other.inserted
	r.updated += other.updated
	r.skipped += other.skipped
	r.diagnostics = append(r.diagnostics, other.diagnostics...)
}

type SyncOptions struct {
	IncludeEstimates bool
}

type SyncReport struct {
	RunID             int64    `json:"runId"`
	Status            string   `json:"status"`
	EventsCommitted   int64    `json:"eventsCommitted"`
	SessionsCommitted int64    `json:"sessionsCommitted"`
	Skipped           int64    `json:"skipped"`
	Errors            int64    `json:"errors"`
	Diagnostics       []string `json:"diagnostics"`
}

func IngestLogs(dbConn *sql.DB) error {
	_, err := Sync(context.Background(), dbConn, SyncOptions{})
	return err
}

func Sync(ctx context.Context, dbConn *sql.DB, opts SyncOptions) (SyncReport, error) {
	repo := NewRepository(dbConn)
	runID, err := repo.BeginSync(ctx)
	if err != nil {
		return SyncReport{}, fmt.Errorf("start sync: %w", err)
	}
	report := SyncReport{RunID: runID, Status: "success", Diagnostics: []string{}}
	home, err := os.UserHomeDir()
	if err != nil {
		report.Status = "failed"
		report.Diagnostics = append(report.Diagnostics, err.Error())
		_ = repo.FinishSync(ctx, runID, report.Status, 0, 0, 0, 1, report.Diagnostics)
		return report, err
	}

	sources := []struct {
		name string
		run  func() (sourceResult, error)
	}{
		{"codex", func() (sourceResult, error) { return ingestCodex(ctx, repo, home) }},
		{"claude", func() (sourceResult, error) { return ingestClaude(ctx, repo, home) }},
		{"agy", func() (sourceResult, error) { return ingestAntigravity(ctx, repo, home, opts.IncludeEstimates) }},
	}
	var combined sourceResult
	var sourceErrors []error
	for _, source := range sources {
		result, sourceErr := source.run()
		combined.merge(result)
		if sourceErr != nil {
			sourceErrors = append(sourceErrors, fmt.Errorf("%s: %w", source.name, sourceErr))
			combined.diagnostics = append(combined.diagnostics, source.name+": "+sourceErr.Error())
		}
	}
	if len(sourceErrors) > 0 {
		report.Status = "degraded"
	}
	report.EventsCommitted, report.SessionsCommitted, report.Skipped, report.Diagnostics = combined.inserted, combined.updated, combined.skipped, combined.diagnostics
	report.Errors = int64(len(sourceErrors))
	if err := repo.FinishSync(ctx, runID, report.Status, report.EventsCommitted, report.SessionsCommitted, report.Skipped, report.Errors, report.Diagnostics); err != nil {
		return report, fmt.Errorf("finish sync: %w", err)
	}
	if len(sourceErrors) > 0 {
		return report, errors.Join(sourceErrors...)
	}
	return report, nil
}

func resolveCodexHome(home string) string {
	return resolveConfiguredHome("CODEX_HOME", filepath.Join(home, ".codex"))
}

// resolveCodexHomes returns the active configured store first and the
// conventional per-user store second when they are distinct. Codex Desktop can
// point WSL processes at a Windows-backed CODEX_HOME while native WSL CLI runs
// retain history under ~/.codex; usage reporting should include both archives.
func resolveCodexHomes(home string) []string {
	configured := resolveCodexHome(home)
	fallback := filepath.Clean(filepath.Join(home, ".codex"))
	homes := []string{configured}
	if !sameCleanPath(configured, fallback) {
		homes = append(homes, fallback)
	}
	return homes
}

func sameCleanPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func resolveClaudeHome(home string) string {
	return resolveConfiguredHome("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
}

func resolveConfiguredHome(envName, fallback string) string {
	configured := strings.TrimSpace(os.Getenv(envName))
	if configured == "" {
		return fallback
	}
	if filepath.IsAbs(configured) {
		return filepath.Clean(configured)
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return filepath.Clean(configured)
	}
	return absolute
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stableID(parts ...string) string { return hashString(strings.Join(parts, "\x00")) }

func intValue(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		value, _ := n.Int64()
		return value
	default:
		return 0
	}
}
