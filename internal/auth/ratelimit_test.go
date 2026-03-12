package auth

import (
	"testing"
	"time"
)

func TestRateLimiterAllowWithinLimit(t *testing.T) {
	rl := NewRateLimiter(15*time.Minute, 5)

	for i := 0; i < 5; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		rl.RecordFailure("1.2.3.4")
	}
}

func TestRateLimiterBlockAfterLimit(t *testing.T) {
	rl := NewRateLimiter(15*time.Minute, 3)

	for i := 0; i < 3; i++ {
		rl.RecordFailure("1.2.3.4")
	}

	if rl.Allow("1.2.3.4") {
		t.Error("should be blocked after 3 failures")
	}
}

func TestRateLimiterDifferentIPs(t *testing.T) {
	rl := NewRateLimiter(15*time.Minute, 2)

	rl.RecordFailure("1.1.1.1")
	rl.RecordFailure("1.1.1.1")

	if rl.Allow("1.1.1.1") {
		t.Error("1.1.1.1 should be blocked")
	}
	if !rl.Allow("2.2.2.2") {
		t.Error("2.2.2.2 should be allowed")
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(15*time.Minute, 2)

	rl.RecordFailure("1.2.3.4")
	rl.RecordFailure("1.2.3.4")

	if rl.Allow("1.2.3.4") {
		t.Error("should be blocked before reset")
	}

	rl.Reset("1.2.3.4")

	if !rl.Allow("1.2.3.4") {
		t.Error("should be allowed after reset")
	}
}

func TestRateLimiterWindowExpiry(t *testing.T) {
	rl := NewRateLimiter(50*time.Millisecond, 2)

	rl.RecordFailure("1.2.3.4")
	rl.RecordFailure("1.2.3.4")

	if rl.Allow("1.2.3.4") {
		t.Error("should be blocked immediately")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Error("should be allowed after window expiry")
	}
}
