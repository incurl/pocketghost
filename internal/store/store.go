// Package store is the edge-side view over the PocketBase control-plane data.
//
// The database itself belongs to PocketBase (see internal/mothership), which
// runs the original mothership application. This package exposes the small,
// typed surface the edge needs — instances, users and the native process
// registry — so the edge code stays independent of PocketBase's API.
//
// The legacy SQLite schema this package used to own is now created by a
// PocketBase migration, so the data model is identical, just stored and served
// by PocketBase instead of a hand-rolled driver.
package store

import (
	"encoding/json"
	"time"

	"github.com/incurl/pocketghost/internal/mothership"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = mothership.ErrNotFound

// InstanceStatus matches the legacy `InstanceStatus` enum.
type InstanceStatus string

const (
	StatusUnknown   InstanceStatus = ""
	StatusIdle      InstanceStatus = "idle"
	StatusPort      InstanceStatus = "porting"
	StatusStarting  InstanceStatus = "starting"
	StatusRunning   InstanceStatus = "running"
	StatusVacuuming InstanceStatus = "vacuuming"
	StatusFailed    InstanceStatus = "failed"
)

// Subscription types, mirroring the legacy enum.
const (
	SubscriptionLegacy   = "legacy"
	SubscriptionFree     = "free"
	SubscriptionPremium  = "premium"
	SubscriptionFounder  = "founder"
	SubscriptionFlounder = "flounder"
)

// Instance is the Go equivalent of `InstanceFields`.
type Instance struct {
	ID         string
	Subdomain  string
	UID        string
	Status     InstanceStatus
	Version    string
	Power      bool
	CNAME      string
	Edge       string
	Dev        bool
	SyncAdmin  bool
	AutoVacuum bool
	Suspension string
	IdleTTL    int
	Secrets    map[string]string
}

// User is the Go equivalent of `UserFields`.
type User struct {
	ID                   string
	Email                string
	Verified             bool
	Subscription         string
	SubscriptionQuantity int
	SubscriptionStatus   string
	Suspension           string
	TrustedIPs           []string
	TokenKey             string
	PasswordHash         string
}

// SSHKey mirrors the `ssh_keys` collection.
type SSHKey struct {
	ID          string
	Name        string
	Key         string
	Fingerprint string
}

// Firewall mirrors the operator-only per-instance firewall overrides.
type Firewall struct {
	IPHourly           *int `json:"ip_hourly,omitempty"`
	InstanceHourly     *int `json:"instance_hourly,omitempty"`
	IPConcurrent       *int `json:"ip_concurrent,omitempty"`
	InstanceConcurrent *int `json:"instance_concurrent,omitempty"`
}

// Process is the durable record of a native PocketBase process.
type Process struct {
	InstanceID string
	PID        int
	Port       int
	Version    string
	BinPath    string
	WorkDir    string
	Status     string
	StartedAt  string
	StoppedAt  string
	ExitCode   int
	Restarts   int
	LastError  string
}

// Alive reports whether the registry entry claims a live process.
func (p *Process) Alive() bool {
	return p != nil && p.Status == "running" && p.PID > 0
}

// ImportStats reports what a legacy import copied.
type ImportStats = mothership.ImportStats

// PBTimeLayout is PocketBase's stored datetime format.
const PBTimeLayout = "2006-01-02 15:04:05.000Z"

// NowString returns the current UTC time in PocketBase storage format.
func NowString() string { return time.Now().UTC().Format(PBTimeLayout) }

// FormatTime converts a time to PocketBase storage format.
func FormatTime(t time.Time) string { return t.UTC().Format(PBTimeLayout) }

// NewID generates a PocketBase-compatible record id.
func NewID() string { return mothership.NewID() }

// IDLength is PocketBase's default record id length.
const IDLength = mothership.IDLength

func parseFirewall(raw string) *Firewall {
	var fw Firewall
	if err := json.Unmarshal([]byte(raw), &fw); err != nil {
		return nil
	}
	return &fw
}
