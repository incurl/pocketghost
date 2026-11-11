package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleUserView(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	id := r.PathValue("id")
	if id == "" || id == u.ID {
		writeJSON(w, http.StatusOK, u)
		return
	}
	if !s.isSuperuser(r) {
		writeError(w, http.StatusForbidden, "Only the record owner can view this record.")
		return
	}
	target, err := s.repo.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "The requested resource wasn't found.")
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	if id := r.PathValue("id"); id != "" && id != u.ID {
		writeError(w, http.StatusForbidden, "Only the record owner can update this record.")
		return
	}

	var body struct {
		Name            *string `json:"name"`
		Avatar          *string `json:"avatar"`
		EmailVisibility *bool   `json:"emailVisibility"`
		Email           *string `json:"email"`
		OldPassword     string  `json:"oldPassword"`
		Password        string  `json:"password"`
		PasswordConfirm string  `json:"passwordConfirm"`
		Unsubscribe     *bool   `json:"unsubscribe"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if body.Name != nil {
		u.Name = *body.Name
	}
	if body.Avatar != nil {
		u.Avatar = *body.Avatar
	}
	if body.EmailVisibility != nil {
		u.EmailVisibility = *body.EmailVisibility
	}
	if body.Unsubscribe != nil {
		u.Unsubscribe = *body.Unsubscribe
	}
	if body.Email != nil && *body.Email != "" && !strings.EqualFold(*body.Email, u.Email) {
		// Direct email writes are rejected: the dashboard uses the two-step
		// request-email-change / confirm-email-change flow, like PocketBase.
		writeValidationError(w, map[string]any{
			"email": map[string]string{"code": "validation_invalid", "message": "Use the email change flow."},
		})
		return
	}
	if body.Password != "" {
		if !verifyPassword(u.PasswordHash, body.OldPassword) {
			writeValidationError(w, map[string]any{
				"oldPassword": map[string]string{"code": "validation_invalid", "message": "Invalid password."},
			})
			return
		}
		if body.Password != body.PasswordConfirm {
			writeValidationError(w, map[string]any{
				"passwordConfirm": map[string]string{"code": "validation_values_mismatch", "message": "Values don't match."},
			})
			return
		}
		hash, err := hashPassword(body.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Password hashing failed")
			return
		}
		u.PasswordHash = hash
	}

	if err := s.repo.UpdateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}
