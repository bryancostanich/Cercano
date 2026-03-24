package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Event represents a single telemetry event from a local inference request.
type Event struct {
	Timestamp     time.Time
	ToolName      string
	Model         string
	InputTokens   int
	OutputTokens  int
	DurationMs    int64
	WasEscalated  bool
	CloudProvider string
	CloudModel    string
	TokenSaving   bool // true if this call substitutes for a cloud call (counts toward savings)
	startTime     time.Time
}

// NewEvent creates a new telemetry event with the current timestamp.
func NewEvent(toolName, model string) *Event {
	now := time.Now()
	return &Event{
		Timestamp:   now,
		ToolName:    toolName,
		Model:       model,
		TokenSaving: true, // default: most calls substitute for cloud calls
		startTime:   now,
	}
}

// Complete finalizes the event with token counts and duration.
func (e *Event) Complete(inputTokens, outputTokens int, wasEscalated bool, cloudProvider, cloudModel string) {
	e.InputTokens = inputTokens
	e.OutputTokens = outputTokens
	e.DurationMs = time.Since(e.startTime).Milliseconds()
	e.WasEscalated = wasEscalated
	e.CloudProvider = cloudProvider
	e.CloudModel = cloudModel
}

// CloudUsageReport represents host-reported cloud token usage.
type CloudUsageReport struct {
	Timestamp         time.Time
	CloudInputTokens  int
	CloudOutputTokens int
	CloudProvider     string
	CloudModel        string
}

// GroupStats holds aggregated stats for a named group (tool, model, or date).
type GroupStats struct {
	Name         string
	Count        int
	InputTokens  int
	OutputTokens int
}

// Stats holds aggregated telemetry statistics.
type Stats struct {
	TotalRequests          int
	TotalInputTokens       int
	TotalOutputTokens      int
	LocalTokensSaved       int     // input + output for non-escalated requests
	TotalCloudInputTokens  int
	TotalCloudOutputTokens int
	LocalPercentage        float64 // percentage of total tokens handled locally (0-100)
	ByTool                 []GroupStats
	ByModel                []GroupStats
	ByDay                  []GroupStats
}

// ComputeSavings calculates the LocalPercentage from local and cloud totals.
func (s *Stats) ComputeSavings() {
	totalLocal := s.LocalTokensSaved
	totalCloud := s.TotalCloudInputTokens + s.TotalCloudOutputTokens
	total := totalLocal + totalCloud
	if total > 0 {
		s.LocalPercentage = float64(totalLocal) / float64(total) * 100
	}
}

// Store defines the interface for telemetry persistence.
type Store interface {
	RecordEvent(ctx context.Context, e *Event) error
	RecordCloudUsage(ctx context.Context, r CloudUsageReport) error
	GetStats(ctx context.Context) (*Stats, error)
	Close() error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite-backed telemetry store.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create telemetry directory: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open telemetry database: %w", err)
	}

	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

func createTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			tool_name TEXT NOT NULL,
			model TEXT NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			was_escalated BOOLEAN NOT NULL DEFAULT 0,
			cloud_provider TEXT NOT NULL DEFAULT '',
			cloud_model TEXT NOT NULL DEFAULT '',
			token_saving BOOLEAN NOT NULL DEFAULT 1
		);

		CREATE TABLE IF NOT EXISTS cloud_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL,
			cloud_input_tokens INTEGER NOT NULL DEFAULT 0,
			cloud_output_tokens INTEGER NOT NULL DEFAULT 0,
			cloud_provider TEXT NOT NULL DEFAULT '',
			cloud_model TEXT NOT NULL DEFAULT ''
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to create telemetry tables: %w", err)
	}
	return nil
}

// migrateSchema adds columns that may be missing from older databases.
func migrateSchema(db *sql.DB) error {
	// Add token_saving column if it doesn't exist (added in v0.x)
	_, err := db.Exec(`ALTER TABLE events ADD COLUMN token_saving BOOLEAN NOT NULL DEFAULT 1`)
	if err != nil {
		// Column already exists — ignore the error
		if !strings.Contains(err.Error(), "duplicate column") {
			// Unexpected error
			return nil // non-fatal, proceed anyway
		}
	}
	return nil
}

// RecordEvent persists a telemetry event.
func (s *SQLiteStore) RecordEvent(ctx context.Context, e *Event) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO events (timestamp, tool_name, model, input_tokens, output_tokens, duration_ms, was_escalated, cloud_provider, cloud_model, token_saving)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp, e.ToolName, e.Model, e.InputTokens, e.OutputTokens, e.DurationMs, e.WasEscalated, e.CloudProvider, e.CloudModel, e.TokenSaving,
	)
	return err
}

// RecordCloudUsage persists a host-reported cloud usage report.
func (s *SQLiteStore) RecordCloudUsage(ctx context.Context, r CloudUsageReport) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cloud_usage (timestamp, cloud_input_tokens, cloud_output_tokens, cloud_provider, cloud_model)
		 VALUES (?, ?, ?, ?, ?)`,
		r.Timestamp, r.CloudInputTokens, r.CloudOutputTokens, r.CloudProvider, r.CloudModel,
	)
	return err
}

// GetStats returns aggregated telemetry statistics.
func (s *SQLiteStore) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{}

	// Totals from events
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(COUNT(*), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN was_escalated = 0 AND token_saving = 1 THEN input_tokens + output_tokens ELSE 0 END), 0)
		FROM events
	`)
	if err := row.Scan(&stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens, &stats.LocalTokensSaved); err != nil {
		return nil, fmt.Errorf("failed to query event stats: %w", err)
	}

	// Cloud usage totals
	row = s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(cloud_input_tokens), 0),
			COALESCE(SUM(cloud_output_tokens), 0)
		FROM cloud_usage
	`)
	if err := row.Scan(&stats.TotalCloudInputTokens, &stats.TotalCloudOutputTokens); err != nil {
		return nil, fmt.Errorf("failed to query cloud usage stats: %w", err)
	}

	// By tool
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool_name, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM events
		GROUP BY tool_name
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var gs GroupStats
		if err := rows.Scan(&gs.Name, &gs.Count, &gs.InputTokens, &gs.OutputTokens); err != nil {
			return nil, fmt.Errorf("failed to scan tool stats: %w", err)
		}
		stats.ByTool = append(stats.ByTool, gs)
	}
	rows.Close()

	// By model
	rows, err = s.db.QueryContext(ctx, `
		SELECT model, COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM events
		GROUP BY model
		ORDER BY COUNT(*) DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query model stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var gs GroupStats
		if err := rows.Scan(&gs.Name, &gs.Count, &gs.InputTokens, &gs.OutputTokens); err != nil {
			return nil, fmt.Errorf("failed to scan model stats: %w", err)
		}
		stats.ByModel = append(stats.ByModel, gs)
	}

	// By day
	dayRows, err := s.db.QueryContext(ctx, `
		SELECT DATE(timestamp), COUNT(*), COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0)
		FROM events
		GROUP BY DATE(timestamp)
		ORDER BY DATE(timestamp) DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query daily stats: %w", err)
	}
	defer dayRows.Close()

	for dayRows.Next() {
		var gs GroupStats
		if err := dayRows.Scan(&gs.Name, &gs.Count, &gs.InputTokens, &gs.OutputTokens); err != nil {
			return nil, fmt.Errorf("failed to scan daily stats: %w", err)
		}
		stats.ByDay = append(stats.ByDay, gs)
	}

	stats.ComputeSavings()
	return stats, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Collector provides async, non-blocking telemetry collection.
// MCP handlers call Emit/EmitCloudUsage without waiting for the write to complete.
type Collector struct {
	store    Store
	events   chan *Event
	cloud    chan CloudUsageReport
	done     chan struct{}
}

// NewCollector creates a Collector that drains events to the given store.
// bufferSize controls the channel capacity; events are dropped if the buffer is full.
func NewCollector(store Store, bufferSize int) *Collector {
	c := &Collector{
		store:  store,
		events: make(chan *Event, bufferSize),
		cloud:  make(chan CloudUsageReport, bufferSize),
		done:   make(chan struct{}),
	}
	go c.drain()
	return c
}

// Store returns the underlying store for direct queries (e.g., GetStats).
func (c *Collector) Store() Store {
	return c.store
}

// Emit queues a telemetry event for async persistence. Non-blocking; drops if buffer full.
func (c *Collector) Emit(e *Event) {
	select {
	case c.events <- e:
	default:
		// Buffer full — drop silently to avoid blocking the request path.
	}
}

// EmitCloudUsage queues a cloud usage report for async persistence.
func (c *Collector) EmitCloudUsage(r CloudUsageReport) {
	select {
	case c.cloud <- r:
	default:
	}
}

// Close drains remaining events and shuts down the collector.
func (c *Collector) Close() {
	close(c.events)
	close(c.cloud)
	<-c.done
}

func (c *Collector) drain() {
	defer close(c.done)
	for {
		select {
		case e, ok := <-c.events:
			if !ok {
				// Channel closed — drain remaining cloud reports and exit.
				for r := range c.cloud {
					if err := c.store.RecordCloudUsage(context.Background(), r); err != nil {
						log.Printf("telemetry: failed to record cloud usage: %v", err)
					}
				}
				return
			}
			if err := c.store.RecordEvent(context.Background(), e); err != nil {
				log.Printf("telemetry: failed to record event: %v", err)
			}
		case r, ok := <-c.cloud:
			if !ok {
				continue
			}
			if err := c.store.RecordCloudUsage(context.Background(), r); err != nil {
				log.Printf("telemetry: failed to record cloud usage: %v", err)
			}
		}
	}
}
