package authn

import (
	"testing"
	"time"
)

func TestFailureLimiterBlocksAtLimitAndSuccessClears(t *testing.T) {
	t.Parallel()
	const key = "192.0.2.1"
	base := time.Unix(1_750_000_000, 0)
	limiter := NewFailureLimiter(3, time.Minute, 2*time.Minute)

	if !limiter.Allow(key, base) {
		t.Fatal("new key should be allowed")
	}
	limiter.Failure(key, base)
	limiter.Failure(key, base.Add(10*time.Second))
	if !limiter.Allow(key, base.Add(15*time.Second)) {
		t.Fatal("key blocked before reaching failure limit")
	}
	thirdFailure := base.Add(20 * time.Second)
	limiter.Failure(key, thirdFailure)
	if limiter.Allow(key, thirdFailure) {
		t.Fatal("key remained allowed after reaching failure limit")
	}
	if !limiter.Allow("198.51.100.2", thirdFailure) {
		t.Fatal("one key's failures affected a different key")
	}
	if limiter.Allow(key, thirdFailure.Add(2*time.Minute-time.Nanosecond)) {
		t.Fatal("key was unblocked before BlockedUntil")
	}
	if !limiter.Allow(key, thirdFailure.Add(2*time.Minute)) {
		t.Fatal("key should be allowed exactly at BlockedUntil")
	}

	limiter.Success(key)
	if !limiter.Allow(key, thirdFailure.Add(time.Second)) {
		t.Fatal("successful authentication did not clear failure state")
	}
}

func TestFailureLimiterResetsExpiredWindow(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_750_000_000, 0)
	window := time.Minute
	limiter := NewFailureLimiter(3, window, time.Minute)

	limiter.Failure("client", base)
	resetAt := base.Add(window + time.Nanosecond)
	limiter.Failure("client", resetAt)
	limiter.Failure("client", resetAt)
	if !limiter.Allow("client", resetAt) {
		t.Fatal("failure count from an expired window was retained")
	}
	limiter.Failure("client", resetAt)
	if limiter.Allow("client", resetAt) {
		t.Fatal("new window did not block at the configured limit")
	}
}
