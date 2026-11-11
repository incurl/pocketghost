// Package proxy is the Go replacement for `httpxy` / `http-proxy-middleware`.
// It implements host-based dynamic routing to tenant PocketBase processes with
// full WebSocket and SSE passthrough.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/metrics"
	"github.com/incurl/pocketghost/internal/ratelimit"
)

// ErrNotReady is returned while the data plane is still warming up.
var ErrNotReady = errors.New("proxy: instance traffic not ready")

// ErrNoTarget is returned when no instance matches the request host.
var ErrNoTarget = errors.New("proxy: no target for host")

// Resolver maps a request host to a loopback target URL.
type Resolver interface {
	// Resolve returns the base URL (for example http://127.0.0.1:31001) for host.
	Resolve(ctx context.Context, host string) (string, error)
}

// Options configures the edge handler.
type Options struct {
	Resolver Resolver
	Logger   *logging.Logger
	Stats    *metrics.Stats

	// RequestTimeout bounds non-streaming requests. Zero means no limit.
	RequestTimeout time.Duration
	// DialTimeout bounds the upstream TCP connect.
	DialTimeout time.Duration
	// MaxIdleConns tunes the shared transport.
	MaxIdleConns int
}

// Handler is the data-plane HTTP handler.
type Handler struct {
	opts   Options
	proxy  *httputil.ReverseProxy
	ready  atomic.Bool
	logger *logging.Logger
	stats  *metrics.Stats
}

type targetKey struct{}

// SetReady toggles the data plane readiness flag. While false, connections are
// dropped so an upstream firewall treats the request as "daemon restarting"
// instead of returning a 404.
func (h *Handler) SetReady(ready bool) { h.ready.Store(ready) }

// Ready reports the readiness flag.
func (h *Handler) Ready() bool { return h.ready.Load() }

// Stats exposes the traffic counters.
func (h *Handler) Stats() *metrics.Stats { return h.stats }

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    status,
		"message": msg,
		"data":    map[string]any{},
	})
}

func protoOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return p
	}
	return "http"
}

// StaticResolver is a fixed host->target map, used by tests.
type StaticResolver map[string]string

// Resolve implements Resolver.
func (s StaticResolver) Resolve(_ context.Context, host string) (string, error) {
	if t, ok := s[strings.ToLower(host)]; ok {
		return t, nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoTarget, host)
}

// New builds the edge handler.
func New(opts Options) *Handler {
	if opts.Logger == nil {
		opts.Logger = logging.Discard()
	}
	if opts.Stats == nil {
		opts.Stats = metrics.New()
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 5 * time.Second
	}
	if opts.MaxIdleConns <= 0 {
		opts.MaxIdleConns = 512
	}

	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout:   opts.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          opts.MaxIdleConns,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		// PocketBase realtime uses SSE; disable compression so upstream chunking
		// is forwarded verbatim.
		DisableCompression: true,
	}

	h := &Handler{opts: opts, logger: opts.Logger.Create("proxy"), stats: opts.Stats}

	h.proxy = &httputil.ReverseProxy{
		Transport: transport,
		// -1 flushes immediately, which is required for SSE and realtime.
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			target, _ := pr.In.Context().Value(targetKey{}).(*url.URL)
			if target == nil {
				return
			}
			pr.SetURL(target)
			// Preserve the original Host: PocketBase builds absolute URLs
			// (verification links, OAuth redirects) from this header.
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			pr.Out.Header.Set("X-Forwarded-Proto", protoOf(pr.In))
			pr.Out.Header.Set("X-PocketHost-Proxy", "edge")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.stats.AddError()
			target, _ := r.Context().Value(targetKey{}).(*url.URL)
			tgt := "?"
			if target != nil {
				tgt = target.String()
			}
			h.logger.Warnf("proxy error for %s%s -> %s: %v", r.Host, r.URL.Path, tgt, err)
			writeJSONError(w, http.StatusBadGateway, "Instance unavailable")
		},
		ModifyResponse: func(res *http.Response) error {
			rewriteCORS(res)
			return nil
		},
	}
	h.ready.Store(true)
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_api/daemon/health", "/api/firewall/health":
		h.handleHealth(w)
		return
	}

	if !h.ready.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}

	host := ratelimit.Hostname(r)
	h.stats.AddRequest()
	h.stats.AddHost(host)
	h.stats.AddIP(ratelimit.ConnectingIP(r))
	h.stats.AddCountry(r.Header.Get("CF-IPCountry"))

	targetStr, err := h.opts.Resolver.Resolve(r.Context(), host)
	if err != nil {
		if errors.Is(err, ErrNoTarget) {
			writeJSONError(w, http.StatusNotFound, "Instance not found")
			return
		}
		status := http.StatusServiceUnavailable
		if strings.Contains(err.Error(), "402") {
			status = http.StatusPaymentRequired
		}
		writeJSONError(w, status, err.Error())
		return
	}

	u, err := url.Parse(targetStr)
	if err != nil {
		h.stats.AddError()
		writeJSONError(w, http.StatusBadGateway, "Invalid instance target")
		return
	}

	r = r.WithContext(context.WithValue(r.Context(), targetKey{}, u))
	h.proxy.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"starting"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// rewriteCORS widens the upstream CORS headers the way
// `rewriteProxiedCorsHeaders` did: PocketBase answers `Access-Control-Allow-Origin:
// *`, which browsers reject for credentialed requests.
func rewriteCORS(res *http.Response) {
	origin := res.Request.Header.Get("Origin")
	if origin == "" {
		return
	}
	res.Header.Set("Access-Control-Allow-Origin", origin)
	res.Header.Set("Access-Control-Allow-Credentials", "true")
	res.Header.Add("Vary", "Origin")

	expose := res.Header.Get("Access-Control-Expose-Headers")
	extra := strings.Join(ratelimit.ExposeHeaders, ", ")
	if expose == "" {
		res.Header.Set("Access-Control-Expose-Headers", extra)
	} else if !strings.Contains(expose, ratelimit.HeaderIPHourlyLimit) {
		res.Header.Set("Access-Control-Expose-Headers", expose+", "+extra)
	}
	res.Header.Add("Vary", "Access-Control-Request-Headers")
}

// CORS is a permissive preflight handler mirroring `corsMiddleware`.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			reqHeaders := r.Header.Get("Access-Control-Request-Headers")
			if reqHeaders == "" {
				reqHeaders = "Authorization, Content-Type, X-Requested-With"
			}
			w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(ratelimit.ExposeHeaders, ", "))
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
