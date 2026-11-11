// Package metrics keeps the edge traffic counters that used to live inside
// ProxyService.ts and EdgeTrafficStats.ts.
package metrics

import (
	"sort"
	"sync"
)

// Snapshot is the JSON shape reported to the mothership.
type Snapshot struct {
	Requests  int64    `json:"requests"`
	Errors    int64    `json:"errors"`
	Hosts     [][2]any `json:"hosts"`
	IPs       [][2]any `json:"ips"`
	Countries [][2]any `json:"countries"`
}

// Stats is an in-memory, flushable traffic counter.
type Stats struct {
	mu        sync.Mutex
	requests  int64
	errors    int64
	hosts     map[string]int64
	ips       map[string]int64
	countries map[string]int64
}

// New creates an empty Stats.
func New() *Stats {
	return &Stats{
		hosts:     map[string]int64{},
		ips:       map[string]int64{},
		countries: map[string]int64{},
	}
}

// AddRequest counts one proxied request.
func (s *Stats) AddRequest() {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
}

// AddError counts one proxy error.
func (s *Stats) AddError() {
	s.mu.Lock()
	s.errors++
	s.mu.Unlock()
}

// AddHost counts one request for a host.
func (s *Stats) AddHost(host string) {
	if host == "" {
		host = "<unknown>"
	}
	s.mu.Lock()
	s.hosts[host]++
	s.mu.Unlock()
}

// AddIP counts one request for a client IP.
func (s *Stats) AddIP(ip string) {
	if ip == "" {
		ip = "<ip>"
	}
	s.mu.Lock()
	s.ips[ip]++
	s.mu.Unlock()
}

// AddCountry counts one request for a Cloudflare country code.
func (s *Stats) AddCountry(c string) {
	if c == "" {
		c = "<ct>"
	}
	s.mu.Lock()
	s.countries[c]++
	s.mu.Unlock()
}

// Snapshot returns the current counters with the top-10 breakdowns.
func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Requests:  s.requests,
		Errors:    s.errors,
		Hosts:     top(s.hosts, 10),
		IPs:       top(s.ips, 10),
		Countries: top(s.countries, 10),
	}
}

// Flush resets the counters (called after a successful heartbeat).
func (s *Stats) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = 0
	s.errors = 0
	s.hosts = map[string]int64{}
	s.ips = map[string]int64{}
	s.countries = map[string]int64{}
}

func top(m map[string]int64, limit int) [][2]any {
	type kv struct {
		k string
		v int64
	}
	all := make([]kv, 0, len(m))
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v == all[j].v {
			return all[i].k < all[j].k
		}
		return all[i].v > all[j].v
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([][2]any, 0, len(all))
	for _, e := range all {
		out = append(out, [2]any{e.k, e.v})
	}
	return out
}
