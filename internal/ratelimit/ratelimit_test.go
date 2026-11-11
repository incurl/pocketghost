package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConsumeHourlyBudget(t *testing.T) {
	l := NewLimiter()
	now := time.Unix(1_700_000_000, 0)
	l.SetClock(func() time.Time { return now })

	// 10 API requests per window => 100 micro points.
	for i := 0; i < 10; i++ {
		res := l.Consume("ip:1.2.3.4", 10, time.Hour, APIWeightNum)
		if !res.Allowed {
			t.Fatalf("request %d unexpectedly denied", i)
		}
	}
	res := l.Consume("ip:1.2.3.4", 10, time.Hour, APIWeightNum)
	if res.Allowed {
		t.Fatal("expected denial after budget exhausted")
	}
	if res.RetryAfter <= 0 {
		t.Fatalf("expected retry-after, got %v", res.RetryAfter)
	}

	// File downloads cost 1/10 of an API request.
	now = now.Add(time.Hour)
	res = l.Consume("ip:1.2.3.4", 10, time.Hour, FilesWeightNum)
	if !res.Allowed {
		t.Fatal("expected window reset")
	}
}

func TestConcurrency(t *testing.T) {
	l := NewLimiter()
	release1, remaining, ok := l.AcquireConcurrent("k", 2)
	if !ok || remaining != 1 {
		t.Fatalf("first acquire ok=%v remaining=%d", ok, remaining)
	}
	release2, _, ok := l.AcquireConcurrent("k", 2)
	if !ok {
		t.Fatal("second acquire should succeed")
	}
	if _, _, ok := l.AcquireConcurrent("k", 2); ok {
		t.Fatal("third acquire should fail")
	}
	release1()
	release1() // idempotent
	release2()
	if _, remaining, ok := l.AcquireConcurrent("k", 2); !ok || remaining != 1 {
		t.Fatalf("after release ok=%v remaining=%d", ok, remaining)
	}
}

func TestConnectingIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := ConnectingIP(r); got != "10.0.0.1" {
		t.Fatalf("remote addr = %q", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := ConnectingIP(r); got != "203.0.113.9" {
		t.Fatalf("xff = %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	if got := ConnectingIP(r); got != "198.51.100.7" {
		t.Fatalf("cf = %q", got)
	}
}

func TestResolveHourlyLimit(t *testing.T) {
	override := 5000
	if got := ResolveHourlyLimit(nil, 1000, 5000, false); got != 1000 {
		t.Fatalf("untrusted default = %d", got)
	}
	if got := ResolveHourlyLimit(nil, 1000, 5000, true); got != 5000 {
		t.Fatalf("trusted default = %d", got)
	}
	if got := ResolveHourlyLimit(&override, 1000, 5000, false); got != 5000 {
		t.Fatalf("override = %d", got)
	}
	low := 100
	if got := ResolveHourlyLimit(&low, 1000, 5000, true); got != 5000 {
		t.Fatalf("trusted override floor = %d", got)
	}
}

func TestMiddlewareSetsHeadersAndBlocks(t *testing.T) {
	l := NewLimiter()
	mw := NewMiddleware(l, nil, nil, false)

	allowed := 0
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < Defaults.UntrustedIPHourly; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/collections/instances/records", nil)
		req.Host = "demo.example.com"
		req.RemoteAddr = "203.0.113.1:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
		if rec.Header().Get(HeaderIPHourlyLimit) == "" {
			t.Fatal("missing rate limit headers")
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/collections/instances/records", nil)
	req.Host = "demo.example.com"
	req.RemoteAddr = "203.0.113.1:5555"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if allowed != Defaults.UntrustedIPHourly {
		t.Fatalf("handler ran %d times", allowed)
	}
}

func TestHealthPathsAreExempt(t *testing.T) {
	mw := NewMiddleware(NewLimiter(), nil, nil, false)
	called := false
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/_api/daemon/health", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("health path should bypass the limiter")
	}
}
