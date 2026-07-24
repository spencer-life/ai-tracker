package db

import (
	"context"
	"time"
)

type Measurement string

const (
	MeasurementReported  Measurement = "reported"
	MeasurementDerived   Measurement = "derived"
	MeasurementEstimated Measurement = "estimated"
	MeasurementLegacy    Measurement = "legacy"
)

type TokenCounts struct {
	InputUncached int64 `json:"inputUncached"`
	CacheRead     int64 `json:"cacheRead"`
	CacheWrite    int64 `json:"cacheWrite"`
	Output        int64 `json:"output"`
	Reasoning     int64 `json:"reasoning"`
	Total         int64 `json:"total"`
}

type QualityCoverage struct {
	Reported  int64 `json:"reported"`
	Derived   int64 `json:"derived"`
	Estimated int64 `json:"estimated"`
	Legacy    int64 `json:"legacy"`
}

type QueryFilter struct {
	From             time.Time
	To               time.Time
	Timezone         *time.Location
	Agent            string
	Provider         string
	Model            string
	Project          string
	Quality          Measurement
	IncludeEstimates bool
	Limit            int
	Cursor           string
}

type Summary struct {
	RangeFrom          time.Time       `json:"rangeFrom"`
	RangeTo            time.Time       `json:"rangeTo"`
	Timezone           string          `json:"timezone"`
	GeneratedAt        time.Time       `json:"generatedAt"`
	LastSuccessfulSync *time.Time      `json:"lastSuccessfulSync,omitempty"`
	Sessions           int64           `json:"sessions"`
	Events             int64           `json:"events"`
	Tokens             TokenCounts     `json:"tokens"`
	CostMicros         *int64          `json:"costMicros,omitempty"`
	Quality            QualityCoverage `json:"quality"`
}

type SeriesPoint struct {
	Start      time.Time       `json:"start"`
	End        time.Time       `json:"end"`
	Tokens     TokenCounts     `json:"tokens"`
	CostMicros *int64          `json:"costMicros,omitempty"`
	Sessions   int64           `json:"sessions"`
	Quality    QualityCoverage `json:"quality"`
}

type Session struct {
	ID              string      `json:"id"`
	Agent           string      `json:"agent"`
	Provider        string      `json:"provider"`
	SourceSessionID string      `json:"sourceSessionId"`
	ParentSessionID string      `json:"parentSessionId,omitempty"`
	Project         string      `json:"project,omitempty"`
	Title           string      `json:"title,omitempty"`
	StartedAt       *time.Time  `json:"startedAt,omitempty"`
	UpdatedAt       time.Time   `json:"updatedAt"`
	EndedAt         *time.Time  `json:"endedAt,omitempty"`
	Status          string      `json:"status"`
	IsSubagent      bool        `json:"isSubagent"`
	Model           string      `json:"model,omitempty"`
	Measurement     Measurement `json:"measurement"`
	Tokens          TokenCounts `json:"tokens"`
	CostMicros      *int64      `json:"costMicros,omitempty"`
	EventCount      int64       `json:"eventCount"`
}

type SessionPage struct {
	Sessions   []Session `json:"sessions"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type BreakdownItem struct {
	Key        string          `json:"key"`
	Tokens     TokenCounts     `json:"tokens"`
	CostMicros *int64          `json:"costMicros,omitempty"`
	Sessions   int64           `json:"sessions"`
	Quality    QualityCoverage `json:"quality"`
}

type SyncStatus struct {
	ID          int64      `json:"id"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	Status      string     `json:"status"`
	Inserted    int64      `json:"inserted"`
	Updated     int64      `json:"updated"`
	Skipped     int64      `json:"skipped"`
	Errors      int64      `json:"errors"`
	Diagnostics []string   `json:"diagnostics"`
}

type AgentStats struct {
	Name         string
	Model        string
	InputTokens  int64
	OutputTokens int64
	Cost         float64
	Jobs         int64
}

type Repository interface {
	Summary(context.Context, QueryFilter) (Summary, error)
	Series(context.Context, QueryFilter, string) ([]SeriesPoint, error)
	ListSessions(context.Context, QueryFilter) (SessionPage, error)
	GetSession(context.Context, string) (Session, error)
	Breakdown(context.Context, QueryFilter, string) ([]BreakdownItem, error)
	LastSync(context.Context) (SyncStatus, error)
	GetAgentStats() ([]AgentStats, error)
	GetRecentLogs(limit int) ([]string, error)
	Close() error
}
