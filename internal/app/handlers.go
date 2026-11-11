package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/proxy"
)

// DaemonHandler is the data plane listener: PocketBase instance hosts are
// reverse-proxied to their native processes, everything else is served by the
// control-plane API.
func (a *App) DaemonHandler() http.Handler {
	apiHandler := a.API.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.isInstanceRequest(r) {
			a.Proxy.ServeHTTP(w, r)
			return
		}
		apiHandler.ServeHTTP(w, r)
	})
}

// isInstanceRequest reports whether the request host belongs to a tenant.
func (a *App) isInstanceRequest(r *http.Request) bool {
	host := hostOnly(r.Host)
	if host == "" {
		return false
	}
	// Control-plane hosts are never proxied.
	switch host {
	case a.Config.ApexDomain,
		a.Config.MothershipNm + "." + a.Config.ApexDomain,
		"app." + a.Config.ApexDomain,
		"localhost", "127.0.0.1":
		return false
	}
	if _, err := a.Repo.GetInstanceByHost(r.Context(), host, a.Config.ApexDomain); err == nil {
		return true
	}
	return false
}

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}

// EdgeHandler is the public firewall listener: CORS, rate limiting, then
// forwarding to the local daemon (or directly to instances when co-located).
func (a *App) EdgeHandler() http.Handler {
	limiter := a.RateLimiter()

	// In single-binary deployments the firewall and daemon are the same process,
	// so requests are served by the daemon handler directly. When DAEMON_PORT is
	// remote (multi-node), requests are forwarded over HTTP instead.
	var core http.Handler
	if a.Config.EdgeInternalURL != "" && isRemoteDaemon(a.Config.EdgeInternalURL) {
		core = newDaemonForwarder(a.Config.EdgeInternalURL, a.Logger)
	} else {
		core = a.DaemonHandler()
	}

	return limiter.Wrap(proxy.CORS(core))
}

func isRemoteDaemon(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host != "" && host != "127.0.0.1" && host != "localhost"
}

// newDaemonForwarder proxies edge traffic to a remote daemon.
func newDaemonForwarder(raw string, _ *logging.Logger) http.Handler {
	target, err := url.Parse(raw)
	if err != nil {
		return http.NotFoundHandler()
	}
	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
		},
	}
	return rp
}

// GraceRetry holds a request while the local daemon restarts, mirroring
// `createDaemonGraceMiddleware`. It returns true when the request was handled.
func (a *App) GraceRetry(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	deadline := time.Now().Add(a.Config.FirewallGrace)
	for {
		healthy, err := a.daemonHealthy(r.Context())
		if err == nil && healthy {
			return false
		}
		if time.Now().After(deadline) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":503,"message":"Edge is restarting. Please retry.","data":{}}`))
			return true
		}
		select {
		case <-r.Context().Done():
			return true
		case <-time.After(a.Config.FirewallGraceRetry):
		}
	}
}

func (a *App) daemonHealthy(ctx context.Context) (bool, error) {
	if a.Instances != nil && a.Instances.Ready() {
		return true, nil
	}
	return false, errors.New("daemon not ready")
}

var _ = fmt.Sprintf
