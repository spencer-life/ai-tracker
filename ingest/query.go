package ingest

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
)

func normalizeFilter(f coredb.QueryFilter) coredb.QueryFilter {
	if f.Quality == coredb.MeasurementEstimated {
		f.IncludeEstimates = true
	}
	if f.Timezone == nil {
		f.Timezone = time.Local
	}
	if f.To.IsZero() {
		f.To = time.Now().In(f.Timezone)
	}
	if f.From.IsZero() {
		f.From = f.To.AddDate(0, 0, -30)
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 250 {
		f.Limit = 250
	}
	return f
}

func usageWhere(f coredb.QueryFilter, alias string) (string, []any) {
	f = normalizeFilter(f)
	p := alias
	if p != "" {
		p += "."
	}
	parts := []string{p + "occurred_at_ms >= ?", p + "occurred_at_ms < ?"}
	args := []any{f.From.UTC().UnixMilli(), f.To.UTC().UnixMilli()}
	if !f.IncludeEstimates {
		parts = append(parts, p+"measurement <> 'estimated'")
	}
	if f.Quality != "" {
		parts = append(parts, p+"measurement = ?")
		args = append(args, f.Quality)
	}
	if f.Provider != "" {
		parts = append(parts, p+"provider = ?")
		args = append(args, f.Provider)
	}
	if f.Model != "" {
		parts = append(parts, p+"model = ?")
		args = append(args, f.Model)
	}
	if f.Agent != "" {
		parts = append(parts, "s.agent = ?")
		args = append(args, f.Agent)
	}
	if f.Project != "" {
		parts = append(parts, "s.project = ?")
		args = append(args, f.Project)
	}
	return strings.Join(parts, " AND "), args
}

const aggregateColumns = `
	COALESCE(SUM(ue.input_uncached),0), COALESCE(SUM(ue.cache_read),0),
	COALESCE(SUM(ue.cache_write),0), COALESCE(SUM(ue.output_tokens),0),
	COALESCE(SUM(ue.reasoning_output),0), COALESCE(SUM(ue.total_tokens),0),
	CASE WHEN SUM(CASE WHEN ue.total_tokens>0 AND ue.cost_micros IS NULL THEN 1 ELSE 0 END)>0
		THEN NULL ELSE SUM(ue.cost_micros) END,
	COUNT(*), COUNT(DISTINCT ue.session_id),
	COALESCE(SUM(CASE WHEN ue.measurement='reported' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='derived' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='estimated' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='legacy' THEN ue.total_tokens ELSE 0 END),0)`

const sessionAggregateColumns = `
	COALESCE(SUM(ue.input_uncached),0),COALESCE(SUM(ue.cache_read),0),
	COALESCE(SUM(ue.cache_write),0),COALESCE(SUM(ue.output_tokens),0),
	COALESCE(SUM(ue.reasoning_output),0),COALESCE(SUM(ue.total_tokens),0),
	CASE WHEN SUM(CASE WHEN ue.total_tokens>0 AND ue.cost_micros IS NULL THEN 1 ELSE 0 END)>0
		THEN NULL ELSE SUM(ue.cost_micros) END,
	COUNT(ue.id),CASE WHEN COUNT(ue.id)>0 THEN 1 ELSE 0 END,
	COALESCE(SUM(CASE WHEN ue.measurement='reported' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='derived' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='estimated' THEN ue.total_tokens ELSE 0 END),0),
	COALESCE(SUM(CASE WHEN ue.measurement='legacy' THEN ue.total_tokens ELSE 0 END),0)`

func scanAggregate(row interface{ Scan(...any) error }, tokens *coredb.TokenCounts, cost *sql.NullInt64, events, sessions *int64, q *coredb.QualityCoverage) error {
	return row.Scan(&tokens.InputUncached, &tokens.CacheRead, &tokens.CacheWrite, &tokens.Output, &tokens.Reasoning, &tokens.Total, cost, events, sessions, &q.Reported, &q.Derived, &q.Estimated, &q.Legacy)
}

func (r *Repository) Summary(ctx context.Context, filter coredb.QueryFilter) (coredb.Summary, error) {
	f := normalizeFilter(filter)
	where, args := usageWhere(f, "ue")
	var out coredb.Summary
	out.RangeFrom, out.RangeTo, out.GeneratedAt = f.From, f.To, time.Now().UTC()
	out.Timezone = f.Timezone.String()
	var cost sql.NullInt64
	if err := scanAggregate(r.db.QueryRowContext(ctx, `SELECT `+aggregateColumns+` FROM usage_events ue JOIN sessions s ON s.id=ue.session_id WHERE `+where, args...), &out.Tokens, &cost, &out.Events, &out.Sessions, &out.Quality); err != nil {
		return out, err
	}
	sessionTotal, err := sessionCount(ctx, r.db, f)
	if err != nil {
		return out, err
	}
	out.Sessions = sessionTotal
	if cost.Valid {
		out.CostMicros = &cost.Int64
	}
	var lastMS sql.NullInt64
	err = r.db.QueryRowContext(ctx, `SELECT finished_at_ms FROM sync_runs WHERE status='success' ORDER BY finished_at_ms DESC LIMIT 1`).Scan(&lastMS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if lastMS.Valid {
		t := time.UnixMilli(lastMS.Int64).UTC()
		out.LastSuccessfulSync = &t
	}
	return out, nil
}

func sessionCount(ctx context.Context, dbConn *sql.DB, f coredb.QueryFilter) (int64, error) {
	f = normalizeFilter(f)
	join := []string{"ue.session_id=s.id", "ue.occurred_at_ms>=?", "ue.occurred_at_ms<?"}
	args := []any{f.From.UTC().UnixMilli(), f.To.UTC().UnixMilli()}
	if !f.IncludeEstimates {
		join = append(join, "ue.measurement<>'estimated'")
	}
	if f.Quality != "" {
		join = append(join, "ue.measurement=?")
		args = append(args, f.Quality)
	}
	if f.Model != "" {
		join = append(join, "ue.model=?")
		args = append(args, f.Model)
	}
	metadata := []string{"s.updated_at_ms>=?", "s.updated_at_ms<?"}
	args = append(args, f.From.UTC().UnixMilli(), f.To.UTC().UnixMilli())
	if f.Model != "" {
		metadata = append(metadata, "s.model=?")
		args = append(args, f.Model)
	}
	if f.Quality != "" {
		metadata = append(metadata, "s.measurement=?")
		args = append(args, f.Quality)
	}
	where := []string{"(ue.id IS NOT NULL OR (" + strings.Join(metadata, " AND ") + "))"}
	if f.Agent != "" {
		where = append(where, "s.agent=?")
		args = append(args, f.Agent)
	}
	if f.Provider != "" {
		where = append(where, "s.provider=?")
		args = append(args, f.Provider)
	}
	if f.Project != "" {
		where = append(where, "s.project=?")
		args = append(args, f.Project)
	}
	var count int64
	err := dbConn.QueryRowContext(ctx, `SELECT COUNT(DISTINCT s.id) FROM sessions s LEFT JOIN usage_events ue ON `+strings.Join(join, " AND ")+` WHERE `+strings.Join(where, " AND "), args...).Scan(&count)
	return count, err
}

type rawPoint struct {
	at      int64
	session string
	tokens  coredb.TokenCounts
	cost    sql.NullInt64
	quality coredb.Measurement
}

func (r *Repository) Series(ctx context.Context, filter coredb.QueryFilter, bucket string) ([]coredb.SeriesPoint, error) {
	f := normalizeFilter(filter)
	where, args := usageWhere(f, "ue")
	rows, err := r.db.QueryContext(ctx, `SELECT ue.occurred_at_ms,ue.session_id,ue.input_uncached,ue.cache_read,ue.cache_write,ue.output_tokens,ue.reasoning_output,ue.total_tokens,ue.cost_micros,ue.measurement FROM usage_events ue JOIN sessions s ON s.id=ue.session_id WHERE `+where+` ORDER BY ue.occurred_at_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	points := map[int64]*coredb.SeriesPoint{}
	sessions := map[int64]map[string]struct{}{}
	unknownCost := map[int64]bool{}
	for rows.Next() {
		var p rawPoint
		if err := rows.Scan(&p.at, &p.session, &p.tokens.InputUncached, &p.tokens.CacheRead, &p.tokens.CacheWrite, &p.tokens.Output, &p.tokens.Reasoning, &p.tokens.Total, &p.cost, &p.quality); err != nil {
			return nil, err
		}
		start, end, err := bucketBounds(time.UnixMilli(p.at).In(f.Timezone), bucket)
		if err != nil {
			return nil, err
		}
		key := start.UTC().UnixMilli()
		item := points[key]
		if item == nil {
			item = &coredb.SeriesPoint{Start: start, End: end}
			points[key] = item
			sessions[key] = map[string]struct{}{}
		}
		addTokens(&item.Tokens, p.tokens)
		addQuality(&item.Quality, p.quality, p.tokens.Total)
		if p.cost.Valid {
			if item.CostMicros == nil {
				v := int64(0)
				item.CostMicros = &v
			}
			*item.CostMicros += p.cost.Int64
		} else if p.tokens.Total > 0 {
			unknownCost[key] = true
		}
		sessions[key][p.session] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	start, _, err := bucketBounds(f.From.In(f.Timezone), bucket)
	if err != nil {
		return nil, err
	}
	var out []coredb.SeriesPoint
	for start.Before(f.To) {
		_, end, _ := bucketBounds(start, bucket)
		key := start.UTC().UnixMilli()
		if p := points[key]; p != nil {
			if unknownCost[key] {
				p.CostMicros = nil
			}
			p.Sessions = int64(len(sessions[key]))
			out = append(out, *p)
		} else {
			out = append(out, coredb.SeriesPoint{Start: start, End: end})
		}
		start = end
	}
	return out, nil
}

func bucketBounds(t time.Time, bucket string) (time.Time, time.Time, error) {
	loc := t.Location()
	switch bucket {
	case "hour":
		s := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		return s, s.Add(time.Hour), nil
	case "day", "":
		s := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return s, s.AddDate(0, 0, 1), nil
	case "week":
		s := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		delta := (int(s.Weekday()) + 6) % 7
		s = s.AddDate(0, 0, -delta)
		return s, s.AddDate(0, 0, 7), nil
	case "month":
		s := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		return s, s.AddDate(0, 1, 0), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported bucket %q", bucket)
	}
}

func (r *Repository) ListSessions(ctx context.Context, filter coredb.QueryFilter) (coredb.SessionPage, error) {
	f := normalizeFilter(filter)
	joinParts := []string{"ue.session_id=s.id", "ue.occurred_at_ms >= ?", "ue.occurred_at_ms < ?"}
	args := []any{f.From.UTC().UnixMilli(), f.To.UTC().UnixMilli()}
	if !f.IncludeEstimates {
		joinParts = append(joinParts, "ue.measurement <> 'estimated'")
	}
	if f.Quality != "" {
		joinParts = append(joinParts, "ue.measurement = ?")
		args = append(args, f.Quality)
	}
	if f.Model != "" {
		joinParts = append(joinParts, "ue.model = ?")
		args = append(args, f.Model)
	}
	metadataParts := []string{"s.updated_at_ms >= ?", "s.updated_at_ms < ?"}
	args = append(args, f.From.UTC().UnixMilli(), f.To.UTC().UnixMilli())
	if f.Model != "" {
		metadataParts = append(metadataParts, "s.model = ?")
		args = append(args, f.Model)
	}
	if f.Quality != "" {
		metadataParts = append(metadataParts, "s.measurement = ?")
		args = append(args, f.Quality)
	}
	whereParts := []string{"(ue.id IS NOT NULL OR (" + strings.Join(metadataParts, " AND ") + "))"}
	if f.Agent != "" {
		whereParts = append(whereParts, "s.agent = ?")
		args = append(args, f.Agent)
	}
	if f.Provider != "" {
		whereParts = append(whereParts, "s.provider = ?")
		args = append(args, f.Provider)
	}
	if f.Project != "" {
		whereParts = append(whereParts, "s.project = ?")
		args = append(args, f.Project)
	}
	cursorWhere := ""
	if f.Cursor != "" {
		ms, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return coredb.SessionPage{}, err
		}
		cursorWhere = " AND (s.updated_at_ms < ? OR (s.updated_at_ms = ? AND s.id < ?))"
		args = append(args, ms, ms, id)
	}
	args = append(args, f.Limit+1)
	q := `SELECT s.id,s.agent,s.provider,s.source_session_id,s.parent_session_id,s.project,s.title,s.started_at_ms,s.updated_at_ms,s.ended_at_ms,s.status,s.is_subagent,s.model,s.measurement,` + sessionAggregateColumns + `
	FROM sessions s LEFT JOIN usage_events ue ON ` + strings.Join(joinParts, " AND ") + ` WHERE ` + strings.Join(whereParts, " AND ") + cursorWhere + ` GROUP BY s.id ORDER BY s.updated_at_ms DESC,s.id DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return coredb.SessionPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := coredb.SessionPage{Sessions: []coredb.Session{}}
	for rows.Next() {
		var s coredb.Session
		var started, ended sql.NullInt64
		var updated int64
		var sub int
		var cost sql.NullInt64
		var ignoredSessions, qr, qd, qe, ql int64
		if err := rows.Scan(&s.ID, &s.Agent, &s.Provider, &s.SourceSessionID, &s.ParentSessionID, &s.Project, &s.Title, &started, &updated, &ended, &s.Status, &sub, &s.Model, &s.Measurement, &s.Tokens.InputUncached, &s.Tokens.CacheRead, &s.Tokens.CacheWrite, &s.Tokens.Output, &s.Tokens.Reasoning, &s.Tokens.Total, &cost, &s.EventCount, &ignoredSessions, &qr, &qd, &qe, &ql); err != nil {
			return page, err
		}
		s.UpdatedAt = time.UnixMilli(updated).UTC()
		if started.Valid {
			v := time.UnixMilli(started.Int64).UTC()
			s.StartedAt = &v
		}
		if ended.Valid {
			v := time.UnixMilli(ended.Int64).UTC()
			s.EndedAt = &v
		}
		s.IsSubagent = sub != 0
		if cost.Valid {
			s.CostMicros = &cost.Int64
		}
		page.Sessions = append(page.Sessions, s)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Sessions) > f.Limit {
		last := page.Sessions[f.Limit-1]
		page.NextCursor = encodeCursor(last.UpdatedAt.UnixMilli(), last.ID)
		page.Sessions = page.Sessions[:f.Limit]
	}
	return page, nil
}

func (r *Repository) GetSession(ctx context.Context, id string) (coredb.Session, error) {
	q := `SELECT s.id,s.agent,s.provider,s.source_session_id,s.parent_session_id,s.project,s.title,s.started_at_ms,s.updated_at_ms,s.ended_at_ms,s.status,s.is_subagent,s.model,s.measurement,` + sessionAggregateColumns + ` FROM sessions s LEFT JOIN usage_events ue ON ue.session_id=s.id WHERE s.id=? GROUP BY s.id`
	var s coredb.Session
	var started, ended sql.NullInt64
	var updated int64
	var sub int
	var cost sql.NullInt64
	var ignoredSessions, qr, qd, qe, ql int64
	err := r.db.QueryRowContext(ctx, q, id).Scan(&s.ID, &s.Agent, &s.Provider, &s.SourceSessionID, &s.ParentSessionID, &s.Project, &s.Title, &started, &updated, &ended, &s.Status, &sub, &s.Model, &s.Measurement, &s.Tokens.InputUncached, &s.Tokens.CacheRead, &s.Tokens.CacheWrite, &s.Tokens.Output, &s.Tokens.Reasoning, &s.Tokens.Total, &cost, &s.EventCount, &ignoredSessions, &qr, &qd, &qe, &ql)
	if err != nil {
		return s, err
	}
	s.UpdatedAt = time.UnixMilli(updated).UTC()
	if started.Valid {
		v := time.UnixMilli(started.Int64).UTC()
		s.StartedAt = &v
	}
	if ended.Valid {
		v := time.UnixMilli(ended.Int64).UTC()
		s.EndedAt = &v
	}
	s.IsSubagent = sub != 0
	if cost.Valid {
		s.CostMicros = &cost.Int64
	}
	return s, nil
}

func (r *Repository) Breakdown(ctx context.Context, filter coredb.QueryFilter, group string) ([]coredb.BreakdownItem, error) {
	column := map[string]string{"agent": "s.agent", "provider": "ue.provider", "model": "ue.model", "project": "s.project", "quality": "ue.measurement"}[group]
	if column == "" {
		return nil, fmt.Errorf("unsupported breakdown %q", group)
	}
	f := normalizeFilter(filter)
	where, args := usageWhere(f, "ue")
	rows, err := r.db.QueryContext(ctx, `SELECT `+column+`,`+aggregateColumns+` FROM usage_events ue JOIN sessions s ON s.id=ue.session_id WHERE `+where+` GROUP BY `+column+` ORDER BY SUM(ue.total_tokens) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []coredb.BreakdownItem{}
	for rows.Next() {
		var item coredb.BreakdownItem
		var cost sql.NullInt64
		var events int64
		if err := rows.Scan(&item.Key, &item.Tokens.InputUncached, &item.Tokens.CacheRead, &item.Tokens.CacheWrite, &item.Tokens.Output, &item.Tokens.Reasoning, &item.Tokens.Total, &cost, &events, &item.Sessions, &item.Quality.Reported, &item.Quality.Derived, &item.Quality.Estimated, &item.Quality.Legacy); err != nil {
			return nil, err
		}
		if cost.Valid {
			item.CostMicros = &cost.Int64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *Repository) LastSync(ctx context.Context) (coredb.SyncStatus, error) {
	var out coredb.SyncStatus
	var started int64
	var finished sql.NullInt64
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT id,started_at_ms,finished_at_ms,status,inserted_count,updated_count,skipped_count,error_count,diagnostics_json FROM sync_runs ORDER BY id DESC LIMIT 1`).Scan(&out.ID, &started, &finished, &out.Status, &out.Inserted, &out.Updated, &out.Skipped, &out.Errors, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		out.Status = "never"
		out.Diagnostics = []string{}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.StartedAt = time.UnixMilli(started).UTC()
	if finished.Valid {
		v := time.UnixMilli(finished.Int64).UTC()
		out.FinishedAt = &v
	}
	_ = json.Unmarshal([]byte(raw), &out.Diagnostics)
	return out, nil
}

func (r *Repository) GetAgentStats() ([]coredb.AgentStats, error) {
	items, err := r.Breakdown(context.Background(), coredb.QueryFilter{From: time.Unix(0, 0), To: time.Now().AddDate(10, 0, 0)}, "agent")
	if err != nil {
		return nil, err
	}
	out := make([]coredb.AgentStats, 0, len(items))
	for _, i := range items {
		cost := float64(0)
		if i.CostMicros != nil {
			cost = float64(*i.CostMicros) / 1e6
		}
		out = append(out, coredb.AgentStats{Name: i.Key, InputTokens: i.Tokens.InputUncached + i.Tokens.CacheRead + i.Tokens.CacheWrite, OutputTokens: i.Tokens.Output + i.Tokens.Reasoning, Cost: cost, Jobs: i.Sessions})
	}
	return out, nil
}

func (r *Repository) GetRecentLogs(limit int) ([]string, error) {
	p, err := r.ListSessions(context.Background(), coredb.QueryFilter{From: time.Unix(0, 0), To: time.Now().AddDate(10, 0, 0), Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(p.Sessions))
	for _, s := range p.Sessions {
		out = append(out, fmt.Sprintf("%s [%s] %s tokens=%d quality=%s", s.UpdatedAt.Format(time.RFC3339), s.Agent, s.Model, s.Tokens.Total, s.Measurement))
	}
	return out, nil
}

func encodeCursor(ms int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", ms, id)))
}
func decodeCursor(v string) (int64, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	return ms, parts[1], nil
}
func addTokens(dst *coredb.TokenCounts, src coredb.TokenCounts) {
	dst.InputUncached += src.InputUncached
	dst.CacheRead += src.CacheRead
	dst.CacheWrite += src.CacheWrite
	dst.Output += src.Output
	dst.Reasoning += src.Reasoning
	dst.Total += src.Total
}
func addQuality(q *coredb.QualityCoverage, m coredb.Measurement, n int64) {
	switch m {
	case coredb.MeasurementReported:
		q.Reported += n
	case coredb.MeasurementDerived:
		q.Derived += n
	case coredb.MeasurementEstimated:
		q.Estimated += n
	case coredb.MeasurementLegacy:
		q.Legacy += n
	}
}
