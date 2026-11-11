// Package procs implements the Docker-free replacement for the legacy container
// orchestration: every tenant gets its own native PocketBase OS process with an
// isolated working directory, an exclusively allocated loopback port, its own
// log files, PID tracking and OS-level resource limits.
package procs

import (
	"fmt"
	"net"
	"sort"
	"sync"
)

// PortAllocator hands out exclusive loopback ports for tenant processes.
//
// It replaces Docker's `PortBindings: {"8090/tcp": [{HostPort: "0"}]}` ephemeral
// mapping with a deterministic range so operations staff can firewall predictably.
type PortAllocator struct {
	mu     sync.Mutex
	min    int
	max    int
	used   map[int]string // port -> instanceID
	cursor int
}

// NewPortAllocator builds an allocator for the inclusive range [min, max].
func NewPortAllocator(min, max int) (*PortAllocator, error) {
	if min <= 0 || max > 65535 || max <= min {
		return nil, fmt.Errorf("procs: invalid port range %d-%d", min, max)
	}
	return &PortAllocator{min: min, max: max, used: map[int]string{}, cursor: min}, nil
}

// Seed marks ports as taken, typically from the persisted process registry.
func (a *PortAllocator) Seed(ports map[int]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for p, id := range ports {
		if p >= a.min && p <= a.max {
			a.used[p] = id
		}
	}
}

// Reserve marks a specific port as used by an instance.
func (a *PortAllocator) Reserve(port int, instanceID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.used[port] = instanceID
}

// Release returns a port to the pool.
func (a *PortAllocator) Release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, port)
}

// Allocate finds and reserves a free port for instanceID.
func (a *PortAllocator) Allocate(instanceID string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for p, owner := range a.used {
		if owner == instanceID {
			return p, nil
		}
	}

	span := a.max - a.min + 1
	for i := 0; i < span; i++ {
		port := a.min + (a.cursor-a.min+i)%span
		if _, taken := a.used[port]; taken {
			continue
		}
		if !portFree(port) {
			continue
		}
		a.used[port] = instanceID
		a.cursor = port + 1
		if a.cursor > a.max {
			a.cursor = a.min
		}
		return port, nil
	}
	return 0, fmt.Errorf("procs: no free port in range %d-%d", a.min, a.max)
}

// Used returns a snapshot of the reservations, sorted by port.
func (a *PortAllocator) Used() map[int]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[int]string, len(a.used))
	for k, v := range a.used {
		out[k] = v
	}
	return out
}

// Ports returns the reserved ports in ascending order.
func (a *PortAllocator) Ports() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int, 0, len(a.used))
	for p := range a.used {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func portFree(port int) bool {
	// Bind on loopback: tenant PocketBase processes listen on 127.0.0.1 only.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
