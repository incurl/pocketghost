package instance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

// BackupTokenHeader is the header the seeded instance backup route expects.
const BackupTokenHeader = "x-ph-backup-token"

// SecretKeyRe matches valid instance secret names (`/^[A-Z][A-Z0-9_]*$/`).
var SecretKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// BackupToken derives the per-instance backup token exactly like
// `mkBackupToken(instanceId, secret)`.
func BackupToken(instanceID, secret string) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("backup-token:" + instanceID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// adminSyncPayload mirrors the JSON the instance-app `_ph_admin_sync` hook reads.
type adminSyncPayload struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	TokenKey     string `json:"tokenKey"`
	PasswordHash string `json:"passwordHash"`
}

// instanceEnv builds the child process environment: secrets, dev flag, apex
// domain, admin sync credentials and the backup token.
func (s *Service) instanceEnv(inst *store.Instance, owner *store.User, log *logging.Logger) map[string]string {
	env := map[string]string{
		"PH_APEX_DOMAIN": s.cfg.ApexDomain,
	}

	if inst.Dev {
		env["DEV"] = "true"
	}

	for k, v := range inst.Secrets {
		if !SecretKeyRe.MatchString(k) {
			continue
		}
		env[k] = v
	}

	if inst.SyncAdmin {
		payload, err := json.Marshal(adminSyncPayload{
			ID:           owner.ID,
			Email:        owner.Email,
			TokenKey:     owner.TokenKey,
			PasswordHash: owner.PasswordHash,
		})
		if err != nil {
			log.Warnf("could not build ADMIN_SYNC payload: %v", err)
		} else {
			env["ADMIN_SYNC"] = string(payload)
		}
	}

	if token := BackupToken(inst.ID, s.cfg.Secret); token != "" {
		env["PH_BACKUP_TOKEN"] = token
	}

	return env
}

// seedInstanceApp copies the bundled instance-app migrations and hooks into the
// instance working directory. In the container world these were bind mounts; with
// native processes a copy keeps a tenant from mutating the shared template.
//
// The source directory is PH_INSTANCE_APP_ROOT (defaults to ./instance-app next
// to the binary). When it is absent the daemon relies on the instance's own
// pb_migrations/pb_hooks folders, exactly as before.
func (s *Service) seedInstanceApp(workDir string, log *logging.Logger) {
	src := strings.TrimSpace(os.Getenv("PH_INSTANCE_APP_ROOT"))
	if src == "" {
		return
	}
	for _, sub := range []string{"pb_migrations", "pb_hooks"} {
		from := filepath.Join(src, sub)
		if st, err := os.Stat(from); err != nil || !st.IsDir() {
			continue
		}
		to := filepath.Join(workDir, sub)
		if err := copyTree(from, to); err != nil {
			log.Warnf("seed %s from %s failed: %v", sub, from, err)
		}
	}
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Never overwrite files the tenant created.
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// orDefault returns v when non-empty, otherwise fallback.
func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var _ = context.Background
