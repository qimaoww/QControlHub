package authn

import (
	"sync"
	"time"
)

type failureRecord struct {
	Count        int
	WindowStart  time.Time
	BlockedUntil time.Time
}

type FailureLimiter struct {
	mu       sync.Mutex
	records  map[string]failureRecord
	limit    int
	window   time.Duration
	blockFor time.Duration
}

func NewFailureLimiter(limit int, window, blockFor time.Duration) *FailureLimiter {
	return &FailureLimiter{
		records: make(map[string]failureRecord),
		limit:   limit, window: window, blockFor: blockFor,
	}
}

func (l *FailureLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	record, ok := l.records[key]
	if !ok {
		return true
	}
	if !record.BlockedUntil.IsZero() && now.Before(record.BlockedUntil) {
		return false
	}
	if now.Sub(record.WindowStart) > l.window {
		delete(l.records, key)
	}
	return true
}

func (l *FailureLimiter) Failure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	record, ok := l.records[key]
	if !ok || now.Sub(record.WindowStart) > l.window {
		record = failureRecord{WindowStart: now}
	}
	record.Count++
	if record.Count >= l.limit {
		record.BlockedUntil = now.Add(l.blockFor)
	}
	l.records[key] = record
	if len(l.records) > 10_000 {
		l.pruneLocked(now)
	}
}

func (l *FailureLimiter) Success(key string) {
	l.mu.Lock()
	delete(l.records, key)
	l.mu.Unlock()
}

func (l *FailureLimiter) pruneLocked(now time.Time) {
	for key, record := range l.records {
		if now.After(record.BlockedUntil) && now.Sub(record.WindowStart) > l.window {
			delete(l.records, key)
		}
	}
}
