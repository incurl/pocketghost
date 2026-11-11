package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("store: record not found")

// Repo is the typed data-access layer over the metadata database.
type Repo struct {
	DB *sql.DB
}

// NewRepo wraps a database handle.
func NewRepo(db *sql.DB) *Repo { return &Repo{DB: db} }

const instanceColumns = `id, created, updated, subdomain, uid, status, version, secrets, webhooks,
 power, suspension, syncAdmin, autoVacuum, cname, cname_active, dev, idleTtl, firewall, edge`

func scanInstance(sc interface{ Scan(...any) error }) (*Instance, error) {
	var (
		i        Instance
		secrets  string
		webhooks string
		firewall string
		power    int
		syncAdm  int
		autoVac  int
		cnameAct int
		dev      int
	)
	err := sc.Scan(
		&i.ID, &i.Created, &i.Updated, &i.Subdomain, &i.UID, &i.Status, &i.Version,
		&secrets, &webhooks, &power, &i.Suspension, &syncAdm, &autoVac, &i.CNAME,
		&cnameAct, &dev, &i.IdleTTL, &firewall, &i.Edge,
	)
	if err != nil {
		return nil, err
	}
	i.CollectionID = CollectionIDInstances
	i.Collection = "instances"
	i.Power = power != 0
	i.SyncAdmin = syncAdm != 0
	i.AutoVacuum = autoVac != 0
	i.CNAMEActive = cnameAct != 0
	i.Dev = dev != 0
	i.Secrets = map[string]string{}
	if sec := UnmarshalJSONColumn[map[string]string](secrets); sec != nil {
		i.Secrets = *sec
	}
	if wh := UnmarshalJSONColumn[[]WebhookItem](webhooks); wh != nil {
		i.Webhooks = *wh
	}
	i.Firewall = UnmarshalJSONColumn[Firewall](firewall)
	return &i, nil
}

// GetInstance loads an instance by record id.
func (r *Repo) GetInstance(ctx context.Context, id string) (*Instance, error) {
	row := r.DB.QueryRowContext(ctx, `select `+instanceColumns+` from instances where id = ?`, id)
	i, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// GetInstanceBySubdomain loads an instance by its (case-insensitive) subdomain.
func (r *Repo) GetInstanceBySubdomain(ctx context.Context, subdomain string) (*Instance, error) {
	row := r.DB.QueryRowContext(ctx, `select `+instanceColumns+` from instances where lower(subdomain) = lower(?)`, subdomain)
	i, err := scanInstance(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// GetInstanceByHost resolves a hostname to an instance, honouring a custom
// CNAME first and falling back to `<subdomain>.<apex>`.
func (r *Repo) GetInstanceByHost(ctx context.Context, host, apex string) (*Instance, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	row := r.DB.QueryRowContext(ctx, `select `+instanceColumns+` from instances where lower(cname) = ? and cname != ''`, host)
	if i, err := scanInstance(row); err == nil {
		return i, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	suffix := "." + strings.ToLower(apex)
	if !strings.HasSuffix(host, suffix) {
		return nil, ErrNotFound
	}
	sub := strings.TrimSuffix(host, suffix)
	if sub == "" || strings.Contains(sub, ".") {
		return nil, ErrNotFound
	}
	return r.GetInstanceBySubdomain(ctx, sub)
}

// ListInstances returns all instances, optionally filtered by owner.
func (r *Repo) ListInstances(ctx context.Context, uid string) ([]*Instance, error) {
	q := `select ` + instanceColumns + ` from instances`
	var args []any
	if uid != "" {
		q += ` where uid = ?`
		args = append(args, uid)
	}
	q += ` order by created`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ListInstancesByHosts returns instances matching any of the given subdomains.
func (r *Repo) ListInstancesBySubdomains(ctx context.Context, subdomains []string) ([]*Instance, error) {
	if len(subdomains) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(subdomains))
	args := make([]any, len(subdomains))
	for i, s := range subdomains {
		placeholders[i] = "?"
		args[i] = strings.ToLower(s)
	}
	q := `select ` + instanceColumns + ` from instances where lower(subdomain) in (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Instance
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// CreateInstance inserts a new instance, filling id/created/updated defaults.
func (r *Repo) CreateInstance(ctx context.Context, in *Instance) error {
	if in.ID == "" {
		in.ID = NewID()
	}
	now := NowString()
	if in.Created == "" {
		in.Created = now
	}
	in.Updated = now
	if in.Status == StatusUnknown {
		in.Status = StatusIdle
	}
	in.CollectionID = CollectionIDInstances
	in.Collection = "instances"

	_, err := r.DB.ExecContext(ctx, `
insert into instances (`+instanceColumns+`)
values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.ID, in.Created, in.Updated, in.Subdomain, in.UID, string(in.Status), in.Version,
		MarshalJSONColumn(in.Secrets), MarshalJSONColumn(in.Webhooks), boolInt(in.Power),
		in.Suspension, boolInt(in.SyncAdmin), boolInt(in.AutoVacuum), in.CNAME,
		boolInt(in.CNAMEActive), boolInt(in.Dev), in.IdleTTL, firewallColumn(in.Firewall), in.Edge,
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("store: instance %q already exists: %w", in.Subdomain, err)
	}
	return err
}

func firewallColumn(f *Firewall) string {
	if f == nil {
		return ""
	}
	return MarshalJSONColumn(f)
}

// UpdateInstance persists mutable instance fields and bumps `updated`.
func (r *Repo) UpdateInstance(ctx context.Context, in *Instance) error {
	in.Updated = NowString()
	res, err := r.DB.ExecContext(ctx, `
update instances set updated=?, subdomain=?, status=?, version=?, secrets=?, webhooks=?,
  power=?, suspension=?, syncAdmin=?, autoVacuum=?, cname=?, cname_active=?, dev=?, idleTtl=?,
  firewall=?, edge=? where id=?`,
		in.Updated, in.Subdomain, string(in.Status), in.Version,
		MarshalJSONColumn(in.Secrets), MarshalJSONColumn(in.Webhooks), boolInt(in.Power),
		in.Suspension, boolInt(in.SyncAdmin), boolInt(in.AutoVacuum), in.CNAME,
		boolInt(in.CNAMEActive), boolInt(in.Dev), in.IdleTTL, firewallColumn(in.Firewall), in.Edge, in.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PatchInstanceFields applies a partial field map, matching the legacy
// `client.updateInstance(id, fields)` behaviour used by the daemon to sync
// status back to the control plane. Only known keys are honoured.
func (r *Repo) PatchInstanceFields(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		switch k {
		case "status":
			sets = append(sets, "status = ?")
			args = append(args, fmt.Sprint(v))
		case "power":
			sets = append(sets, "power = ?")
			args = append(args, boolInt(toBool(v)))
		case "subdomain":
			sets = append(sets, "subdomain = ?")
			args = append(args, fmt.Sprint(v))
		case "version":
			sets = append(sets, "version = ?")
			args = append(args, fmt.Sprint(v))
		case "secrets":
			sets = append(sets, "secrets = ?")
			args = append(args, MarshalJSONColumn(v))
		case "webhooks":
			sets = append(sets, "webhooks = ?")
			args = append(args, MarshalJSONColumn(v))
		case "syncAdmin":
			sets = append(sets, "syncAdmin = ?")
			args = append(args, boolInt(toBool(v)))
		case "autoVacuum":
			sets = append(sets, "autoVacuum = ?")
			args = append(args, boolInt(toBool(v)))
		case "dev":
			sets = append(sets, "dev = ?")
			args = append(args, boolInt(toBool(v)))
		case "cname":
			sets = append(sets, "cname = ?")
			args = append(args, fmt.Sprint(v))
		case "cname_active":
			sets = append(sets, "cname_active = ?")
			args = append(args, boolInt(toBool(v)))
		case "idleTtl":
			sets = append(sets, "idleTtl = ?")
			args = append(args, toInt(v))
		case "edge":
			sets = append(sets, "edge = ?")
			args = append(args, fmt.Sprint(v))
		case "suspension":
			sets = append(sets, "suspension = ?")
			args = append(args, fmt.Sprint(v))
		}
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated = ?")
	args = append(args, NowString(), id)

	res, err := r.DB.ExecContext(ctx, `update instances set `+strings.Join(sets, ", ")+` where id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteInstance removes an instance and its runtime row.
func (r *Repo) DeleteInstance(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `delete from instances where id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountInstancesByUID counts instances owned by a user (plan limit checks).
func (r *Repo) CountInstancesByUID(ctx context.Context, uid string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `select count(*) from instances where uid = ?`, uid).Scan(&n)
	return n, err
}

// CountRunningInstancesByUID counts instances currently running for a user.
func (r *Repo) CountRunningInstancesByUID(ctx context.Context, uid string) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`select count(*) from instances where uid = ? and status != 'idle'`, uid).Scan(&n)
	return n, err
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
