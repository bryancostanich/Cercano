package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"cercano/source/server/pkg/proto"
)

var ErrMetricsRequest = errors.New("invalid metrics request")

// MetricsBounds converts viewer calendar dates into half-open UTC bounds.
// Calendar arithmetic is intentional: a local day need not be 24 hours.
func MetricsBounds(r *proto.GetTokenMetricsRequest, now, cutover time.Time) (time.Time, time.Time, *time.Location, error) {
	bad := func() (time.Time, time.Time, *time.Location, error) {
		return time.Time{}, time.Time{}, nil, ErrMetricsRequest
	}
	if r == nil || len(r.Timezone) > 128 || r.Timezone == "" || r.Timezone == "Local" {
		return bad()
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return bad()
	}
	if r.Population != "" && r.Population != "attempts" && r.Population != "external" {
		return bad()
	}
	for _, v := range []*string{r.Provider, r.Model, r.Source} {
		if v != nil && (len(*v) > 1024 || strings.ContainsRune(*v, 0)) {
			return bad()
		}
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start, end := today, today.AddDate(0, 0, 1)
	switch r.Preset {
	case "", "today":
	case "7d":
		start = today.AddDate(0, 0, -6)
	case "30d":
		start = today.AddDate(0, 0, -29)
	case "all":
		start = cutover
		if start.After(end) {
			return bad()
		}
	case "custom":
		start, err = time.ParseInLocation("2006-01-02", r.StartDate, loc)
		if err != nil {
			return bad()
		}
		end, err = time.ParseInLocation("2006-01-02", r.EndDate, loc)
		if err != nil {
			return bad()
		}
		end = end.AddDate(0, 0, 1)
	case "utc":
		start = time.UnixMicro(r.StartUnixMicros)
		end = time.UnixMicro(r.EndUnixMicros)
	default:
		return bad()
	}
	if !end.After(start) || start.Year() < 1970 || end.Year() > 9999 || end.Sub(start) > 100*366*24*time.Hour {
		return bad()
	}
	// New accounting history before the cutover cannot contribute to new totals.
	if start.Before(cutover) {
		start = cutover
		if start.After(end) {
			start = end
		}
	}
	return start.UTC(), end.UTC(), loc, nil
}

const metricsWarnings = "Reported usage only; unknown counters are not zero. Cache and reasoning categories are subsets, not additional total tokens. OpenAI/Ollama SDK-ambiguous zero counters remain unknown. Coverage of all inference paths has not yet been certified; this is not billing-grade accounting."

// QueryTokenMetrics uses a consistent read transaction and bounded aggregate
// results. It never opens or initializes a production database or reads legacy
// records. now is explicit for deterministic tests. live replaces one persisted
// writer snapshot, avoiding double counting host health.
func (s *SQLiteStore) QueryTokenMetrics(ctx context.Context, r *proto.GetTokenMetricsRequest, now time.Time, writer string, live *AccountingHealth) (*proto.GetTokenMetricsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT value FROM accounting_metadata WHERE key='tracking_since'").Scan(&raw); err != nil {
		return nil, err
	}
	cutover, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, err
	}
	start, end, loc, err := MetricsBounds(r, now, cutover)
	if err != nil {
		return nil, err
	}
	out := &proto.GetTokenMetricsResponse{Population: r.Population, Timezone: r.Timezone, TrackingSince: raw, StartUnixMicros: start.UnixMicro(), EndUnixMicros: end.UnixMicro(), GeneratedAt: now.UTC().Format(time.RFC3339Nano), Warnings: []string{metricsWarnings}}
	if out.Population == "" {
		out.Population = "attempts"
	}
	table, stamp, source := "inference_attempts", "started_at", "source"
	extra := "cache_read_tokens,cache_write_tokens,reasoning_tokens"
	incomplete := "usage_final=0 OR input_tokens IS NULL OR output_tokens IS NULL"
	unfinished := "outcome='started'"
	if out.Population == "external" {
		table, stamp, source = "external_usage_reports", "reported_at", "reporter"
		extra = "NULL,NULL,NULL"
		incomplete = "input_tokens IS NULL OR output_tokens IS NULL"
		unfinished = "0"
		out.Warnings = append(out.Warnings, "External reports may overlap internal attempts. Do not add populations together. Source filter selects reporter; report ingestion coverage is not certified.")
	}
	// A normalized subquery keeps all aggregate paths on the identical population.
	base := fmt.Sprintf("SELECT provider,model,%s AS source,%s AS timestamp,input_tokens,output_tokens,%s,(%s) AS incomplete,(%s) AS unfinished FROM %s WHERE %s>=? AND %s<?", source, stamp, extra, incomplete, unfinished, table, stamp, stamp)
	// Alias nullable external-only categories explicitly.
	if out.Population == "external" {
		base = strings.Replace(base, "NULL,NULL,NULL", "NULL AS cache_read_tokens,NULL AS cache_write_tokens,NULL AS reasoning_tokens", 1)
	}
	args := []any{start.UnixMicro(), end.UnixMicro()}
	for i, v := range []*string{r.Provider, r.Model, r.Source} {
		if v != nil {
			col := []string{"provider", "model", source}[i]
			base += " AND " + col + "=?"
			args = append(args, *v)
		}
	}
	query := func(suffix string, more ...any) (*sql.Rows, error) {
		a := append(append([]any{}, args...), more...)
		return tx.QueryContext(ctx, "SELECT "+metricAggregate+" FROM ("+base+") "+suffix, a...)
	}
	rows, err := query("")
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		out.Totals, err = scanMetricTotals(rows, nil)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	// At most 120 calendar buckets, widening for long ranges, never truncating totals.
	days := int(end.Sub(start)/(24*time.Hour)) + 2
	stride := (days + 119) / 120
	if stride < 1 {
		stride = 1
	}
	cursor := start
	for cursor.Before(end) {
		local := cursor.In(loc)
		boundary := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, stride).UTC()
		if boundary.After(end) {
			boundary = end
		}
		rows, err = query("WHERE timestamp>=? AND timestamp<?", cursor.UnixMicro(), boundary.UnixMicro())
		if err != nil {
			return nil, err
		}
		b := &proto.TokenMetricBucket{StartUnixMicros: cursor.UnixMicro(), EndUnixMicros: boundary.UnixMicro(), Label: local.Format("2006-01-02"), Partial: cursor.Before(now) && boundary.After(now)}
		if !cursor.Equal(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)) {
			b.Partial = true
		}
		if rows.Next() {
			b.Totals, err = scanMetricTotals(rows, nil)
		}
		rowErr = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if rowErr != nil {
			return nil, rowErr
		}
		out.Buckets = append(out.Buckets, b)
		cursor = boundary
	}
	for _, dim := range []string{"provider", "model", "source"} {
		rows, err = tx.QueryContext(ctx, "SELECT "+dim+","+metricAggregate+" FROM ("+base+") GROUP BY "+dim+" ORDER BY COALESCE(SUM(input_tokens),0)+COALESCE(SUM(output_tokens),0) DESC,"+dim+" LIMIT 51", args...)
		if err != nil {
			return nil, err
		}
		n := 0
		for rows.Next() {
			var value string
			total, e := scanMetricTotals(rows, []any{&value})
			if e != nil {
				rows.Close()
				return nil, e
			}
			n++
			if n > 50 {
				out.BreakdownsTruncated = true
				continue
			}
			out.Breakdowns = append(out.Breakdowns, &proto.TokenMetricBreakdown{Dimension: dim, Value: value, Totals: total})
		}
		rowErr = rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
	}
	out.Health, err = queryMetricsHealth(ctx, tx, writer, live)
	if err != nil {
		return nil, err
	}
	if out.Health.CoverageIncomplete || out.Health.Lost > 0 || out.Health.Uncertain > 0 {
		out.Warnings = append(out.Warnings, "Known accounting gaps exist. Health is global: gap dates are unavailable, so empty buckets mean no records, not measured zero usage.")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

const metricAggregate = `COUNT(*),COALESCE(SUM(input_tokens),0),COUNT(input_tokens),COALESCE(SUM(output_tokens),0),COUNT(output_tokens),COALESCE(SUM(cache_read_tokens),0),COUNT(cache_read_tokens),COALESCE(SUM(cache_write_tokens),0),COUNT(cache_write_tokens),COALESCE(SUM(reasoning_tokens),0),COUNT(reasoning_tokens),COALESCE(SUM(incomplete),0),COALESCE(SUM(provider='' OR model='' OR source=''),0),COALESCE(SUM(unfinished),0)`

func scanMetricTotals(rows *sql.Rows, prefix []any) (*proto.TokenMetricTotals, error) {
	t := &proto.TokenMetricTotals{Input: &proto.TokenMetricCount{}, Output: &proto.TokenMetricCount{}, CacheRead: &proto.TokenMetricCount{}, CacheWrite: &proto.TokenMetricCount{}, Reasoning: &proto.TokenMetricCount{}}
	args := append(prefix, &t.Records, &t.Input.Tokens, &t.Input.KnownRecords, &t.Output.Tokens, &t.Output.KnownRecords, &t.CacheRead.Tokens, &t.CacheRead.KnownRecords, &t.CacheWrite.Tokens, &t.CacheWrite.KnownRecords, &t.Reasoning.Tokens, &t.Reasoning.KnownRecords, &t.Incomplete, &t.UnknownAttribution, &t.Unfinished)
	return t, rows.Scan(args...)
}

func queryMetricsHealth(ctx context.Context, tx *sql.Tx, writer string, live *AccountingHealth) (*proto.TokenMetricsHealth, error) {
	// Aggregation bounds payload regardless of the number of historical writers.
	// Historical pending work is uncertain rather than evidence of a live queue.
	h := &proto.TokenMetricsHealth{}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(json_extract(snapshot,'$.pending')),0),COALESCE(SUM(json_extract(snapshot,'$.retries')),0),COALESCE(SUM(json_extract(snapshot,'$.write_failures')),0),COALESCE(SUM(json_extract(snapshot,'$.lost')),0),COALESCE(SUM(json_extract(snapshot,'$.uncertain')),0),COALESCE(MAX(json_extract(snapshot,'$.coverage_incomplete')),0),COALESCE(MAX(NULLIF(json_extract(snapshot,'$.last_persistence'),'0001-01-01T00:00:00Z')),''),COALESCE(MAX(json_extract(snapshot,'$.last_error')),''),COALESCE(MIN(NULLIF(json_extract(snapshot,'$.oldest_pending'),'0001-01-01T00:00:00Z')),'') FROM accounting_health WHERE writer_id!=?`, writer).Scan(&h.Pending, &h.Retries, &h.WriteFailures, &h.Lost, &h.Uncertain, &h.CoverageIncomplete, &h.LastPersistence, &h.LastError, &h.OldestPending)
	if err != nil {
		return nil, err
	}
	// RFC3339Nano is not lexically sortable when fractional precision differs.
	// Keep aggregation in SQL, selecting only the extreme timestamp per field.
	for _, q := range []struct {
		field, order string
		target       *string
	}{
		{"last_persistence", "DESC", &h.LastPersistence}, {"oldest_pending", "ASC", &h.OldestPending},
	} {
		value := "json_extract(snapshot,'$." + q.field + "')"
		query := "SELECT COALESCE((SELECT " + value + " FROM accounting_health WHERE writer_id!=? AND " + value + "!='0001-01-01T00:00:00Z' AND julianday(" + value + ") IS NOT NULL ORDER BY julianday(" + value + ") " + q.order + ",rtrim(" + value + ",'Z') " + q.order + " LIMIT 1),'')"
		if err = tx.QueryRowContext(ctx, query, writer).Scan(q.target); err != nil {
			return nil, err
		}
	}
	if h.Pending > 0 {
		h.CoverageIncomplete = true
	}
	if live != nil {
		h.Pending += int64(live.Pending)
		h.Retries += live.Retries
		h.WriteFailures += live.WriteFailures
		h.Lost += live.Lost
		h.Uncertain += live.Uncertain
		h.CoverageIncomplete = h.CoverageIncomplete || live.CoverageIncomplete
		if !live.LastPersistence.IsZero() {
			t := live.LastPersistence.UTC().Format(time.RFC3339Nano)
			if metricsTimeAfter(t, h.LastPersistence) {
				h.LastPersistence = t
			}
		}
		if live.LastError != "" {
			h.LastError = live.LastError
		}
		if !live.OldestPending.IsZero() {
			t := live.OldestPending.UTC().Format(time.RFC3339Nano)
			if h.OldestPending == "" || metricsTimeAfter(h.OldestPending, t) {
				h.OldestPending = t
			}
		}
	}
	return h, nil
}

// TokenMetrics runs only on the reporting path, independent of collector admission.
func (c *Collector) TokenMetrics(ctx context.Context, r *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error) {
	store, ok := c.store.(*SQLiteStore)
	if !ok {
		return nil, errors.New("metrics storage unavailable")
	}
	c.mu.RLock()
	attempts := c.attempts
	c.mu.RUnlock()
	if attempts == nil {
		return nil, errors.New("attempt accounting not enabled")
	}
	h := attempts.Health()
	return store.QueryTokenMetrics(ctx, r, time.Now(), attempts.WriterID(), &h)
}

func metricsTimeAfter(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	return ea == nil && (eb != nil || ta.After(tb))
}
