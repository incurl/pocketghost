package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/store"
)

// mailLink records an outbound action link. The original deployment sent these
// through the mothership mail handler (SMTP/SES). Native mode logs the link and
// hands it to the configured notifier, so an operator can still complete flows
// without an SMTP dependency on the edge node.
func (s *Server) mailLink(r *http.Request, to, kind, token string) {
	base := strings.TrimSuffix(s.cfg.AppURL, "/")
	var link string
	switch kind {
	case "verification":
		link = base + "/login/confirm-account/" + token
	case "password-reset":
		link = base + "/login/password-reset/confirm/" + token
	case "confirm-email-change":
		link = base + "/login/confirm-email-change/" + token
	default:
		link = base + "/" + kind + "/" + token
	}
	s.log.Infof("mail(%s) to=%s link=%s", kind, to, link)
	if s.mailer != nil {
		s.mailer.Send(Mail{To: to, Kind: kind, Link: link})
	}
}

// Mail is one outbound transactional message.
type Mail struct {
	To   string
	Kind string
	Link string
}

// Mailer delivers transactional mail (SMTP, SES, log...).
type Mailer interface {
	Send(m Mail)
}

func (s *Server) handleRequestVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	_ = decodeJSON(r, &body)

	if u, err := s.repo.GetUserByEmail(r.Context(), body.Email); err == nil && !u.Verified {
		if tok, err := s.signToken(u.ID, store.CollectionIDUsers, "verification", time.Hour); err == nil {
			s.mailLink(r, u.Email, "verification", tok)
		}
	}
	// Always 204: never disclose whether an address exists.
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfirmVerification(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	c, err := s.parseToken(body.Token)
	if err != nil || c.Type != "verification" {
		writeError(w, http.StatusBadRequest, "Invalid or expired verification token.")
		return
	}
	u, err := s.repo.GetUser(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid or expired verification token.")
		return
	}
	u.Verified = true
	if err := s.repo.UpdateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	_ = decodeJSON(r, &body)
	if u, err := s.repo.GetUserByEmail(r.Context(), body.Email); err == nil {
		if tok, err := s.signToken(u.ID, store.CollectionIDUsers, "passwordReset", time.Hour); err == nil {
			s.mailLink(r, u.Email, "password-reset", tok)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token           string `json:"token"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"passwordConfirm"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	c, err := s.parseToken(body.Token)
	if err != nil || c.Type != "passwordReset" {
		writeError(w, http.StatusBadRequest, "Invalid or expired password reset token.")
		return
	}
	if len(body.Password) < 8 || body.Password != body.PasswordConfirm {
		writeValidationError(w, map[string]any{
			"password": map[string]string{"code": "validation_length_out_of_range", "message": "Must be at least 8 characters."},
		})
		return
	}
	u, err := s.repo.GetUser(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid or expired password reset token.")
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Password hashing failed")
		return
	}
	u.PasswordHash = hash
	u.TokenKey = randomTokenKey()
	if err := s.repo.UpdateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRequestEmailChange(w http.ResponseWriter, r *http.Request) {
	u := s.userFromRequest(r)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
		return
	}
	var body struct {
		NewEmail string `json:"newEmail"`
	}
	if err := decodeJSON(r, &body); err != nil || !strings.Contains(body.NewEmail, "@") {
		writeError(w, http.StatusBadRequest, "A valid new email is required.")
		return
	}
	tok, err := s.signToken(u.ID, store.CollectionIDUsers, "emailChange:"+strings.ToLower(body.NewEmail), time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Token generation failed")
		return
	}
	s.mailLink(r, body.NewEmail, "confirm-email-change", tok)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	c, err := s.parseToken(body.Token)
	if err != nil || !strings.HasPrefix(c.Type, "emailChange:") {
		writeError(w, http.StatusBadRequest, "Invalid or expired email change token.")
		return
	}
	u, err := s.repo.GetUser(r.Context(), c.ID)
	if err != nil || !verifyPassword(u.PasswordHash, body.Password) {
		writeError(w, http.StatusBadRequest, "Invalid or expired email change token.")
		return
	}
	u.Email = strings.TrimPrefix(c.Type, "emailChange:")
	u.Verified = false
	if err := s.repo.UpdateUser(r.Context(), u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
