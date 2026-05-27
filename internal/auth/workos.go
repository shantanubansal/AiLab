// WorkOS verifier — validates a WorkOS-issued JWT and maps its claims to a
// platform Identity. WorkOS Organizations map 1:1 to platform tenants; the
// org_id claim becomes Identity.TenantID.
//
// JWKS is fetched and cached with auto-refresh every 15 minutes — a key
// rotation by WorkOS picks up at most one cycle later.

package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// WorkOSVerifier validates JWTs issued by WorkOS.
type WorkOSVerifier struct {
	cache    *jwk.Cache
	jwksURL  string
	audience string // optional: when set, "aud" must contain it
}

// NewWorkOSVerifier builds a verifier that fetches JWKS from jwksURL and
// validates standard claims plus the optional audience. The cache is
// primed synchronously; subsequent refreshes happen in the background.
func NewWorkOSVerifier(ctx context.Context, jwksURL, audience string) (*WorkOSVerifier, error) {
	if jwksURL == "" {
		return nil, errors.New("workos: jwksURL is required")
	}
	c := jwk.NewCache(ctx)
	if err := c.Register(jwksURL, jwk.WithMinRefreshInterval(15*time.Minute)); err != nil {
		return nil, fmt.Errorf("register jwks: %w", err)
	}
	primeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := c.Refresh(primeCtx, jwksURL); err != nil {
		return nil, fmt.Errorf("prime jwks: %w", err)
	}
	return &WorkOSVerifier{cache: c, jwksURL: jwksURL, audience: audience}, nil
}

// Verify parses and validates the token, returning the platform Identity.
func (v *WorkOSVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	set, err := v.cache.Get(ctx, v.jwksURL)
	if err != nil {
		return Identity{}, fmt.Errorf("jwks fetch: %w", err)
	}

	parseOpts := []jwt.ParseOption{
		jwt.WithKeySet(set),
		jwt.WithValidate(true),
		jwt.WithAcceptableSkew(60 * time.Second),
	}
	if v.audience != "" {
		parseOpts = append(parseOpts, jwt.WithAudience(v.audience))
	}

	tok, err := jwt.Parse([]byte(token), parseOpts...)
	if err != nil {
		return Identity{}, ErrUnauthorized
	}

	tenantID, _ := readStringClaim(tok, "org_id")
	if tenantID == "" {
		// Fallbacks for environments where the org claim is named differently;
		// keeps the verifier useful before WorkOS Organizations are fully wired.
		tenantID, _ = readStringClaim(tok, "organization_id")
	}
	if tenantID == "" {
		return Identity{}, ErrUnauthorized
	}

	userID := tok.Subject()
	if userID == "" {
		return Identity{}, ErrUnauthorized
	}

	return Identity{TenantID: tenantID, UserID: userID}, nil
}

func readStringClaim(tok jwt.Token, name string) (string, bool) {
	v, ok := tok.Get(name)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
