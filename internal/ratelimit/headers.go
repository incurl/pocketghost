package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HeaderNames mirrors `POCKETHOST_RATE_LIMIT_HEADERS`.
const (
	HeaderIPHourlyLimit               = "X-PocketHost-RateLimit-Ip-Hourly-Limit"
	HeaderIPHourlyRemaining           = "X-PocketHost-RateLimit-Ip-Hourly-Remaining"
	HeaderIPHourlyReset               = "X-PocketHost-RateLimit-Ip-Hourly-Reset"
	HeaderInstanceHourlyLimit         = "X-PocketHost-RateLimit-Instance-Hourly-Limit"
	HeaderInstanceHourlyRemaining     = "X-PocketHost-RateLimit-Instance-Hourly-Remaining"
	HeaderInstanceHourlyReset         = "X-PocketHost-RateLimit-Instance-Hourly-Reset"
	HeaderIPConcurrentLimit           = "X-PocketHost-RateLimit-Ip-Concurrent-Limit"
	HeaderIPConcurrentRemaining       = "X-PocketHost-RateLimit-Ip-Concurrent-Remaining"
	HeaderInstanceConcurrentLimit     = "X-PocketHost-RateLimit-Instance-Concurrent-Limit"
	HeaderInstanceConcurrentRemaining = "X-PocketHost-RateLimit-Instance-Concurrent-Remaining"
)

// ExposeHeaders is the CORS `Access-Control-Expose-Headers` value the edge sends.
var ExposeHeaders = []string{
	HeaderIPHourlyLimit, HeaderIPHourlyRemaining, HeaderIPHourlyReset,
	HeaderInstanceHourlyLimit, HeaderInstanceHourlyRemaining, HeaderInstanceHourlyReset,
	HeaderIPConcurrentLimit, HeaderIPConcurrentRemaining,
	HeaderInstanceConcurrentLimit, HeaderInstanceConcurrentRemaining,
}

// ConnectingIP resolves the real client IP the way the legacy firewall did:
// Cloudflare / True-Client-IP first, then the first X-Forwarded-For hop, then
// X-Real-IP, then the socket address.
func ConnectingIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("True-Client-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if first, _, ok := strings.Cut(v, ","); ok {
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		} else if ip := strings.TrimSpace(v); ip != "" {
			return ip
		}
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return strings.TrimSpace(v)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// IsPocketBaseFilesPath reports whether a request is a file download (cheaper).
func IsPocketBaseFilesPath(path string) bool {
	return path == "/api/files" || strings.HasPrefix(path, "/api/files/")
}

// IsHealthProbePath exempts health/vacuum control paths from rate limiting.
func IsHealthProbePath(path string) bool {
	return path == "/api/firewall/health" ||
		path == "/_api/daemon/health" ||
		strings.HasPrefix(path, "/_api/daemon/vacuum")
}

// ConsumeWeightForPath mirrors `consumeWeightForPath`.
func ConsumeWeightForPath(path string) int {
	if IsPocketBaseFilesPath(path) {
		return FilesWeightNum
	}
	return APIWeightNum
}

// ResolveHourlyLimit mirrors `resolveHourlyLimit`: an operator override replaces
// the untrusted cap; trusted callers get at least the trusted default.
func ResolveHourlyLimit(override *int, untrustedDefault, trustedDefault int, trusted bool) int {
	if override == nil {
		if trusted {
			return trustedDefault
		}
		return untrustedDefault
	}
	if trusted {
		if *override > trustedDefault {
			return *override
		}
		return trustedDefault
	}
	return *override
}

// Overrides are the operator-only per-instance firewall settings.
type Overrides struct {
	IPHourly           *int
	InstanceHourly     *int
	IPConcurrent       *int
	InstanceConcurrent *int
}

// HeaderSet renders the rate-limit response headers.
func HeaderSet(h http.Header, ipHourly, instHourly Result, ipConc, instConc int) {
	setInt := func(k string, v int) { h.Set(k, strconv.Itoa(v)) }

	setInt(HeaderIPHourlyLimit, ipHourly.Limit)
	setInt(HeaderIPHourlyRemaining, ipHourly.Remaining)
	if !ipHourly.ResetUnixZero() {
		setInt(HeaderIPHourlyReset, int(ipHourly.ResetUnix))
	}

	setInt(HeaderInstanceHourlyLimit, instHourly.Limit)
	setInt(HeaderInstanceHourlyRemaining, instHourly.Remaining)
	if !instHourly.ResetUnixZero() {
		setInt(HeaderInstanceHourlyReset, int(instHourly.ResetUnix))
	}

	setInt(HeaderIPConcurrentLimit, ipConc)
	setInt(HeaderIPConcurrentRemaining, 0)
	setInt(HeaderInstanceConcurrentLimit, instConc)
	setInt(HeaderInstanceConcurrentRemaining, 0)
}

// SetConcurrencyRemaining fills the two concurrency "remaining" headers once the
// acquire step knows the real value.
func SetConcurrencyRemaining(h http.Header, ipRemaining, instRemaining int) {
	h.Set(HeaderIPConcurrentRemaining, strconv.Itoa(maxInt(ipRemaining, 0)))
	h.Set(HeaderInstanceConcurrentRemaining, strconv.Itoa(maxInt(instRemaining, 0)))
}

// ResetUnixZero reports whether the window reset timestamp is unset.
func (r Result) ResetUnixZero() bool { return r.ResetUnix == 0 }

// RetryAfterSeconds converts a retry duration to whole seconds (minimum 1).
func RetryAfterSeconds(d time.Duration) int {
	s := int(d / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
