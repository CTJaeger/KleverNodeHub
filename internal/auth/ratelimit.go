package auth

import (
	"sync"
	"time"
)

// RateLimiter tracks failed login attempts per IP with a sliding window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	window   time.Duration
	maxFails int
}

// NewRateLimiter creates a rate limiter with the given window and max failures.
func NewRateLimiter(window time.Duration, maxFails int) *RateLimiter {
	return &RateLimiter{
		attempts: make(map[string][]time.Time),
		window:   window,
		maxFails: maxFails,
	}
}

// Allow checks if an IP is allowed to attempt a login.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.pruneExpired(ip)
	return len(rl.attempts[ip]) < rl.maxFails
}

// RecordFailure records a failed login attempt for the given IP.
func (rl *RateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.pruneExpired(ip)
	rl.attempts[ip] = append(rl.attempts[ip], time.Now())
}

// Reset clears the failure count for an IP after a successful login.
func (rl *RateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.attempts, ip)
}

// pruneExpired removes attempts older than the window. Must be called with lock held.
func (rl *RateLimiter) pruneExpired(ip string) {
	attempts := rl.attempts[ip]
	if len(attempts) == 0 {
		return
	}

	cutoff := time.Now().Add(-rl.window)
	valid := 0
	for _, t := range attempts {
		if t.After(cutoff) {
			attempts[valid] = t
			valid++
		}
	}

	if valid == 0 {
		delete(rl.attempts, ip)
	} else {
		rl.attempts[ip] = attempts[:valid]
	}
}
