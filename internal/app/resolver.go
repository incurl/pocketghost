package app

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/ratelimit"
	"github.com/incurl/pocketghost/internal/store"
)

// limiterResolver answers the firewall's trust and quota questions from the
// metadata database (the mirror, in the original architecture).
type limiterResolver struct {
	repo *store.Repo
	cfg  *config.Config
}

// IsTrustedIP reports whether the connecting IP belongs to an account that has
// listed it as trusted (or matches PH_USER_PROXY_IPS).
func (r *limiterResolver) IsTrustedIP(ip, host string) bool {
	if ip == "" {
		return false
	}
	for _, allowed := range r.cfg.UserProxyIPs {
		if allowed == ip {
			return true
		}
	}
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	inst, err := r.repo.GetInstanceByHost(ctx, host, r.cfg.ApexDomain)
	if err != nil {
		return false
	}
	trusted, err := r.repo.TrustedIPs(ctx, inst.UID)
	if err != nil {
		return false
	}
	for _, entry := range trusted {
		if entry == ip {
			return true
		}
		if strings.Contains(entry, "/") {
			if _, cidr, err := net.ParseCIDR(entry); err == nil && cidr.Contains(ipAddr) {
				return true
			}
		}
	}
	return false
}

// InstanceFirewall returns the operator overrides for a host.
func (r *limiterResolver) InstanceFirewall(host string) *ratelimit.Overrides {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	inst, err := r.repo.GetInstanceByHost(ctx, host, r.cfg.ApexDomain)
	if err != nil {
		return nil
	}
	fw, err := r.repo.InstanceFirewall(ctx, inst.ID)
	if err != nil || fw == nil {
		return nil
	}
	return &ratelimit.Overrides{
		IPHourly:           fw.IPHourly,
		InstanceHourly:     fw.InstanceHourly,
		IPConcurrent:       fw.IPConcurrent,
		InstanceConcurrent: fw.InstanceConcurrent,
	}
}
