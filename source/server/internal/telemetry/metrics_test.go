package telemetry

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/proto"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMetricsCalendar(t *testing.T) {
	cut := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		date  string
		hours int
	}{{"2026-03-08", 23}, {"2026-11-01", 25}} {
		r := &proto.GetTokenMetricsRequest{Preset: "custom", Timezone: "America/New_York", StartDate: tt.date, EndDate: tt.date}
		a, b, _, e := MetricsBounds(r, time.Now(), cut)
		if e != nil || b.Sub(a) != time.Duration(tt.hours)*time.Hour {
			t.Fatalf("%s: %v %v %v", tt.date, a, b, e)
		}
	}
	now := time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC)
	a, b, _, e := MetricsBounds(&proto.GetTokenMetricsRequest{Preset: "7d", Timezone: "America/New_York"}, now, cut)
	if e != nil || a.Format(time.RFC3339) != "2026-03-03T05:00:00Z" || b.Format(time.RFC3339) != "2026-03-10T04:00:00Z" {
		t.Fatalf("calendar: %v %v %v", a, b, e)
	}
	for _, r := range []*proto.GetTokenMetricsRequest{nil, {Timezone: "Local"}, {Timezone: "not/a/zone"}, {Timezone: "UTC", Preset: "oops"}, {Timezone: "UTC", Population: "union"}, {Timezone: "UTC", Preset: "custom", StartDate: "2026-02-30", EndDate: "2026-03-01"}, {Timezone: "UTC", Preset: "custom", StartDate: "2026-03-05", EndDate: "2026-03-01"}, {Timezone: "UTC", Provider: strptr(strings.Repeat("x", 1025))}} {
		if _, _, _, e := MetricsBounds(r, now, cut); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}
func strptr(v string) *string { return &v }
func metricsStore(t *testing.T) *SQLiteStore {
	s := accountingTestStore(t)
	if _, e := s.db.Exec("UPDATE accounting_metadata SET value='2026-01-01T00:00:00Z' WHERE key='tracking_since'"); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestMetricsPopulationPresenceAndFilters(t *testing.T) {
	s := metricsStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	a := accountingFixture("a")
	a.Tokens = llm.TokenUsage{Input: llm.ReportedTokens(10), Output: llm.ReportedTokens(0), CacheRead: llm.ReportedTokens(3), Final: true}
	a.Outcome = usage.Completed
	a.EndedAt = a.StartedAt.Add(time.Second)
	b := accountingFixture("b")
	b.Provider = ""
	b.Model = ""
	b.Tokens.Input = llm.ReportedTokens(5)
	c := a
	c.ID = "c"
	c.StartedAt = c.StartedAt.AddDate(0, 0, 1)
	c.EndedAt = c.StartedAt.Add(time.Second)
	c.Tokens.Output = llm.ReportedTokens(2)
	if e := s.WriteAttempts(ctx, []usage.AttemptObservation{a, b, c}); e != nil {
		t.Fatal(e)
	}
	// These legacy records and external observations must never join primary totals.
	if e := s.RecordEvent(ctx, NewEvent("legacy", "fake")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("INSERT INTO external_usage_reports VALUES('external',?,'host','fake','fake',1,900,100)", a.StartedAt.UnixMicro()); e != nil {
		t.Fatal(e)
	}
	r := &proto.GetTokenMetricsRequest{Preset: "7d", Timezone: "UTC"}
	out, e := s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if out.Totals.Records != 3 || out.Totals.Input.Tokens != 25 || out.Totals.Output.Tokens != 2 || out.Totals.Output.KnownRecords != 2 || out.Totals.CacheRead.Tokens != 6 || out.Totals.Incomplete != 1 || out.Totals.UnknownAttribution != 1 || out.Totals.Unfinished != 1 {
		t.Fatalf("%+v", out.Totals)
	}
	var in, records int64
	for _, b := range out.Buckets {
		in += b.Totals.Input.Tokens
		records += b.Totals.Records
	}
	if in != 25 || records != 3 {
		t.Fatalf("bucket sum %d %d", in, records)
	}
	r.Provider = strptr("")
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 1 || out.Totals.Input.Tokens != 5 {
		t.Fatalf("unknown: %v %v", out, e)
	}
	for _, d := range out.Breakdowns {
		if d.Totals.Records != 1 {
			t.Fatal("inconsistent filter", d)
		}
	}
	r.Provider = nil
	r.Model = strptr("fake")
	r.Source = strptr("main")
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 2 {
		t.Fatalf("filters %v %v", out, e)
	}
	r.Model = nil
	r.Source = nil
	r.Population = "external"
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 1 || out.Totals.Input.Tokens != 900 || out.Totals.CacheRead.KnownRecords != 0 {
		t.Fatalf("external: %v %v", out, e)
	}
	r.Population = "attempts"
	r.Preset = "custom"
	r.StartDate = "2026-09-01"
	r.EndDate = "2026-09-01"
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 0 || len(out.Buckets) != 1 {
		t.Fatalf("empty: %v %v", out, e)
	}
	// A half-open boundary excludes the second day, including reported zero output.
	r.Preset = "utc"
	r.StartUnixMicros = a.StartedAt.UnixMicro()
	r.EndUnixMicros = c.StartedAt.UnixMicro()
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 2 || out.Totals.Output.KnownRecords != 1 {
		t.Fatalf("bounds: %v %v", out, e)
	}
}
func TestMetricsCutoverHealthAndBounds(t *testing.T) {
	s := metricsStore(t)
	ctx := t.Context()
	now := time.Date(2030, 9, 15, 12, 0, 0, 0, time.UTC)
	h := AccountingHealth{Pending: 3, Lost: 2, Retries: 4, LastError: "old error"}
	if e := s.WriteAccountingHealth(ctx, "host", h); e != nil {
		t.Fatal(e)
	}
	live := AccountingHealth{Pending: 1, LastPersistence: now}
	out, e := s.QueryTokenMetrics(ctx, &proto.GetTokenMetricsRequest{Preset: "all", Timezone: "UTC"}, now, "host", &live)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Buckets) > 120 || out.Health.Pending != 1 || out.Health.Lost != 0 || out.Health.LastError != "" {
		t.Fatalf("%v", out)
	}
	if out.StartUnixMicros != time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro() {
		t.Fatal("cutover", out.StartUnixMicros)
	}
	if e := s.WriteAccountingHealth(ctx, "old-worker", h); e != nil {
		t.Fatal(e)
	}
	out, e = s.QueryTokenMetrics(ctx, &proto.GetTokenMetricsRequest{Preset: "today", Timezone: "UTC"}, now, "host", &live)
	if e != nil || !out.Health.CoverageIncomplete || out.Health.Lost != 2 {
		t.Fatalf("health: %v %v", out, e)
	}
	if !out.Buckets[0].Partial {
		t.Fatal("current day must be partial")
	}
	r := &proto.GetTokenMetricsRequest{Preset: "custom", Timezone: "UTC", StartDate: "2025-01-01", EndDate: "2025-01-02"}
	out, e = s.QueryTokenMetrics(ctx, r, now, "", nil)
	if e != nil || out.Totals.Records != 0 || len(out.Buckets) != 0 {
		t.Fatalf("before cutover: %v %v", out, e)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = s.QueryTokenMetrics(ctx, r, now, "", nil); e == nil {
		t.Fatal("canceled query succeeded")
	}
}
func TestMetricsIndexedQueriesAndBoundedBreakdowns(t *testing.T) {
	s := metricsStore(t)
	ctx := t.Context()
	for _, q := range []string{"SELECT COUNT(*) FROM inference_attempts WHERE started_at>=? AND started_at<?", "SELECT COUNT(*) FROM inference_attempts WHERE provider='fake' AND model='fake' AND started_at>=? AND started_at<?", "SELECT COUNT(*) FROM inference_attempts WHERE source='main' AND started_at>=? AND started_at<?"} {
		rows, e := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, 0, 2000000000000000)
		if e != nil {
			t.Fatal(e)
		}
		var detail string
		for rows.Next() {
			var id, parent, unused int
			var d string
			if e = rows.Scan(&id, &parent, &unused, &d); e != nil {
				t.Fatal(e)
			}
			detail += d
		}
		rows.Close()
		if !strings.Contains(detail, "INDEX") || !strings.Contains(detail, "SEARCH") {
			t.Fatal(detail)
		}
	}
	var batch []usage.AttemptObservation
	for i := 0; i < 70; i++ {
		a := accountingFixture(fmt.Sprint(i))
		a.Model = fmt.Sprint(i)
		batch = append(batch, a)
	}
	if e := s.WriteAttempts(ctx, batch); e != nil {
		t.Fatal(e)
	}
	out, e := s.QueryTokenMetrics(ctx, &proto.GetTokenMetricsRequest{Preset: "all", Timezone: "UTC"}, time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), "", nil)
	if e != nil || out.Totals.Records != 70 || !out.BreakdownsTruncated || len(out.Breakdowns) > 150 {
		t.Fatalf("bounded: %v %v", out, e)
	}
}
