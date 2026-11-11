package store

import (
	"context"

	"github.com/incurl/pocketghost/internal/mothership"
)

// Repo is the typed data-access layer over the control-plane database.
//
// It is backed by the PocketBase app, so the edge reads exactly the records the
// original mothership hooks write.
type Repo struct {
	store *mothership.Store
}

// NewRepo wraps a PocketBase-backed store.
func NewRepo(store *mothership.Store) *Repo { return &Repo{store: store} }

// Store exposes the underlying PocketBase store.
func (r *Repo) Store() *mothership.Store { return r.store }

func toInstance(i *mothership.Instance) *Instance {
	if i == nil {
		return nil
	}
	return &Instance{
		ID:         i.ID,
		Subdomain:  i.Subdomain,
		UID:        i.UID,
		Status:     InstanceStatus(i.Status),
		Version:    i.Version,
		Power:      i.Power,
		CNAME:      i.CNAME,
		Edge:       i.Edge,
		Dev:        i.Dev,
		SyncAdmin:  i.SyncAdmin,
		AutoVacuum: i.AutoVacuum,
		Suspension: i.Suspension,
		IdleTTL:    i.IdleTTL,
		Secrets:    i.Secrets,
	}
}

func toUser(u *mothership.User) *User {
	if u == nil {
		return nil
	}
	return &User{
		ID:                   u.ID,
		Email:                u.Email,
		Verified:             u.Verified,
		Subscription:         u.Subscription,
		SubscriptionQuantity: u.SubscriptionQuantity,
		SubscriptionStatus:   u.SubscriptionStatus,
		Suspension:           u.Suspension,
		TrustedIPs:           u.TrustedIPs,
		TokenKey:             u.TokenKey,
		PasswordHash:         u.PasswordHash,
	}
}

// GetInstance loads an instance by record id.
func (r *Repo) GetInstance(ctx context.Context, id string) (*Instance, error) {
	i, err := r.store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	return toInstance(i), nil
}

// GetInstanceBySubdomain loads an instance by its subdomain.
func (r *Repo) GetInstanceBySubdomain(ctx context.Context, subdomain string) (*Instance, error) {
	i, err := r.store.GetInstanceBySubdomain(ctx, subdomain)
	if err != nil {
		return nil, err
	}
	return toInstance(i), nil
}

// GetInstanceByHost resolves a hostname to an instance.
func (r *Repo) GetInstanceByHost(ctx context.Context, host, apex string) (*Instance, error) {
	i, err := r.store.GetInstanceByHost(ctx, host, apex)
	if err != nil {
		return nil, err
	}
	return toInstance(i), nil
}

// ListInstances returns all instances, optionally filtered by owner.
func (r *Repo) ListInstances(ctx context.Context, uid string) ([]*Instance, error) {
	list, err := r.store.ListInstances(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]*Instance, 0, len(list))
	for _, i := range list {
		out = append(out, toInstance(i))
	}
	return out, nil
}

// CountInstancesByUID counts instances owned by a user (plan limit checks).
func (r *Repo) CountInstancesByUID(ctx context.Context, uid string) (int, error) {
	list, err := r.store.ListInstances(ctx, uid)
	if err != nil {
		return 0, err
	}
	return len(list), nil
}

// CountRunningInstancesByUID counts instances currently running for a user.
func (r *Repo) CountRunningInstancesByUID(ctx context.Context, uid string) (int, error) {
	list, err := r.store.ListInstances(ctx, uid)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, i := range list {
		if InstanceStatus(i.Status) != StatusIdle {
			n++
		}
	}
	return n, nil
}

// GetUser loads a user by record id.
func (r *Repo) GetUser(ctx context.Context, id string) (*User, error) {
	u, err := r.store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return toUser(u), nil
}

// GetUserByEmail loads a user by email.
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u, err := r.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return toUser(u), nil
}

// TrustedIPs returns the account-level trusted IP list (firewall bypass).
func (r *Repo) TrustedIPs(ctx context.Context, uid string) ([]string, error) {
	u, err := r.store.GetUser(ctx, uid)
	if err != nil {
		return nil, nil
	}
	return u.TrustedIPs, nil
}

// CreateInstance inserts a new instance record.
func (r *Repo) CreateInstance(ctx context.Context, inst *Instance) error {
	saved, err := r.store.CreateInstance(ctx, &mothership.Instance{
		ID:         inst.ID,
		Subdomain:  inst.Subdomain,
		UID:        inst.UID,
		Status:     string(inst.Status),
		Version:    inst.Version,
		Power:      inst.Power,
		CNAME:      inst.CNAME,
		Edge:       inst.Edge,
		Dev:        inst.Dev,
		SyncAdmin:  inst.SyncAdmin,
		AutoVacuum: inst.AutoVacuum,
		Suspension: inst.Suspension,
		IdleTTL:    inst.IdleTTL,
		Secrets:    inst.Secrets,
	})
	if err != nil {
		return err
	}
	inst.ID = saved.ID
	return nil
}

// CreateUser inserts a new user record.
func (r *Repo) CreateUser(ctx context.Context, u *User) error {
	saved, err := r.store.CreateUser(ctx, &mothership.User{
		ID:                   u.ID,
		Email:                u.Email,
		Verified:             u.Verified,
		Subscription:         u.Subscription,
		SubscriptionQuantity: u.SubscriptionQuantity,
		SubscriptionStatus:   u.SubscriptionStatus,
		Suspension:           u.Suspension,
		TokenKey:             u.TokenKey,
		PasswordHash:         u.PasswordHash,
	})
	if err != nil {
		return err
	}
	u.ID = saved.ID
	return nil
}

// ListSSHKeys returns the SSH keys registered to a user.
func (r *Repo) ListSSHKeys(ctx context.Context, uid string) ([]*SSHKey, error) {
	list, err := r.store.ListSSHKeys(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]*SSHKey, 0, len(list))
	for _, k := range list {
		out = append(out, &SSHKey{
			ID:          k.ID,
			Name:        k.Name,
			Key:         k.Key,
			Fingerprint: k.Fingerprint,
		})
	}
	return out, nil
}

// InstanceFirewall returns the per-instance firewall overrides (nil when unset).
func (r *Repo) InstanceFirewall(ctx context.Context, instanceID string) (*Firewall, error) {
	raw, err := r.store.FirewallJSON(ctx, instanceID)
	if err != nil || raw == "" {
		return nil, err
	}
	return parseFirewall(raw), nil
}

// PatchInstanceFields applies a partial field map (status sync back to the
// control plane).
func (r *Repo) PatchInstanceFields(ctx context.Context, id string, fields map[string]any) error {
	return r.store.UpdateInstanceFields(ctx, id, fields)
}
