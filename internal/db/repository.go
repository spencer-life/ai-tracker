package db

import (
    "time"
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

type AgentStats struct {
    Name        string
    Model       string
    InputTokens int64
    OutputTokens int64
    Cost        float64
    Jobs        int64
}

type Repository interface {
    Init() error
    InsertLog(agent, model string, timestamp time.Time, inTokens, outTokens int, cost float64, hash string) error
    GetCursor(path string) int64
    UpdateCursor(path string, offset int64) error
    GetAgentStats() ([]AgentStats, error)
    GetRecentLogs(limit int) ([]string, error)
    Close() error
}
