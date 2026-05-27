// Package auth verifies bearer tokens and extracts the platform's
// (tenantId, userId) identity. v1 uses WorkOS-issued JWTs; in dev mode
// (WORKOS_JWKS_URL unset) we accept tokens of the form "dev:<tenantId>:<userId>"
// so contributors can iterate without WorkOS configured.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ctxKey is a private type for context keys to avoid collisions.
type ctxKey int

const identityKey ctxKey = 1

// Identity is the authenticated principal for the current request.
type Identity struct {
	TenantID string
	UserID   string
}

// FromContext returns the Identity attached to ctx by Middleware. The second
// return is false when the request was not authenticated (should never happen
// for handlers behind Middleware).
func FromContext(ctx context.Context) (Identity, bool) {
	v, ok := ctx.Value(identityKey).(Identity)
	return v, ok
}

// Verifier resolves a bearer token to an Identity. Real implementation lives
// in a workos verifier; dev mode uses DevVerifier.
type Verifier interface {
	Verify(ctx context.Context, token string) (Identity, error)
}

// ErrUnauthorized signals the token was missing or invalid.
var ErrUnauthorized = errors.New("unauthorized")

// DevVerifier accepts tokens shaped like "dev:<tenantId>:<userId>". Never enable in production.
type DevVerifier struct{}

func (DevVerifier) Verify(_ context.Context, token string) (Identity, error) {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 || parts[0] != "dev" || parts[1] == "" || parts[2] == "" {
		return Identity{}, ErrUnauthorized
	}
	return Identity{TenantID: parts[1], UserID: parts[2]}, nil
}

// Middleware wraps an http.Handler with bearer-token authentication. The
// handler chain runs only when verification succeeds; otherwise 401 is returned.
func Middleware(v Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			tok := strings.TrimPrefix(h, "Bearer ")
			ident, err := v.Verify(r.Context(), tok)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, ident)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
