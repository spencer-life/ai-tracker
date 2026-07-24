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
	RunID       int64    `json:"runId"`
	Status      string   `json:"status"`
	Inserted    int64    `json:"inserted"`
	Updated     int64    `json:"updated"`
	Skipped     int64    `json:"skipped"`
	Diagnostics []string `json:"diagnostics"`
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
	report.Inserted, report.Updated, report.Skipped, report.Diagnostics = combined.inserted, combined.updated, combined.skipped, combined.diagnostics
	if err := repo.FinishSync(ctx, runID, report.Status, report.Inserted, report.Updated, report.Skipped, int64(len(sourceErrors)), report.Diagnostics); err != nil {
		return report, fmt.Errorf("finish sync: %w", err)
	}
	if len(sourceErrors) > 0 {
		return report, errors.Join(sourceErrors...)
	}
	return report, nil
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
