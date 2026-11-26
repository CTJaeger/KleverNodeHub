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

	// global counts failures from all IPs, against guessing spread over many
	// addresses. 0 disables it.
	global    []time.Time
	globalMax int
}

// maxTrackedIPs bounds the attempts map so random source IPs can't grow it.
const maxTrackedIPs = 10000

// SetGlobalLimit sets how many failures from all IPs together are allowed per
// window. Passkey and wallet logins don't use the limiter, so the owner keeps
// those even while password login is blocked.
func (rl *RateLimiter) SetGlobalLimit(n int) {
	rl.mu.Lock()
	rl.globalMax = n
	rl.mu.Unlock()
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
	rl.pruneGlobal()
	if rl.globalMax > 0 && len(rl.global) >= rl.globalMax {
		return false
	}
	return len(rl.attempts[ip]) < rl.maxFails
}

// AllowIP checks only the per-IP limit, for the recovery login: the owner's
// last resort must not be blockable by failures spread over other IPs.
func (rl *RateLimiter) AllowIP(ip string) bool {
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
	if _, known := rl.attempts[ip]; !known && len(rl.attempts) >= maxTrackedIPs {
		for k := range rl.attempts {
			rl.pruneExpired(k)
		}
		if len(rl.attempts) >= maxTrackedIPs {
			rl.attempts = make(map[string][]time.Time) // the global limit still applies
		}
	}
	now := time.Now()
	rl.attempts[ip] = append(rl.attempts[ip], now)
	if rl.globalMax > 0 {
		rl.global = append(rl.global, now)
	}
}

// pruneGlobal drops global failures older than the window. Lock must be held.
func (rl *RateLimiter) pruneGlobal() {
	cutoff := time.Now().Add(-rl.window)
	i := 0
	for i < len(rl.global) && !rl.global[i].After(cutoff) {
		i++
	}
	rl.global = rl.global[i:]
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
