package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

// InitializeAccounting creates the new accounting population, leaving all
// legacy tables untouched. Call from background startup, never inference.
// TrackingSince is inserted once, transactionally with the new schema.
func (s *SQLiteStore) InitializeAccounting(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS accounting_metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS inference_attempts (
   id TEXT PRIMARY KEY,revision INTEGER NOT NULL CHECK(revision>0),
   operation_id TEXT NOT NULL,conversation_id TEXT NOT NULL,session_id TEXT NOT NULL,worker_id TEXT NOT NULL,
   source TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,profile TEXT NOT NULL,destination TEXT NOT NULL,
   started_at INTEGER NOT NULL,ended_at INTEGER,
   outcome TEXT NOT NULL CHECK(outcome IN ('started','completed','failed','interrupted')),
   input_tokens INTEGER CHECK(input_tokens>=0),output_tokens INTEGER CHECK(output_tokens>=0),
   cache_read_tokens INTEGER CHECK(cache_read_tokens>=0),cache_write_tokens INTEGER CHECK(cache_write_tokens>=0),reasoning_tokens INTEGER CHECK(reasoning_tokens>=0),usage_final INTEGER NOT NULL CHECK(usage_final IN (0,1)))`,
		`CREATE INDEX IF NOT EXISTS inference_attempts_time ON inference_attempts(started_at)`,
		`CREATE INDEX IF NOT EXISTS inference_attempts_provider_model_time ON inference_attempts(provider,model,started_at)`,
		`CREATE INDEX IF NOT EXISTS inference_attempts_source_time ON inference_attempts(source,started_at)`,
		`CREATE INDEX IF NOT EXISTS inference_attempts_session_time ON inference_attempts(session_id,started_at)`,
		`CREATE TABLE IF NOT EXISTS accounting_health (writer_id TEXT PRIMARY KEY,updated_at INTEGER NOT NULL,snapshot TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS accounting_operations (
   id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,session_id TEXT NOT NULL,source TEXT NOT NULL,
   started_at INTEGER NOT NULL,ended_at INTEGER,outcome TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS accounting_operations_time ON accounting_operations(started_at)`,
		`CREATE TABLE IF NOT EXISTS external_usage_reports (
   id TEXT PRIMARY KEY,reported_at INTEGER NOT NULL,reporter TEXT NOT NULL,
   provider TEXT NOT NULL,model TEXT NOT NULL,stable_identity INTEGER NOT NULL,
   input_tokens INTEGER CHECK(input_tokens>=0),output_tokens INTEGER CHECK(output_tokens>=0))`,
		`CREATE INDEX IF NOT EXISTS external_usage_reports_time ON external_usage_reports(reported_at)`,
	} {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var version string
	err = tx.QueryRowContext(ctx, `SELECT value FROM accounting_metadata WHERE key='schema_version'`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && version != "1" {
		return fmt.Errorf("unsupported accounting schema version %q", version)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO accounting_metadata(key,value) VALUES('schema_version','1') ON CONFLICT(key) DO NOTHING`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO accounting_metadata(key,value) VALUES('tracking_since',?) ON CONFLICT(key) DO NOTHING`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) TrackingSince(ctx context.Context) (time.Time, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM accounting_metadata WHERE key='tracking_since'`).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}

func nullableCount(c llm.TokenCount) any {
	if !c.Known {
		return nil
	}
	return c.Value
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().UnixMicro()
}

// ValidateAttempt bounds metadata and rejects malformed observations. It never
// inspects prompts, serializes events, touches storage, or repairs attribution.
func ValidateAttempt(a usage.AttemptObservation) error {
	if a.ID == "" || a.Revision == 0 || a.Revision > math.MaxInt64 || a.StartedAt.IsZero() {
		return fmt.Errorf("invalid attempt identity or start")
	}
	for _, v := range []string{a.ID, a.Attribution.OperationID, a.Attribution.ConversationID, a.Attribution.SessionID, a.Attribution.WorkerID, a.Attribution.Source, a.Provider, a.Model, a.Profile, a.Destination} {
		if len(v) > 1024 {
			return fmt.Errorf("attempt metadata exceeds limit")
		}
	}
	switch a.Outcome {
	case usage.Started:
		if !a.EndedAt.IsZero() {
			return fmt.Errorf("started attempt has end time")
		}
	case usage.Completed, usage.Failed, usage.Interrupted:
		if a.EndedAt.IsZero() || a.EndedAt.Before(a.StartedAt) {
			return fmt.Errorf("invalid attempt end")
		}
	default:
		return fmt.Errorf("invalid attempt outcome")
	}
	for _, c := range []llm.TokenCount{a.Tokens.Input, a.Tokens.Output, a.Tokens.CacheRead, a.Tokens.CacheWrite, a.Tokens.Reasoning} {
		if c.Known && c.Value < 0 {
			return fmt.Errorf("negative usage")
		}
	}
	return nil
}

// WriteAttempts atomically persists bounded batches. Retransmission and stale
// snapshots are idempotent; primary-key identity defines one physical attempt.
func (s *SQLiteStore) WriteAttempts(ctx context.Context, batch []usage.AttemptObservation) error {
	if len(batch) == 0 {
		return nil
	}
	if len(batch) > 256 {
		return fmt.Errorf("attempt batch exceeds 256 observations")
	}
	for _, a := range batch {
		if err := ValidateAttempt(a); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO inference_attempts
 (id,revision,operation_id,conversation_id,session_id,worker_id,source,provider,model,profile,destination,started_at,ended_at,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,reasoning_tokens,usage_final)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,provider=excluded.provider,model=excluded.model,
 profile=excluded.profile,destination=excluded.destination,ended_at=excluded.ended_at,outcome=excluded.outcome,
 input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,cache_read_tokens=excluded.cache_read_tokens,
 cache_write_tokens=excluded.cache_write_tokens,reasoning_tokens=excluded.reasoning_tokens,usage_final=excluded.usage_final
 WHERE excluded.revision>inference_attempts.revision AND (inference_attempts.outcome='started' OR excluded.outcome!='started')`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range batch {
		_, err = stmt.ExecContext(ctx, a.ID, a.Revision, a.Attribution.OperationID, a.Attribution.ConversationID, a.Attribution.SessionID, a.Attribution.WorkerID, a.Attribution.Source, a.Provider, a.Model, a.Profile, a.Destination, a.StartedAt.UTC().UnixMicro(), nullableTime(a.EndedAt), a.Outcome, nullableCount(a.Tokens.Input), nullableCount(a.Tokens.Output), nullableCount(a.Tokens.CacheRead), nullableCount(a.Tokens.CacheWrite), nullableCount(a.Tokens.Reasoning), a.Tokens.Final)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AccountingAttempt reads a single attempt for diagnostics/tests, not UI history.
func (s *SQLiteStore) AccountingAttempt(ctx context.Context, id string) (usage.AttemptObservation, error) {
	var a usage.AttemptObservation
	var start int64
	var end sql.NullInt64
	var counts [5]sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,revision,operation_id,conversation_id,session_id,worker_id,source,provider,model,profile,destination,started_at,ended_at,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,reasoning_tokens,usage_final FROM inference_attempts WHERE id=?`, id).Scan(&a.ID, &a.Revision, &a.Attribution.OperationID, &a.Attribution.ConversationID, &a.Attribution.SessionID, &a.Attribution.WorkerID, &a.Attribution.Source, &a.Provider, &a.Model, &a.Profile, &a.Destination, &start, &end, &a.Outcome, &counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &a.Tokens.Final)
	if err != nil {
		return a, err
	}
	a.StartedAt = time.UnixMicro(start).UTC()
	if end.Valid {
		a.EndedAt = time.UnixMicro(end.Int64).UTC()
	}
	targets := []*llm.TokenCount{&a.Tokens.Input, &a.Tokens.Output, &a.Tokens.CacheRead, &a.Tokens.CacheWrite, &a.Tokens.Reasoning}
	for i, v := range counts {
		if v.Valid {
			*targets[i] = llm.ReportedTokens(v.Int64)
		}
	}
	return a, nil
}

// WriteAccountingHealth persists independent loss/uncertainty counters after
// recovery, even when the observation queue was full. Called only in background.
func (s *SQLiteStore) WriteAccountingHealth(ctx context.Context, writer string, h AccountingHealth) error {
	payload, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO accounting_health(writer_id,updated_at,snapshot) VALUES(?,?,?) ON CONFLICT(writer_id) DO UPDATE SET updated_at=excluded.updated_at,snapshot=excluded.snapshot WHERE COALESCE(json_extract(excluded.snapshot,'$.sequence'),0)>=COALESCE(json_extract(accounting_health.snapshot,'$.sequence'),0)`, writer, time.Now().UTC().UnixMicro(), string(payload))
	return err
}
