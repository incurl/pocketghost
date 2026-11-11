package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/incurl/pocketghost/internal/config"
	"github.com/incurl/pocketghost/internal/logging"
	"github.com/incurl/pocketghost/internal/pbbin"
	"github.com/incurl/pocketghost/internal/store"
)

type testEnv struct {
	srv  *Server
	repo *store.Repo
	cfg  *config.Config
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PH_HOME", home)
	t.Setenv("DATA_ROOT", filepath.Join(home, "data"))
	t.Setenv("NODE_ENV", "development")
	t.Setenv("APEX_DOMAIN", "pockethost.io")
	t.Setenv("PH_SECRET", "test-secret")
	t.Setenv("MOTHERSHIP_ADMIN_USERNAME", "admin@test.local")
	t.Setenv("MOTHERSHIP_ADMIN_PASSWORD", "adminpass")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	db, err := store.Open(filepath.Join(home, "meta.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewRepo(db)

	logs := logging.NewLogStore(cfg.InstancesRoot(), 0, logging.Discard())
	bins := pbbin.New(cfg.PocketBaseRoot, logging.Discard())

	srv, err := New(Options{
		Config:   cfg,
		Repo:     repo,
		Binaries: bins,
		Logs:     logs,
		Logger:   logging.Discard(),
	})
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	return &testEnv{srv: srv, repo: repo, cfg: cfg}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Host = "app.pockethost.io"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) signup(t *testing.T, email, password string) string {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/collections/users/records", map[string]string{
		"email": email, "password": password, "passwordConfirm": password,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodPost, "/api/collections/users/auth-with-password", map[string]string{
		"identity": email, "password": password,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("auth: %d %s", rec.Code, rec.Body.String())
	}
	var out authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("empty token")
	}
	return out.Token
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodGet, "/_api/daemon/health", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestSubdomainValidation(t *testing.T) {
	cases := map[string]bool{
		"abc":        true,
		"my-site":    true,
		"a":          false,
		"-bad":       false,
		"bad-":       false,
		"UPPER":      false,
		"admin":      false,
		"mothership": false,
	}
	for name, ok := range cases {
		if got := instanceNameError(name) == ""; got != ok {
			t.Errorf("instanceNameError(%q) valid=%v, want %v", name, got, ok)
		}
	}
}
