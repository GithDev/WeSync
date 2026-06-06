package store

import (
	"time"
)

// power_events table operations. Append-only, ring-buffered to ~maxEvents
// rows so a multi-month-old install doesn't accumulate megabytes of log
// entries.

const maxEvents = 200

// AppendPowerEvent records a single transition. tsUnixMs lets callers
// pass an injected clock for tests; pass 0 to use time.Now().
func (s *Store) AppendPowerEvent(kind, message string, tsUnixMs int64) error {
	if tsUnixMs == 0 {
		tsUnixMs = time.Now().UnixMilli()
	}
	e := PowerEvent{
		Timestamp: tsUnixMs,
		Kind:      kind,
		Message:   message,
	}
	if err := s.db.Create(&e).Error; err != nil {
		return err
	}
	// Trim the oldest rows beyond maxEvents. SQLite supports
	// DELETE ... WHERE id IN (subquery), which is what GORM emits.
	s.db.Exec(
		`DELETE FROM power_events WHERE id IN (SELECT id FROM power_events ORDER BY timestamp DESC LIMIT -1 OFFSET ?)`,
		maxEvents,
	)
	return nil
}

// ListPowerEvents returns the N most recent events in newest-first order.
// Iso-formatted timestamps are populated on the way out — the table
// stores raw unix ms, but the API contract is RFC3339.
func (s *Store) ListPowerEvents(limit int) ([]PowerEvent, error) {
	if limit <= 0 || limit > maxEvents {
		limit = maxEvents
	}
	var events []PowerEvent
	if err := s.db.Order("timestamp DESC").Limit(limit).Find(&events).Error; err != nil {
		return nil, err
	}
	for i := range events {
		events[i].TimeISO = time.UnixMilli(events[i].Timestamp).UTC().Format(time.RFC3339)
	}
	return events, nil
}
