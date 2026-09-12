package main

import (
	"sync"

	"cercano/source/server/internal/telemetry"
)

// startAgentTelemetry transfers ownership to the returned shutdown function.
// The caller must stop usage producers before invoking shutdown.
func startAgentTelemetry(path, session string) (*telemetry.Collector, func(), error) {
	store, err := telemetry.NewSQLiteStore(path)
	if err != nil {
		return nil, nil, err
	}
	collector := telemetry.NewCollector(store, 256)
	collector.SetSessionID(session)
	if err := collector.EnableAccounting(telemetry.AccountingOptions{}); err != nil {
		collector.Close()
		_ = store.Close()
		return nil, nil, err
	}
	var once sync.Once
	return collector, func() { once.Do(func() { collector.Close(); _ = store.Close() }) }, nil
}
