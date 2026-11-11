package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/incurl/pocketghost/internal/store"
)

type ctxKey string

const (
	ctxUserKey      ctxKey = "user"
	ctxSuperuserKey ctxKey = "superuser"
)

// claims is the JWT payload PocketBase clients expect.
type claims struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	CollectionID string `json:"collectionId"`
	Refreshable  bool   `json:"refreshable"`
	Exp          int64  `json:"exp"`
	Iat          int64  `json:"iat"`
}

// TokenTTL matches PocketBase's default 14-day user auth token duration.
const TokenTTL = 14 * 24 * time.Hour

// signToken creates an HS256 JWT.
func (s *Server) signToken(id, collectionID, typ string, ttl time.Duration) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	now := time.Now()
	payload := claims{
		ID:           id,
		Type:         typ,
		CollectionID: collectionID,
		Refreshable:  true,
		Iat:          now.Unix(),
		Exp:          now.Add(ttl).Unix(),
	}
	hb, _ := json.Marshal(header)
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signing := b64(hb) + "." + b64(pb)
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(signing))
	return signing + "." + b64(mac.Sum(nil)), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// parseToken verifies an HS256 JWT and returns its claims.
func (s *Server) parseToken(raw string) (*claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	signing := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(signing))
	want := b64(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[2])) != 1 {
		return nil, errors.New("invalid token signature")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c claims
	if err := json.Unmarshal(rawPayload, &c); err != nil {
		return nil, err
	}
	if c.Exp > 0 && time.Now().Unix() > c.Exp {
		return nil, errors.New("token expired")
	}
	return &c, nil
}

// bearerToken extracts the Authorization bearer token.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(h)
}

// userFromRequest returns the authenticated user, if any.
func (s *Server) userFromRequest(r *http.Request) *store.User {
	if u, ok := r.Context().Value(ctxUserKey).(*store.User); ok {
		return u
	}
	tok := bearerToken(r)
	if tok == "" {
		return nil
	}
	c, err := s.parseToken(tok)
	if err != nil || c.Type != "auth" || c.CollectionID != store.CollectionIDUsers {
		return nil
	}
	u, err := s.repo.GetUser(r.Context(), c.ID)
	if err != nil {
		return nil
	}
	return u
}

// requireUser rejects unauthenticated requests with PocketBase's 401 shape.
func (s *Server) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.userFromRequest(r)
		if u == nil {
			writeError(w, http.StatusUnauthorized, "The request requires valid record authorization token.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey, u)))
	}
}

// isSuperuser reports whether the request carries a superuser token.
func (s *Server) isSuperuser(r *http.Request) bool {
	if v, ok := r.Context().Value(ctxSuperuserKey).(bool); ok && v {
		return true
	}
	tok := bearerToken(r)
	if tok == "" {
		return false
	}
	c, err := s.parseToken(tok)
	return err == nil && c.Type == "auth" && c.CollectionID == "_superusers"
}

// requireSuperuser rejects non-superuser requests.
func (s *Server) requireSuperuser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isSuperuser(r) {
			writeError(w, http.StatusUnauthorized, "The request requires superuser authorization token.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxSuperuserKey, true)))
	}
}

// hashPassword produces a bcrypt hash compatible with PocketBase's stored format.
func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// verifyPassword accepts a bcrypt hash or PocketBase's plain `$2a$...` form.
func verifyPassword(hash, pw string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// randomTokenKey mirrors PocketBase's 50-char tokenKey.
func randomTokenKey() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, 50)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("tk%d", time.Now().UnixNano())
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf)
}
