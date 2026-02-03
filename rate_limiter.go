package main

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type visitor struct {
	count       int
	windowStart time.Time
}

type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     int
	window   time.Duration
	stopChan chan struct{}
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		stopChan: make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopChan:
			return
		}
	}
}

func (rl *RateLimiter) cleanup() {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for key, v := range rl.visitors {
		if now.Sub(v.windowStart) >= rl.window {
			delete(rl.visitors, key)
		}
	}
}

// Allow checks whether a request identified by key is within the rate limit.
// Returns whether the request is allowed, the current count, and seconds until window reset.
func (rl *RateLimiter) Allow(key string) (bool, int, int) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists || now.Sub(v.windowStart) >= rl.window {
		rl.visitors[key] = &visitor{count: 1, windowStart: now}
		resetSeconds := int(math.Ceil(rl.window.Seconds()))
		return true, 1, resetSeconds
	}

	v.count++
	resetSeconds := int(math.Ceil(rl.window.Seconds() - now.Sub(v.windowStart).Seconds()))
	if resetSeconds < 1 {
		resetSeconds = 1
	}
	return v.count <= rl.rate, v.count, resetSeconds
}

func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopChan:
		// already closed
	default:
		close(rl.stopChan)
	}
}

func setRateLimitHeaders(c *gin.Context, limit, remaining, resetSeconds int) {
	c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
	rem := remaining
	if rem < 0 {
		rem = 0
	}
	c.Header("X-RateLimit-Remaining", strconv.Itoa(rem))
	c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Duration(resetSeconds)*time.Second).Unix(), 10))
}

// IPRateLimitMiddleware creates a Gin middleware that rate-limits by client IP.
func IPRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()
		allowed, count, resetSeconds := limiter.Allow(key)
		remaining := limiter.rate - count
		setRateLimitHeaders(c, limiter.rate, remaining, resetSeconds)

		if !allowed {
			c.Header("Retry-After", strconv.Itoa(resetSeconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"retry_after": resetSeconds,
			})
			return
		}
		c.Next()
	}
}

// AccountRateLimitMiddleware creates a Gin middleware that rate-limits by account ID
// (set by authMiddleware), falling back to client IP if not available.
func AccountRateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()
		if accountID, exists := c.Get("account_id"); exists {
			if id, ok := accountID.(string); ok && id != "" {
				key = "account:" + id
			}
		}

		allowed, count, resetSeconds := limiter.Allow(key)
		remaining := limiter.rate - count
		setRateLimitHeaders(c, limiter.rate, remaining, resetSeconds)

		if !allowed {
			c.Header("Retry-After", strconv.Itoa(resetSeconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"retry_after": resetSeconds,
			})
			return
		}
		c.Next()
	}
}
