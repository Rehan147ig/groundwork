package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"

	"github.com/golang-jwt/jwt/v5"
)

func newTestEngine() *engine.Engine {
	backend := runtime.NewMemoryBackend()
	return &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        500 * time.Millisecond,
			QdrantSearch: 100 * time.Millisecond,
			OpenFGACheck: 150 * time.Millisecond,
			AuditWrite:   50 * time.Millisecond,
		},
		Backend: engine.VectorRetrievalClient{Vector: backend.Vector},
		ACL:     backend.ACL,
		Auditor: engine.RuntimeTraceAuditWriter{Trace: backend.Trace},
	}
}

func signMCP(t *testing.T, secret, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()})
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

const aclQuestion = "How do live ACL checks fail closed?"

// MCP must derive the effective user from the verified token, ignoring a forged
// raw user_id. finance_user is authorized for the sharepoint-policy document, so
// the document being returned proves the token identity (not "attacker") was used.
func TestMCPUsesVerifiedIdentityIgnoringForgedUserID(t *testing.T) {
	t.Setenv("GROUNDWORK_JWT_HS_SECRET", "mcp-secret")
	verifier, err := runtime.BuildIdentityVerifier()
	if err != nil || verifier == nil {
		t.Fatalf("build verifier: err=%v nil=%v", err, verifier == nil)
	}
	token := signMCP(t, "mcp-secret", "finance_user")

	srv := NewServer(newTestEngine(), "tenant_demo", "uk", verifier, false)
	var buf bytes.Buffer
	srv.writer = &buf

	args, _ := json.Marshal(map[string]string{"user_id": "attacker", "user_token": token, "question": aclQuestion})
	srv.handleGroundworkSearch(context.Background(), 1, json.RawMessage(args))

	out := buf.String()
	if !strings.Contains(out, "sharepoint-policy") {
		t.Fatalf("expected finance_user (from verified token) to receive sharepoint-policy, got: %s", out)
	}
}

func TestMCPFailsClosedWithoutVerifiedIdentity(t *testing.T) {
	srv := NewServer(newTestEngine(), "tenant_demo", "uk", nil, false) // no verifier, demo off
	var buf bytes.Buffer
	srv.writer = &buf

	args, _ := json.Marshal(map[string]string{"user_id": "finance_user", "question": aclQuestion})
	srv.handleGroundworkSearch(context.Background(), 2, json.RawMessage(args))

	out := buf.String()
	if !strings.Contains(out, "FAIL CLOSED") {
		t.Fatalf("expected fail-closed without a verified identity, got: %s", out)
	}
	if strings.Contains(out, "sharepoint-policy") {
		t.Fatalf("no document may be returned without a verified identity, got: %s", out)
	}
}

func TestMCPDemoModeAllowsRawUserID(t *testing.T) {
	srv := NewServer(newTestEngine(), "tenant_demo", "uk", nil, true) // demo identity ON
	var buf bytes.Buffer
	srv.writer = &buf

	args, _ := json.Marshal(map[string]string{"user_id": "finance_user", "question": aclQuestion})
	srv.handleGroundworkSearch(context.Background(), 3, json.RawMessage(args))

	out := buf.String()
	if !strings.Contains(out, "sharepoint-policy") {
		t.Fatalf("demo mode should resolve raw user_id finance_user and return the doc, got: %s", out)
	}
}
