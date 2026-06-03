package runtime

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// hs256Verifier builds an HS256 JWTVerifier for tests (shared with server_test.go).
func hs256Verifier(secret string) *JWTVerifier {
	return &JWTVerifier{
		parser: jwt.NewParser(jwt.WithExpirationRequired(), jwt.WithValidMethods([]string{"HS256"})),
		keyFunc: func(token *jwt.Token) (any, error) {
			return []byte(secret), nil
		},
	}
}

func signHS256(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestEffectiveUserIDPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		claims jwt.MapClaims
		want   string
	}{
		{"sub wins over all", jwt.MapClaims{"sub": "u-sub", "email": "e@x.com", "preferred_username": "puser"}, "u-sub"},
		{"email when no sub", jwt.MapClaims{"email": "e@x.com", "preferred_username": "puser"}, "e@x.com"},
		{"preferred_username last", jwt.MapClaims{"preferred_username": "puser"}, "puser"},
		{"none present", jwt.MapClaims{"name": "no identifier"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveUserID(tc.claims); got != tc.want {
				t.Fatalf("effectiveUserID=%q want %q", got, tc.want)
			}
		})
	}
}

func TestJWTVerifierAcceptsValidToken(t *testing.T) {
	v := hs256Verifier("secret-key")
	token := signHS256(t, "secret-key", jwt.MapClaims{"sub": "alice@corp.com", "exp": time.Now().Add(time.Hour).Unix()})
	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if id.UserID != "alice@corp.com" || !id.Verified {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestJWTVerifierRejectsExpired(t *testing.T) {
	v := hs256Verifier("secret-key")
	token := signHS256(t, "secret-key", jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(-time.Minute).Unix()})
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestJWTVerifierRejectsBadSignature(t *testing.T) {
	v := hs256Verifier("secret-key")
	token := signHS256(t, "WRONG-key", jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected bad signature to be rejected")
	}
}

func TestJWTVerifierRejectsNoneAlg(t *testing.T) {
	// Forge an unsigned token: alg=none, plausible claims, empty signature.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker","exp":9999999999}`))
	token := header + "." + payload + "."
	v := hs256Verifier("secret-key")
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected alg=none (unsigned) token to be rejected")
	}
}

func TestJWTVerifierRejectsMissingExpiry(t *testing.T) {
	v := hs256Verifier("secret-key")
	token := signHS256(t, "secret-key", jwt.MapClaims{"sub": "alice"}) // no exp
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected token without exp to be rejected (WithExpirationRequired)")
	}
}

func TestResolveEffectiveIdentity(t *testing.T) {
	v := hs256Verifier("secret-key")
	valid := signHS256(t, "secret-key", jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})

	// 1. token path -> verified identity, demo user ignored
	id, err := ResolveEffectiveIdentity(context.Background(), v, false, valid, "ignored")
	if err != nil || id.UserID != "alice" || !id.Verified {
		t.Fatalf("token path: id=%+v err=%v", id, err)
	}
	// 2. no token + demo on -> demo user (unverified)
	id, err = ResolveEffectiveIdentity(context.Background(), v, true, "", "demo_user")
	if err != nil || id.UserID != "demo_user" || id.Verified {
		t.Fatalf("demo path: id=%+v err=%v", id, err)
	}
	// 3. no token + demo off -> fail closed
	if _, err := ResolveEffectiveIdentity(context.Background(), v, false, "", "demo_user"); err == nil {
		t.Fatal("expected fail closed without token and demo off")
	}
	// 4. token present but no verifier configured -> fail closed
	if _, err := ResolveEffectiveIdentity(context.Background(), nil, true, valid, ""); err == nil {
		t.Fatal("expected fail closed when token present but no verifier")
	}
}
