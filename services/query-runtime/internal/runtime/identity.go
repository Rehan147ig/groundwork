package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ErrIdentityMissing means no end-user identity assertion was supplied and demo
// identity is not enabled. Callers MUST fail closed.
var ErrIdentityMissing = errors.New("verified end-user identity required")

// ErrIdentityInvalid means an identity assertion was supplied but failed
// verification (bad signature, expired, wrong issuer/audience, unsigned "none"
// algorithm, or missing a usable subject claim). Callers MUST fail closed.
var ErrIdentityInvalid = errors.New("invalid end-user identity assertion")

// Identity is the verified end-user on whose behalf a query runs. UserID is the
// effective identifier Groundwork checks against document permissions. In
// production it is derived from a cryptographically verified token — never from
// the request body.
type Identity struct {
	UserID   string
	Subject  string // JWT "sub" (IdP+app-scoped for Entra; stable subject for generic OIDC)
	OID      string // JWT "oid" (Entra directory object id; matches the Graph user id)
	Email    string
	Username string
	Issuer   string
	// EmailVerified mirrors the OIDC "email_verified" claim; email / preferred_username are
	// only treated as verified identity aliases for principal resolution when this is true.
	EmailVerified bool
	// Verified is true only when UserID came from a validated signed assertion.
	// Demo identities (ALLOW_DEMO_IDENTITY=true) are reported with Verified=false.
	Verified bool
}

// IdentityVerifier validates a signed end-user identity assertion (a JWT) and
// returns the verified Identity. Implementations MUST reject unsigned ("none"),
// tampered, and expired tokens.
type IdentityVerifier interface {
	Verify(ctx context.Context, token string) (Identity, error)
}

// JWTVerifier verifies RS256/HS256 JWTs against a configured key, enforcing
// expiry and (optionally) issuer/audience. It rejects the "none" algorithm and
// algorithm-confusion attacks via an allow-list of signing methods.
type JWTVerifier struct {
	parser  *jwt.Parser
	keyFunc jwt.Keyfunc
}

// Verify parses and validates the token, returning the effective Identity.
func (v *JWTVerifier) Verify(_ context.Context, token string) (Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Identity{}, ErrIdentityMissing
	}
	claims := jwt.MapClaims{}
	parsed, err := v.parser.ParseWithClaims(token, claims, v.keyFunc)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrIdentityInvalid, err)
	}
	if !parsed.Valid {
		return Identity{}, fmt.Errorf("%w: token is not valid", ErrIdentityInvalid)
	}
	userID := effectiveUserID(claims)
	if userID == "" {
		return Identity{}, fmt.Errorf("%w: token has no sub/email/preferred_username claim", ErrIdentityInvalid)
	}
	return Identity{
		UserID:        userID,
		Subject:       claimString(claims, "sub"),
		OID:           claimString(claims, "oid"),
		Email:         claimString(claims, "email"),
		Username:      claimString(claims, "preferred_username"),
		Issuer:        claimString(claims, "iss"),
		EmailVerified: claimBool(claims, "email_verified"),
		Verified:      true,
	}, nil
}

// effectiveUserID resolves the effective user identifier from standard OIDC
// claims, in priority order: sub, then email, then preferred_username.
func effectiveUserID(claims jwt.MapClaims) string {
	for _, key := range []string{"sub", "email", "preferred_username"} {
		if value := claimString(claims, key); value != "" {
			return value
		}
	}
	return ""
}

func claimString(claims jwt.MapClaims, key string) string {
	if value, ok := claims[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func claimBool(claims jwt.MapClaims, key string) bool {
	switch v := claims[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// BuildIdentityVerifier constructs an IdentityVerifier from environment config:
//
//	GROUNDWORK_JWT_HS_SECRET           HMAC (HS256) shared secret
//	GROUNDWORK_JWT_RS_PUBLIC_KEY       RSA (RS256) public key, PEM
//	GROUNDWORK_JWT_RS_PUBLIC_KEY_FILE  path to an RSA public key PEM file
//	GROUNDWORK_JWT_ISSUER              optional: required token issuer (iss)
//	GROUNDWORK_JWT_AUDIENCE            optional: required token audience (aud)
//
// Returns (nil, nil) when no verification key is configured; with no verifier and
// ALLOW_DEMO_IDENTITY!=true the runtime fails closed on every query. An OIDC
// deployment would plug a JWKS-backed verifier in here without touching callers.
func BuildIdentityVerifier() (IdentityVerifier, error) {
	parserOpts := []jwt.ParserOption{jwt.WithExpirationRequired()}
	if issuer := strings.TrimSpace(os.Getenv("GROUNDWORK_JWT_ISSUER")); issuer != "" {
		parserOpts = append(parserOpts, jwt.WithIssuer(issuer))
	}
	if audience := strings.TrimSpace(os.Getenv("GROUNDWORK_JWT_AUDIENCE")); audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(audience))
	}

	if secret := os.Getenv("GROUNDWORK_JWT_HS_SECRET"); secret != "" {
		key := []byte(secret)
		parserOpts = append(parserOpts, jwt.WithValidMethods([]string{"HS256"}))
		return &JWTVerifier{
			parser: jwt.NewParser(parserOpts...),
			keyFunc: func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
				}
				return key, nil
			},
		}, nil
	}

	pemData, err := rsaPublicKeyPEM()
	if err != nil {
		return nil, err
	}
	if pemData != nil {
		pub, err := jwt.ParseRSAPublicKeyFromPEM(pemData)
		if err != nil {
			return nil, fmt.Errorf("parse RSA public key: %w", err)
		}
		parserOpts = append(parserOpts, jwt.WithValidMethods([]string{"RS256"}))
		return &JWTVerifier{
			parser: jwt.NewParser(parserOpts...),
			keyFunc: func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
					return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
				}
				return pub, nil
			},
		}, nil
	}

	return nil, nil
}

func rsaPublicKeyPEM() ([]byte, error) {
	if inline := strings.TrimSpace(os.Getenv("GROUNDWORK_JWT_RS_PUBLIC_KEY")); inline != "" {
		return []byte(inline), nil
	}
	if path := strings.TrimSpace(os.Getenv("GROUNDWORK_JWT_RS_PUBLIC_KEY_FILE")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read RSA public key file: %w", err)
		}
		return data, nil
	}
	return nil, nil
}

// ResolveEffectiveIdentity centralizes the fail-closed identity decision shared by
// every transport (REST, MCP). Precedence:
//
//  1. A signed assertion token — always verified; any failure fails closed.
//  2. A demo identity (demoUserID) — honored ONLY when allowDemo is true.
//  3. Otherwise fail closed with ErrIdentityMissing.
//
// tenant_id/region are NEVER derived here — they come only from the Groundwork API key.
func ResolveEffectiveIdentity(ctx context.Context, verifier IdentityVerifier, allowDemo bool, token, demoUserID string) (Identity, error) {
	if token = strings.TrimSpace(token); token != "" {
		if verifier == nil {
			return Identity{}, fmt.Errorf("%w: no identity verifier configured", ErrIdentityInvalid)
		}
		return verifier.Verify(ctx, token)
	}
	if allowDemo {
		if demoUserID = strings.TrimSpace(demoUserID); demoUserID == "" {
			return Identity{}, fmt.Errorf("%w: demo identity enabled but no user_id supplied", ErrIdentityMissing)
		}
		return Identity{UserID: demoUserID, Subject: demoUserID, Verified: false}, nil
	}
	return Identity{}, ErrIdentityMissing
}
