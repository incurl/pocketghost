package api

import (
	"net/http"
	"strings"

	"github.com/incurl/pocketghost/internal/store"
)

// authResponse is PocketBase's auth envelope.
type authResponse struct {
	Token  string `json:"token"`
	Record any    `json:"record"`
	Meta   any    `json:"meta,omitempty"`
}

func (s *Server) handleUserAuth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identity string `json:"identity"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	identity := body.Identity
	if identity == "" {
		identity = body.Email
	}
	if identity == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "An identity and password are required.")
		return
	}

	u, err := s.repo.GetUserByEmail(r.Context(), identity)
	if err != nil || !verifyPassword(u.PasswordHash, body.Password) {
		writeError(w, http.StatusBadRequest, "Failed to authenticate.")
		return
	}

	token, err := s.signToken(u.ID, store.CollectionIDUsers, "auth", TokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Token generation failed")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, Record: u})
}

func (s *Server) handleUserAuthRefresh(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	token, err := s.signToken(u.ID, store.CollectionIDUsers, "auth", TokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Token generation failed")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, Record: u})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email           string `json:"email"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"passwordConfirm"`
		Name            string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	fields := map[string]any{}
	if !strings.Contains(body.Email, "@") {
		fields["email"] = map[string]string{"code": "validation_invalid_email", "message": "Must be a valid email address."}
	}
	if len(body.Password) < 8 {
		fields["password"] = map[string]string{"code": "validation_length_out_of_range", "message": "Must be at least 8 characters."}
	}
	if body.Password != body.PasswordConfirm {
		fields["passwordConfirm"] = map[string]string{"code": "validation_values_mismatch", "message": "Values don't match."}
	}
	if len(fields) > 0 {
		writeValidationError(w, fields)
		return
	}

	if _, err := s.repo.GetUserByEmail(r.Context(), body.Email); err == nil {
		writeValidationError(w, map[string]any{
			"email": map[string]string{"code": "validation_not_unique", "message": "The email is already in use."},
		})
		return
	}

	hash, err := hashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Password hashing failed")
		return
	}
	u := &store.User{
		Email:           body.Email,
		Name:            body.Name,
		Username:        "users" + store.NewID()[:6],
		TokenKey:        randomTokenKey(),
		PasswordHash:    hash,
		Subscription:    store.SubscriptionFree,
		EmailVisibility: true,
	}
	// Plan default: one free running instance slot, matching the signup hook.
	u.SubscriptionQuantity = 1

	if err := s.repo.CreateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleSuperuserAuth(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identity string `json:"identity"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	user := s.cfg.MothershipAdminUsername
	pass := s.cfg.MothershipAdminPassword
	if user == "" {
		user = "admin@pockethost.local"
	}
	if pass == "" {
		pass = s.cfg.Secret
	}
	if pass == "" || body.Identity != user || body.Password != pass {
		writeError(w, http.StatusBadRequest, "Failed to authenticate.")
		return
	}
	token, err := s.signToken("superuser", "_superusers", "auth", TokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Token generation failed")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		Record: map[string]any{
			"id":             "superuser",
			"collectionId":   "_superusers",
			"collectionName": "_superusers",
			"email":          user,
		},
	})
}
