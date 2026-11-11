package store

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"time"
)

// IDAlphabet is PocketBase's record id alphabet (`[a-z0-9]{15}`).
const IDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// IDLength is PocketBase's default record id length.
const IDLength = 15

// NewID generates a PocketBase-compatible record id.
func NewID() string {
	b := make([]byte, IDLength)
	max := big.NewInt(int64(len(IDAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failure is unrecoverable; fall back to time-based
			// bytes so the caller still gets a unique-ish id.
			b[i] = IDAlphabet[(time.Now().UnixNano()>>uint(i*3))%int64(len(IDAlphabet))]
			continue
		}
		b[i] = IDAlphabet[n.Int64()]
	}
	return string(b)
}

// PBTimeLayout is PocketBase's stored datetime format.
const PBTimeLayout = "2006-01-02 15:04:05.000Z"

// NowString returns the current UTC time in PocketBase storage format.
func NowString() string { return time.Now().UTC().Format(PBTimeLayout) }

// FormatTime converts a time to PocketBase storage format.
func FormatTime(t time.Time) string { return t.UTC().Format(PBTimeLayout) }

// ParseTime parses a PocketBase datetime, tolerating RFC3339 as well.
func ParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(PBTimeLayout, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02 15:04:05.000Z07:00", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// boolInt converts a Go bool to SQLite's 0/1.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// MarshalJSONColumn encodes a value as a JSON text column, defaulting to "null".
func MarshalJSONColumn(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// UnmarshalJSONColumn is the inverse of MarshalJSONColumn.
func UnmarshalJSONColumn[T any](raw string) *T {
	if raw == "" || raw == "null" {
		return nil
	}
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return &out
}
