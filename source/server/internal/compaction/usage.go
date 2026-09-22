package compaction

import (
	"context"
	"sync"
)

// SummaryUsage measures token volume, not currency. Input includes cache reads
// if the provider includes them. Estimates remain distinguishable from reports.
type SummaryUsage struct {
	ReportedInput   int
	ReportedOutput  int
	EstimatedInput  int
	EstimatedOutput int
	Calls           int
	Observations    int // accounting reports, including explicit zero-attempt reports
}

func (u SummaryUsage) Reported() int  { return u.ReportedInput + u.ReportedOutput }
func (u SummaryUsage) Estimated() int { return u.EstimatedInput + u.EstimatedOutput }
func (u SummaryUsage) Total() int     { return u.Reported() + u.Estimated() }

type UsageMeter struct {
	mu    sync.Mutex
	usage SummaryUsage
}
type usageKey struct{}

func WithUsageMeter(ctx context.Context) (context.Context, *UsageMeter) {
	m := &UsageMeter{}
	return context.WithValue(ctx, usageKey{}, m), m
}
func (m *UsageMeter) Snapshot() SummaryUsage { m.mu.Lock(); defer m.mu.Unlock(); return m.usage }
func RecordSummaryUsage(ctx context.Context, u SummaryUsage) {
	m, _ := ctx.Value(usageKey{}).(*UsageMeter)
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage.ReportedInput += max(0, u.ReportedInput)
	m.usage.ReportedOutput += max(0, u.ReportedOutput)
	m.usage.EstimatedInput += max(0, u.EstimatedInput)
	m.usage.EstimatedOutput += max(0, u.EstimatedOutput)
	m.usage.Calls += max(0, u.Calls)
	m.usage.Observations++
}
