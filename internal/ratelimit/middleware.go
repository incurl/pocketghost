package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
)

// HourlyDuration is the rate-limit window length.
const HourlyDuration = time.Hour

// Defaults mirrors the `LIMITS` table in the legacy firewall.
var Defaults = struct {
	UntrustedIPHourly       int
	UntrustedHostHourly     int
	TrustedIPHourly         int
	TrustedHostHourly       int
	UntrustedIPConcurrent   int
	TrustedIPConcurrent     int
	UntrustedHostConcurrent int
	TrustedHostConcurrent   int
}{
	UntrustedIPHourly:       1000,
	UntrustedHostHourly:     10000,
	TrustedIPHourly:         5000,
	TrustedHostHourly:       20000,
	UntrustedIPConcurrent:   15,
	TrustedIPConcurrent:     50,
	UntrustedHostConcurrent: 250,
	TrustedHostConcurrent:   250,
}

// TrustedIPClientHeader lets a trusted proxy assert the real end-client IP.
const TrustedIPClientHeader = "X-PocketHost-Client-IP"

// Resolver supplies the control-plane facts the limiter needs.
type Resolver interface {
	// IsTrustedIP reports whether the connecting IP belongs to a trusted account.
	IsTrustedIP(ip, host string) bool
	// InstanceFirewall returns the operator overrides for a host (nil if none).
	InstanceFirewall(host string) *Overrides
}

// Middleware enforces hourly and concurrent limits per IP and per instance.
type Middleware struct {
	Limiter  *Limiter
	Resolver Resolver
	Logger   *logging.Logger
	Disabled bool
}

// NewMiddleware builds the firewall rate limiter.
func NewMiddleware(l *Limiter, r Resolver, logger *logging.Logger, disabled bool) *Middleware {
	return &Middleware{Limiter: l, Resolver: r, Logger: logger, Disabled: disabled}
}

// Wrap returns the middleware handler.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Disabled || IsHealthProbePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		host := Hostname(r)
		connectingIP := ConnectingIP(r)
		trusted := false
		var overrides *Overrides
		if m.Resolver != nil {
			trusted = m.Resolver.IsTrustedIP(connectingIP, host)
			overrides = m.Resolver.InstanceFirewall(host)
		}

		// Cloudflare's image resizing proxy fans one page load into many
		// requests; treat it as trusted so a gallery page cannot self-throttle.
		if strings.Contains(strings.ToLower(r.Header.Get("Via")), "image-resizing-proxy") &&
			strings.Contains(strings.ToLower(r.Header.Get("CDN-Loop")), "cloudflare") {
			trusted = true
		}

		clientIP := connectingIP
		if trusted {
			if custom := r.Header.Get(TrustedIPClientHeader); custom != "" {
				clientIP = strings.TrimSpace(custom)
			}
		}

		ipHourlyLimit := Defaults.UntrustedIPHourly
		hostHourlyLimit := Defaults.UntrustedHostHourly
		ipConcLimit := Defaults.UntrustedIPConcurrent
		hostConcLimit := Defaults.UntrustedHostConcurrent
		if trusted {
			ipHourlyLimit = Defaults.TrustedIPHourly
			hostHourlyLimit = Defaults.TrustedHostHourly
			ipConcLimit = Defaults.TrustedIPConcurrent
			hostConcLimit = Defaults.TrustedHostConcurrent
		}
		if overrides != nil {
			ipHourlyLimit = ResolveHourlyLimit(overrides.IPHourly, Defaults.UntrustedIPHourly, Defaults.TrustedIPHourly, trusted)
			hostHourlyLimit = ResolveHourlyLimit(overrides.InstanceHourly, Defaults.UntrustedHostHourly, Defaults.TrustedHostHourly, trusted)
			if overrides.IPConcurrent != nil {
				ipConcLimit = *overrides.IPConcurrent
			}
			if overrides.InstanceConcurrent != nil {
				hostConcLimit = *overrides.InstanceConcurrent
			}
		}

		weight := ConsumeWeightForPath(r.URL.Path)

		ipRes := m.Limiter.Consume("ip:"+clientIP, ipHourlyLimit, HourlyDuration, weight)
		hostRes := m.Limiter.Consume("host:"+host, hostHourlyLimit, HourlyDuration, weight)

		HeaderSet(w.Header(), ipRes, hostRes, ipConcLimit, hostConcLimit)

		if !ipRes.Allowed || !hostRes.Allowed {
			retry := ipRes.RetryAfter
			if hostRes.RetryAfter > retry {
				retry = hostRes.RetryAfter
			}
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(retry)))
			writeLimitError(w, "IP "+clientIP+" on instance "+host, ipRes, hostRes)
			if m.Logger != nil {
				m.Logger.Debugf("rate limited %s %s on %s (ip=%s trusted=%v)", r.Method, r.URL.Path, host, clientIP, trusted)
			}
			return
		}

		ipRelease, ipRemaining, ipOK := m.Limiter.AcquireConcurrent("ipc:"+clientIP, ipConcLimit)
		if !ipOK {
			w.Header().Set("Retry-After", "1")
			writeConcurrencyError(w, "IP "+clientIP, ipConcLimit)
			return
		}
		defer ipRelease()

		hostRelease, hostRemaining, hostOK := m.Limiter.AcquireConcurrent("hostc:"+host, hostConcLimit)
		if !hostOK {
			w.Header().Set("Retry-After", "1")
			writeConcurrencyError(w, "instance "+host, hostConcLimit)
			return
		}
		defer hostRelease()

		SetConcurrencyRemaining(w.Header(), ipRemaining, hostRemaining)
		w.Header().Set("Access-Control-Expose-Headers", strings.Join(ExposeHeaders, ", "))

		next.ServeHTTP(w, r)
	})
}

// Hostname extracts the effective request host (X-Forwarded-Host aware).
func Hostname(r *http.Request) string {
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			host = strings.TrimSpace(first)
		} else {
			host = strings.TrimSpace(fwd)
		}
	}
	if h, _, ok := strings.Cut(host, ":"); ok && !strings.Contains(host, "]") {
		host = h
	}
	return strings.ToLower(host)
}

func writeLimitError(w http.ResponseWriter, who string, ipRes, hostRes Result) {
	limit := ipRes.Limit
	if hostRes.Limit < limit {
		limit = hostRes.Limit
	}
	msg := fmt.Sprintf("Too many requests. %s is limited to %d requests/hour.", who, limit)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"code":429,"message":"` + msg + `","data":{}}`))
}

func writeConcurrencyError(w http.ResponseWriter, who string, limit int) {
	msg := fmt.Sprintf("Too many concurrent requests. %s is limited to %d concurrent requests.", who, limit)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"code":429,"message":"` + msg + `","data":{}}`))
}
