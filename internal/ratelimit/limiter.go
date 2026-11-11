// Package ratelimit is the Go replacement for `rate-limiter-flexible` and the
// `bottleneck` concurrency limiter. It keeps the original "micro point" budget
// model so the limits and response headers stay identical.
package ratelimit

import (
	"sync"
	"time"
)

// WeightDen is the micro-point denominator (1 API request = 10 micro points).
const WeightDen = 10

// FilesWeightNum is the cost of a PocketBase file request.
const FilesWeightNum = 1

// APIWeightNum is the cost of any other API request.
const APIWeightNum = WeightDen

// window is one fixed-window counter.
type window struct {
	used  int
	reset time.Time
}

// Limiter keeps rate-limit windows and concurrency counters in memory.
type Limiter struct {
	mu       sync.Mutex
	windows  map[string]*window
	conc     map[string]int
	now      func() time.Time
	lastGC   time.Time
	gcPeriod time.Duration
}

// NewLimiter creates a limiter. It opportunistically garbage-collects expired
// windows so memory stays flat on a 512 MB edge node.
func NewLimiter() *Limiter {
	return &Limiter{
		windows:  map[string]*window{},
		conc:     map[string]int{},
		now:      time.Now,
		gcPeriod: time.Minute,
	}
}

// SetClock overrides the clock (tests only).
func (l *Limiter) SetClock(now func() time.Time) { l.now = now }

// Result is the outcome of a Consume call.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	ResetUnix  int64
	RetryAfter time.Duration
}

// Consume deducts `points` from the `key` budget within `windowLen`.
// `limit` is expressed in API points; internally everything is micro points.
func (l *Limiter) Consume(key string, limit int, windowLen time.Duration, points int) Result {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.gcLocked(now)

	limitMicro := limit * WeightDen
	w := l.windows[key]
	if w == nil || !now.Before(w.reset) {
		w = &window{reset: now.Add(windowLen)}
		l.windows[key] = w
	}

	res := Result{
		Limit:     limit,
		Remaining: microToAPI(limitMicro - w.used),
		ResetUnix: w.reset.Unix(),
	}

	if w.used+points > limitMicro {
		res.Allowed = false
		res.Remaining = microToAPI(limitMicro - w.used)
		if res.Remaining < 0 {
			res.Remaining = 0
		}
		res.RetryAfter = w.reset.Sub(now)
		if res.RetryAfter < 0 {
			res.RetryAfter = 0
		}
		return res
	}

	w.used += points
	res.Allowed = true
	res.Remaining = microToAPI(limitMicro - w.used)
	if res.Remaining < 0 {
		res.Remaining = 0
	}
	return res
}

// AcquireConcurrent increments a concurrent counter. The returned release
// function must be called exactly once when the request finishes.
func (l *Limiter) AcquireConcurrent(key string, limit int) (release func(), remaining int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cur := l.conc[key]
	if cur >= limit {
		return func() {}, 0, false
	}
	l.conc[key] = cur + 1
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		released = true
		l.conc[key]--
		if l.conc[key] <= 0 {
			delete(l.conc, key)
		}
	}, limit - cur - 1, true
}

// Concurrent returns the live concurrency count for a key.
func (l *Limiter) Concurrent(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conc[key]
}

func (l *Limiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < l.gcPeriod {
		return
	}
	l.lastGC = now
	for k, w := range l.windows {
		if now.After(w.reset) {
			delete(l.windows, k)
		}
	}
}

func microToAPI(micro int) int {
	if micro <= 0 {
		return 0
	}
	return micro / WeightDen
}
