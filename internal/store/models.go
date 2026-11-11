package store

import (
	"time"
)

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

// WebhookItem mirrors `InstanceWebhookItem`.
type WebhookItem struct {
	Endpoint  string        `json:"endpoint"`
	Value     string        `json:"value"`
	LastFired *WebhookFired `json:"lastFired,omitempty"`
}

// WebhookFired mirrors `lastFired`.
type WebhookFired struct {
	Timestamp int64           `json:"timestamp"`
	Response  WebhookResponse `json:"response"`
}

// WebhookResponse mirrors `response`.
type WebhookResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Firewall mirrors the operator-only `InstanceFirewall` overrides.
type Firewall struct {
	IPHourly           *int `json:"ip_hourly,omitempty"`
	InstanceHourly     *int `json:"instance_hourly,omitempty"`
	IPConcurrent       *int `json:"ip_concurrent,omitempty"`
	InstanceConcurrent *int `json:"instance_concurrent,omitempty"`
}

// Instance is the Go equivalent of `InstanceFields`, serialised in PocketBase
// record shape so the Svelte dashboard consumes it unchanged.
type Instance struct {
	ID           string            `json:"id"`
	CollectionID string            `json:"collectionId"`
	Collection   string            `json:"collectionName"`
	Created      string            `json:"created"`
	Updated      string            `json:"updated"`
	Subdomain    string            `json:"subdomain"`
	UID          string            `json:"uid"`
	Status       InstanceStatus    `json:"status"`
	Version      string            `json:"version"`
	Secrets      map[string]string `json:"secrets"`
	Webhooks     []WebhookItem     `json:"webhooks"`
	Power        bool              `json:"power"`
	Suspension   string            `json:"suspension"`
	SyncAdmin    bool              `json:"syncAdmin"`
	AutoVacuum   bool              `json:"autoVacuum"`
	CNAME        string            `json:"cname"`
	CNAMEActive  bool              `json:"cname_active"`
	Dev          bool              `json:"dev"`
	IdleTTL      int               `json:"idleTtl"`
	Firewall     *Firewall         `json:"firewall"`
	Edge         string            `json:"edge"`

	// Expand carries `expand.uid` for the `?expand=uid` API requests the
	// dashboard issues.
	Expand map[string]any `json:"expand,omitempty"`

	// Runtime is not persisted in the instances table; it is joined from the
	// native process registry for admin/CLI output.
	Runtime *Process `json:"runtime,omitempty"`
}

// CollectionIDInstances is the PocketBase collection id used in API payloads.
const CollectionIDInstances = "pbc_instances"

// IsPowerOn reports whether the instance is allowed to run.
func (i *Instance) IsPowerOn() bool { return i.Power }

// Subscription types, mirroring the legacy enum.
const (
	SubscriptionLegacy   = "legacy"
	SubscriptionFree     = "free"
	SubscriptionPremium  = "premium"
	SubscriptionFounder  = "founder"
	SubscriptionFlounder = "flounder"
)

// User is the Go equivalent of `UserFields` (plus credentials for auth).
type User struct {
	ID                   string   `json:"id"`
	CollectionID         string   `json:"collectionId"`
	Collection           string   `json:"collectionName"`
	Created              string   `json:"created"`
	Updated              string   `json:"updated"`
	Email                string   `json:"email"`
	EmailVisibility      bool     `json:"emailVisibility"`
	Verified             bool     `json:"verified"`
	Username             string   `json:"username"`
	Name                 string   `json:"name"`
	Avatar               string   `json:"avatar"`
	Subscription         string   `json:"subscription"`
	SubscriptionInterval string   `json:"subscription_interval"`
	SubscriptionQuantity int      `json:"subscription_quantity"`
	SubscriptionStatus   string   `json:"subscription_status"`
	ProductSKU           string   `json:"product_sku"`
	BillingProvider      string   `json:"billing_provider"`
	BillingLinks         string   `json:"billing_links"`
	IsLegacy             bool     `json:"isLegacy"`
	VolumeStorageUsed    int64    `json:"volume_storage_used"`
	ObjectStorageUsed    int64    `json:"object_storage_used"`
	OverLimitSince       string   `json:"over_limit_since"`
	LapsedSince          string   `json:"lapsed_since"`
	TrustedIPs           []string `json:"trusted_ips"`
	Suspension           string   `json:"suspension"`
	Unsubscribe          bool     `json:"unsubscribe"`
	NotifyMaintenance    bool     `json:"notifyMaintenanceMode"`
	DiscordID            string   `json:"discord_id"`

	// Credentials never leave the store package except for auth checks.
	TokenKey     string `json:"-"`
	PasswordHash string `json:"-"`
}

// CollectionIDUsers is the PocketBase collection id used in API payloads.
const CollectionIDUsers = "pbc_users"

// Edge mirrors the `edges` collection.
type Edge struct {
	ID          string `json:"id"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
	EdgeID      string `json:"edge_id"`
	Region      string `json:"region"`
	Status      string `json:"status"`
	InternalURL string `json:"internal_url"`
	LastSeen    string `json:"last_seen"`
	Version     string `json:"version"`
}

// Process is the durable record of a native PocketBase process.
type Process struct {
	InstanceID string `json:"instance_id"`
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	Version    string `json:"version"`
	BinPath    string `json:"bin_path"`
	WorkDir    string `json:"workdir"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	StoppedAt  string `json:"stopped_at"`
	ExitCode   int    `json:"exit_code"`
	Restarts   int    `json:"restarts"`
	LastError  string `json:"last_error"`
}

// Alive reports whether the registry entry claims a live process.
func (p *Process) Alive() bool {
	return p != nil && p.Status == "running" && p.PID > 0
}

// SSHKey mirrors the `ssh_keys` collection.
type SSHKey struct {
	ID          string `json:"id"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// APIToken mirrors the `api_tokens` collection.
type APIToken struct {
	ID       string `json:"id"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	Scope    string `json:"scope"`
	LastUsed string `json:"last_used"`
}

// Version mirrors the `versions` collection.
type Version struct {
	ID       string `json:"id"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
	Version  string `json:"version"`
	Notes    string `json:"notes"`
	Platform string `json:"platform"`
	SHA256   string `json:"sha256"`
}

// timeNow is swappable in tests.
var timeNow = time.Now
