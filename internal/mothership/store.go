package mothership

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("mothership: record not found")

// Instance mirrors the `instances` collection fields the edge reads.
type Instance struct {
	ID         string
	Subdomain  string
	UID        string
	Status     string
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

// User mirrors the `users` collection fields the edge reads.
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

// Store reads the mothership records through the PocketBase app.
//
// This replaces the hand-rolled SQLite repository: the PocketBase app owns
// the database, and this type is a thin, typed view over its collections.
type Store struct {
	app core.App
}

// NewStore creates a store backed by the given app.
func NewStore(app core.App) *Store { return &Store{app: app} }

// App exposes the underlying PocketBase app.
func (s *Store) App() core.App { return s.app }

func (s *Store) instanceFromRecord(r *core.Record) *Instance {
	if r == nil {
		return nil
	}
	inst := &Instance{
		ID:         r.Id,
		Subdomain:  r.GetString("subdomain"),
		UID:        r.GetString("uid"),
		Status:     r.GetString("status"),
		Version:    r.GetString("version"),
		Power:      r.GetBool("power"),
		CNAME:      r.GetString("cname"),
		Edge:       r.GetString("edge"),
		Dev:        r.GetBool("dev"),
		SyncAdmin:  r.GetBool("syncAdmin"),
		AutoVacuum: r.GetBool("autoVacuum"),
		Suspension: r.GetString("suspension"),
		IdleTTL:    r.GetInt("idleTtl"),
	}
	if raw := r.Get("secrets"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			inst.Secrets = make(map[string]string, len(m))
			for k, v := range m {
				if sv, ok := v.(string); ok {
					inst.Secrets[k] = sv
				}
			}
		}
	}
	return inst
}

func (s *Store) userFromRecord(r *core.Record) *User {
	if r == nil {
		return nil
	}
	u := &User{
		ID:                   r.Id,
		Email:                r.GetString("email"),
		Verified:             r.GetBool("verified"),
		Subscription:         r.GetString("subscription"),
		SubscriptionQuantity: r.GetInt("subscription_quantity"),
		SubscriptionStatus:   r.GetString("subscription_status"),
		Suspension:           r.GetString("suspension"),
		TokenKey:             r.GetString("tokenKey"),
		PasswordHash:         r.GetString("password"),
	}
	if raw := r.Get("trusted_ips"); raw != nil {
		if list, ok := raw.([]any); ok {
			for _, v := range list {
				if sv, ok := v.(string); ok {
					u.TrustedIPs = append(u.TrustedIPs, sv)
				}
			}
		}
	}
	return u
}

// GetInstance loads an instance by record id.
func (s *Store) GetInstance(ctx context.Context, id string) (*Instance, error) {
	r, err := s.app.FindRecordById("instances", id)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.instanceFromRecord(r), nil
}

// GetInstanceBySubdomain loads an instance by subdomain.
func (s *Store) GetInstanceBySubdomain(ctx context.Context, subdomain string) (*Instance, error) {
	r, err := s.app.FindFirstRecordByData("instances", "subdomain", subdomain)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.instanceFromRecord(r), nil
}

// GetInstanceByHost resolves a hostname to an instance, honouring a custom CNAME
// before falling back to the `<subdomain>.<apex>` mapping.
func (s *Store) GetInstanceByHost(ctx context.Context, host, apex string) (*Instance, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}

	if r, err := s.app.FindFirstRecordByData("instances", "cname", host); err == nil {
		if cname := r.GetString("cname"); cname != "" {
			return s.instanceFromRecord(r), nil
		}
	}

	suffix := "." + strings.ToLower(apex)
	if !strings.HasSuffix(host, suffix) {
		return nil, ErrNotFound
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") {
		return nil, ErrNotFound
	}
	return s.GetInstanceBySubdomain(ctx, sub)
}

// CreateInstance inserts a new instance record and returns the stored record.
//
// The `instances` collection stores `uid` and `edge` as *relation* fields, so
// they are written as single-relation values (a record id), not plain text.
func (s *Store) CreateInstance(ctx context.Context, inst *Instance) (*Instance, error) {
	collection, err := s.app.FindCollectionByNameOrId("instances")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	if inst.ID != "" {
		record.Id = inst.ID
	}
	record.Set("subdomain", inst.Subdomain)
	record.Set("uid", inst.UID)
	record.Set("status", inst.Status)
	record.Set("version", inst.Version)
	record.Set("power", inst.Power)
	record.Set("cname", inst.CNAME)
	record.Set("edge", inst.Edge)
	record.Set("dev", inst.Dev)
	record.Set("syncAdmin", inst.SyncAdmin)
	record.Set("autoVacuum", inst.AutoVacuum)
	record.Set("suspension", inst.Suspension)
	record.Set("idleTtl", inst.IdleTTL)
	if inst.Secrets != nil {
		record.Set("secrets", inst.Secrets)
	}
	if err := s.app.Save(record); err != nil {
		return nil, err
	}
	return s.instanceFromRecord(record), nil
}

// CreateUser inserts a new user record and returns the stored record.
func (s *Store) CreateUser(ctx context.Context, u *User) (*User, error) {
	collection, err := s.app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	if u.ID != "" {
		record.Id = u.ID
	}
	record.Set("email", u.Email)
	record.Set("verified", u.Verified)
	record.Set("subscription", u.Subscription)
	record.Set("subscription_quantity", u.SubscriptionQuantity)
	record.Set("subscription_status", u.SubscriptionStatus)
	record.Set("suspension", u.Suspension)
	if u.TokenKey != "" {
		record.Set("tokenKey", u.TokenKey)
	}
	if u.PasswordHash != "" {
		record.Set("password", u.PasswordHash)
	}
	if err := s.app.Save(record); err != nil {
		return nil, err
	}
	return s.userFromRecord(record), nil
}

// ListInstances returns every instance, optionally filtered by owner.
func (s *Store) ListInstances(ctx context.Context, uid string) ([]*Instance, error) {
	records, err := s.app.FindAllRecords("instances", nil)
	if err != nil {
		return nil, err
	}
	out := make([]*Instance, 0, len(records))
	for _, r := range records {
		inst := s.instanceFromRecord(r)
		if uid != "" && inst.UID != uid {
			continue
		}
		out = append(out, inst)
	}
	return out, nil
}

// GetUser loads a user by record id.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	r, err := s.app.FindRecordById("users", id)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.userFromRecord(r), nil
}

// GetUserByEmail loads a user by email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	r, err := s.app.FindAuthRecordByEmail("users", email)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.userFromRecord(r), nil
}

// FirewallJSON returns the raw `firewall` JSON field of an instance.
//
// The original mothership stores the operator overrides as a JSON text column,
// so the edge reads them verbatim.
func (s *Store) FirewallJSON(ctx context.Context, instanceID string) (string, error) {
	r, err := s.app.FindRecordById("instances", instanceID)
	if err != nil {
		return "", ErrNotFound
	}
	return r.GetString("firewall"), nil
}

// ListSSHKeys returns the SSH keys registered to a user.
func (s *Store) ListSSHKeys(ctx context.Context, uid string) ([]*SSHKey, error) {
	records, err := s.app.FindAllRecords("ssh_keys", nil)
	if err != nil {
		return nil, err
	}
	var out []*SSHKey
	for _, r := range records {
		if r.GetString("user") != uid {
			continue
		}
		out = append(out, &SSHKey{
			ID:          r.Id,
			Name:        r.GetString("name"),
			Key:         r.GetString("key"),
			Fingerprint: r.GetString("fingerprint"),
		})
	}
	return out, nil
}

// SSHKey mirrors the `ssh_keys` collection.
type SSHKey struct {
	ID          string
	Name        string
	Key         string
	Fingerprint string
}

// UpdateInstanceFields patches instance fields (used by the edge to sync
// status back to the control plane).
func (s *Store) UpdateInstanceFields(ctx context.Context, id string, fields map[string]any) error {
	r, err := s.app.FindRecordById("instances", id)
	if err != nil {
		return ErrNotFound
	}
	for k, v := range fields {
		switch k {
		case "status":
			r.Set("status", fmt.Sprint(v))
		case "power":
			r.Set("power", toBool(v))
		case "version":
			r.Set("version", fmt.Sprint(v))
		case "dev":
			r.Set("dev", toBool(v))
		case "syncAdmin":
			r.Set("syncAdmin", toBool(v))
		case "autoVacuum":
			r.Set("autoVacuum", toBool(v))
		case "subdomain":
			r.Set("subdomain", fmt.Sprint(v))
		case "cname":
			r.Set("cname", fmt.Sprint(v))
		case "idleTtl":
			r.Set("idleTtl", toInt(v))
		case "edge":
			r.Set("edge", fmt.Sprint(v))
		case "suspension":
			r.Set("suspension", fmt.Sprint(v))
		}
	}
	return s.app.Save(r)
}

// Process mirrors the `processes` collection (the native process registry).
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

// UpsertProcess writes the runtime state of a tenant process.
func (s *Store) UpsertProcess(ctx context.Context, p *Process) error {
	record, err := s.app.FindFirstRecordByData("processes", "instance", p.InstanceID)
	if err != nil {
		collection, cerr := s.app.FindCollectionByNameOrId("processes")
		if cerr != nil {
			return cerr
		}
		record = core.NewRecord(collection)
		record.Set("instance", p.InstanceID)
	}
	record.Set("pid", p.PID)
	record.Set("port", p.Port)
	record.Set("version", p.Version)
	record.Set("bin_path", p.BinPath)
	record.Set("workdir", p.WorkDir)
	record.Set("status", p.Status)
	record.Set("exit_code", p.ExitCode)
	record.Set("restarts", p.Restarts)
	record.Set("last_error", p.LastError)
	if p.StartedAt != "" {
		record.Set("started_at", p.StartedAt)
	}
	if p.StoppedAt != "" {
		record.Set("stopped_at", p.StoppedAt)
	}
	return s.app.Save(record)
}

// GetProcess loads the runtime row of an instance.
func (s *Store) GetProcess(ctx context.Context, instanceID string) (*Process, error) {
	r, err := s.app.FindFirstRecordByData("processes", "instance", instanceID)
	if err != nil {
		return nil, ErrNotFound
	}
	return processFromRecord(r), nil
}

// ListProcesses returns every runtime row.
func (s *Store) ListProcesses(ctx context.Context) ([]*Process, error) {
	records, err := s.app.FindAllRecords("processes", nil)
	if err != nil {
		return nil, err
	}
	out := make([]*Process, 0, len(records))
	for _, r := range records {
		out = append(out, processFromRecord(r))
	}
	return out, nil
}

func processFromRecord(r *core.Record) *Process {
	return &Process{
		InstanceID: r.GetString("instance"),
		PID:        r.GetInt("pid"),
		Port:       r.GetInt("port"),
		Version:    r.GetString("version"),
		BinPath:    r.GetString("bin_path"),
		WorkDir:    r.GetString("workdir"),
		Status:     r.GetString("status"),
		StartedAt:  r.GetString("started_at"),
		StoppedAt:  r.GetString("stopped_at"),
		ExitCode:   r.GetInt("exit_code"),
		Restarts:   r.GetInt("restarts"),
		LastError:  r.GetString("last_error"),
	}
}

// MarkProcessStopped records a clean or crashed exit.
func (s *Store) MarkProcessStopped(ctx context.Context, instanceID string, exitCode int, lastErr string) error {
	r, err := s.app.FindFirstRecordByData("processes", "instance", instanceID)
	if err != nil {
		return ErrNotFound
	}
	r.Set("status", "stopped")
	r.Set("pid", 0)
	r.Set("exit_code", exitCode)
	r.Set("last_error", lastErr)
	r.Set("stopped_at", time.Now().UTC().Format("2006-01-02 15:04:05.000Z"))
	return s.app.Save(r)
}

// PortsInUse returns the ports currently recorded as allocated.
func (s *Store) PortsInUse(ctx context.Context) (map[int]string, error) {
	rows, err := s.ListProcesses(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, r := range rows {
		if r.Port > 0 {
			out[r.Port] = r.InstanceID
		}
	}
	return out, nil
}

// IDLength is PocketBase's default record id length.
const IDLength = 15

// idAlphabet is PocketBase's record id alphabet (`[a-z0-9]{15}`).
const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewID generates a PocketBase-compatible record id.
func NewID() string {
	b := make([]byte, IDLength)
	max := big.NewInt(int64(len(idAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failure is unrecoverable; fall back to time-based
			// bytes so the caller still gets a unique-ish id.
			b[i] = idAlphabet[(time.Now().UnixNano()>>uint(i*3))%int64(len(idAlphabet))]
			continue
		}
		b[i] = idAlphabet[n.Int64()]
	}
	return string(b)
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	}
	return false
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var n int
		fmt.Sscan(x, &n)
		return n
	}
	return 0
}
