package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const userColumns = `id, created, updated, email, emailVisibility, verified, username, name, avatar,
 subscription, subscription_interval, subscription_quantity, subscription_status, product_sku,
 billing_provider, billing_links, isLegacy, volume_storage_used, object_storage_used,
 over_limit_since, lapsed_since, trusted_ips, suspension, unsubscribe, notifyMaintenanceMode,
 discord_id, tokenKey, password`

func scanUser(sc interface{ Scan(...any) error }) (*User, error) {
	var (
		u           User
		emailVis    int
		verified    int
		legacy      int
		unsub       int
		notifyMaint int
		trustedIPs  string
	)
	err := sc.Scan(
		&u.ID, &u.Created, &u.Updated, &u.Email, &emailVis, &verified, &u.Username, &u.Name, &u.Avatar,
		&u.Subscription, &u.SubscriptionInterval, &u.SubscriptionQuantity, &u.SubscriptionStatus,
		&u.ProductSKU, &u.BillingProvider, &u.BillingLinks, &legacy, &u.VolumeStorageUsed,
		&u.ObjectStorageUsed, &u.OverLimitSince, &u.LapsedSince, &trustedIPs, &u.Suspension,
		&unsub, &notifyMaint, &u.DiscordID, &u.TokenKey, &u.PasswordHash,
	)
	if err != nil {
		return nil, err
	}
	u.CollectionID = CollectionIDUsers
	u.Collection = "users"
	u.EmailVisibility = emailVis != 0
	u.Verified = verified != 0
	u.IsLegacy = legacy != 0
	u.Unsubscribe = unsub != 0
	u.NotifyMaintenance = notifyMaint != 0
	if t := UnmarshalJSONColumn[[]string](trustedIPs); t != nil {
		u.TrustedIPs = *t
	}
	return &u, nil
}

// GetUser loads a user by record id.
func (r *Repo) GetUser(ctx context.Context, id string) (*User, error) {
	u, err := scanUser(r.DB.QueryRowContext(ctx, `select `+userColumns+` from users where id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetUserByEmail loads a user by email (case-insensitive), matching PocketBase.
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u, err := scanUser(r.DB.QueryRowContext(ctx, `select `+userColumns+` from users where lower(email) = lower(?)`, email))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// CreateUser inserts a user.
func (r *Repo) CreateUser(ctx context.Context, u *User) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	now := NowString()
	if u.Created == "" {
		u.Created = now
	}
	u.Updated = now
	if u.Subscription == "" {
		u.Subscription = SubscriptionFree
	}
	if u.SubscriptionInterval == "" {
		u.SubscriptionInterval = "month"
	}
	if u.Username == "" {
		u.Username = "users" + u.ID[:6]
	}
	u.CollectionID = CollectionIDUsers
	u.Collection = "users"

	_, err := r.DB.ExecContext(ctx, `
insert into users (`+userColumns+`) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.Created, u.Updated, u.Email, boolInt(u.EmailVisibility), boolInt(u.Verified),
		u.Username, u.Name, u.Avatar, u.Subscription, u.SubscriptionInterval, u.SubscriptionQuantity,
		u.SubscriptionStatus, u.ProductSKU, u.BillingProvider, u.BillingLinks, boolInt(u.IsLegacy),
		u.VolumeStorageUsed, u.ObjectStorageUsed, u.OverLimitSince, u.LapsedSince,
		MarshalJSONColumn(u.TrustedIPs), u.Suspension, boolInt(u.Unsubscribe),
		boolInt(u.NotifyMaintenance), u.DiscordID, u.TokenKey, u.PasswordHash,
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("store: user %q already exists: %w", u.Email, err)
	}
	return err
}

// UpdateUser persists mutable user fields.
func (r *Repo) UpdateUser(ctx context.Context, u *User) error {
	u.Updated = NowString()
	res, err := r.DB.ExecContext(ctx, `
update users set updated=?, email=?, emailVisibility=?, verified=?, username=?, name=?, avatar=?,
 subscription=?, subscription_interval=?, subscription_quantity=?, subscription_status=?,
 product_sku=?, billing_provider=?, billing_links=?, isLegacy=?, volume_storage_used=?,
 object_storage_used=?, over_limit_since=?, lapsed_since=?, trusted_ips=?, suspension=?,
 unsubscribe=?, notifyMaintenanceMode=?, discord_id=?, tokenKey=?, password=? where id=?`,
		u.Updated, u.Email, boolInt(u.EmailVisibility), boolInt(u.Verified), u.Username, u.Name, u.Avatar,
		u.Subscription, u.SubscriptionInterval, u.SubscriptionQuantity, u.SubscriptionStatus,
		u.ProductSKU, u.BillingProvider, u.BillingLinks, boolInt(u.IsLegacy), u.VolumeStorageUsed,
		u.ObjectStorageUsed, u.OverLimitSince, u.LapsedSince, MarshalJSONColumn(u.TrustedIPs),
		u.Suspension, boolInt(u.Unsubscribe), boolInt(u.NotifyMaintenance), u.DiscordID,
		u.TokenKey, u.PasswordHash, u.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUsers returns all users ordered by creation time.
func (r *Repo) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := r.DB.QueryContext(ctx, `select `+userColumns+` from users order by created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// TrustedIPs returns the account-level trusted IP list (firewall bypass).
func (r *Repo) TrustedIPs(ctx context.Context, uid string) ([]string, error) {
	var raw string
	err := r.DB.QueryRowContext(ctx, `select trusted_ips from users where id = ?`, uid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t := UnmarshalJSONColumn[[]string](raw); t != nil {
		return *t, nil
	}
	return nil, nil
}

// InstanceFirewall returns the per-instance firewall overrides (nil when unset).
func (r *Repo) InstanceFirewall(ctx context.Context, instanceID string) (*Firewall, error) {
	var raw string
	err := r.DB.QueryRowContext(ctx, `select firewall from instances where id = ?`, instanceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return UnmarshalJSONColumn[Firewall](raw), nil
}
