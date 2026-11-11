package instance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/store"
)

func testConfig(t *testing.T, secret string) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APEX_DOMAIN", "pockethost.io")
	t.Setenv("PH_SECRET", secret)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return cfg
}

func TestBackupTokenMatchesLegacyAlgorithm(t *testing.T) {
	got := BackupToken("abc123", "s3cret")

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("backup-token:abc123"))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
	if BackupToken("abc123", "") != "" {
		t.Fatal("empty secret must disable the token")
	}
}

func TestInstanceEnv(t *testing.T) {
	cfg := testConfig(t, "s3cret")
	svc := New(cfg, nil, nil, nil, nil, logging.Discard())

	inst := &store.Instance{
		ID:        "i1",
		Subdomain: "demo",
		Dev:       true,
		SyncAdmin: true,
		Secrets: map[string]string{
			"API_KEY":           "value",
			"PB_ENCRYPTION_KEY": "enc",
			"bad-key":           "ignored",
		},
	}
	owner := &store.User{ID: "u1", Email: "o@example.com", TokenKey: "tk", PasswordHash: "ph"}

	env := svc.instanceEnv(inst, owner, logging.Discard())

	if env["PH_APEX_DOMAIN"] != "pockethost.io" {
		t.Fatalf("apex = %q", env["PH_APEX_DOMAIN"])
	}
	if env["DEV"] != "true" {
		t.Fatalf("DEV = %q", env["DEV"])
	}
	if env["API_KEY"] != "value" || env["PB_ENCRYPTION_KEY"] != "enc" {
		t.Fatalf("secrets missing: %#v", env)
	}
	if _, bad := env["bad-key"]; bad {
		t.Fatal("invalid secret key was forwarded")
	}
	if env["PH_BACKUP_TOKEN"] != BackupToken("i1", "s3cret") {
		t.Fatal("backup token mismatch")
	}

	var payload adminSyncPayload
	if err := json.Unmarshal([]byte(env["ADMIN_SYNC"]), &payload); err != nil {
		t.Fatalf("ADMIN_SYNC: %v", err)
	}
	if payload.Email != "o@example.com" || payload.PasswordHash != "ph" || payload.TokenKey != "tk" {
		t.Fatalf("ADMIN_SYNC = %#v", payload)
	}

	// syncAdmin=false must omit the credentials entirely.
	inst.SyncAdmin = false
	env = svc.instanceEnv(inst, owner, logging.Discard())
	if _, ok := env["ADMIN_SYNC"]; ok {
		t.Fatal("ADMIN_SYNC should be omitted when syncAdmin is false")
	}
}

func TestIdleTTL(t *testing.T) {
	cfg := testConfig(t, "")
	cfg.DaemonPBIdleTTL = 5_000_000_000 // 5s

	inst := &store.Instance{}
	if got := idleTTL(inst, cfg); got.Seconds() != 5 {
		t.Fatalf("default ttl = %v", got)
	}
	inst.IdleTTL = 60_000 // 60s in ms
	if got := idleTTL(inst, cfg); got.Seconds() != 60 {
		t.Fatalf("instance ttl = %v", got)
	}
}

func TestUserErrorMapping(t *testing.T) {
	err := UserErr("nope")
	if !IsUserError(err) {
		t.Fatal("expected user error")
	}
	if StatusCode(err) != 503 {
		t.Fatalf("status = %d", StatusCode(err))
	}
	paywalled := &UserError{Msg: "upgrade", Status: 402}
	if StatusCode(paywalled) != 402 {
		t.Fatalf("status = %d", StatusCode(paywalled))
	}
	if StatusCode(errorsNew("boom")) != 0 {
		t.Fatal("system errors must not map to a user status")
	}
}

func errorsNew(s string) error { return &plainError{s} }

type plainError struct{ s string }

func (e *plainError) Error() string { return e.s }
