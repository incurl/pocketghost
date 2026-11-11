package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/incurl/pocketghost/internal/store"
)

func TestSignupAndInstanceLifecycle(t *testing.T) {
	e := newTestEnv(t)
	token := e.signup(t, "user@example.com", "supersecret1")

	rec := e.do(t, http.MethodPost, "/api/instance", map[string]string{"subdomain": "alpha"}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Instance store.Instance `json:"instance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Instance.Subdomain != "alpha" || !created.Instance.Power {
		t.Fatalf("instance = %+v", created.Instance)
	}
	if len(created.Instance.ID) != store.IDLength {
		t.Fatalf("id = %q", created.Instance.ID)
	}

	// Second instance exceeds the free plan slot.
	rec = e.do(t, http.MethodPost, "/api/instance", map[string]string{"subdomain": "beta"}, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected plan limit, got %d %s", rec.Code, rec.Body.String())
	}

	// Reserved name.
	rec = e.do(t, http.MethodPost, "/api/instance", map[string]string{"subdomain": "admin"}, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected reserved name rejection, got %d", rec.Code)
	}

	// List with expand.
	rec = e.do(t, http.MethodGet, "/api/collections/instances/records?expand=uid", nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list ListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.TotalItems != 1 {
		t.Fatalf("total = %d", list.TotalItems)
	}

	// The on-disk layout exists immediately after creation.
	if _, err := e.repo.GetInstance(t.Context(), created.Instance.ID); err != nil {
		t.Fatalf("instance disappeared: %v", err)
	}

	// Power off, then delete.
	rec = e.do(t, http.MethodPut, "/api/instance/"+created.Instance.ID, map[string]any{
		"id":     created.Instance.ID,
		"fields": map[string]any{"power": false},
	}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("power off: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, http.MethodDelete, "/api/instance/"+created.Instance.ID, nil, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.repo.GetInstance(t.Context(), created.Instance.ID); err != store.ErrNotFound {
		t.Fatalf("expected deletion, got %v", err)
	}
}

func TestInstanceRequiresAuth(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodPost, "/api/instance", map[string]string{"subdomain": "nope"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	rec = e.do(t, http.MethodGet, "/api/collections/instances/records", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestWrongPassword(t *testing.T) {
	e := newTestEnv(t)
	e.signup(t, "pw@example.com", "supersecret1")
	rec := e.do(t, http.MethodPost, "/api/collections/users/auth-with-password", map[string]string{
		"identity": "pw@example.com", "password": "wrongpassword",
	}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSuperuserAuthAndOperatorGuard(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, http.MethodPost, "/api/collections/_superusers/auth-with-password", map[string]string{
		"identity": "admin@test.local", "password": "adminpass",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("superuser auth: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodGet, "/_api/daemon/processes", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/_api/daemon/version", nil)
	req.Header.Set("X-PocketHost-Secret", "test-secret")
	rec2 := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("version: %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestVersionsAndStats(t *testing.T) {
	e := newTestEnv(t)
	if rec := e.do(t, http.MethodGet, "/api/versions", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("versions: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodGet, "/stats.json", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSignupEndpoint(t *testing.T) {
	e := newTestEnv(t)
	var check struct {
		Available bool `json:"available"`
	}
	rec := e.do(t, http.MethodGet, "/api/signup?instanceName=mysite", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("check: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &check)
	if !check.Available {
		t.Fatal("expected name to be available")
	}

	rec = e.do(t, http.MethodPost, "/api/signup", map[string]string{
		"email": "signup@example.com", "password": "supersecret1", "instanceName": "mysite",
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, http.MethodGet, "/api/signup?instanceName=mysite", nil, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &check)
	if check.Available {
		t.Fatal("expected name to be taken after signup")
	}
}
