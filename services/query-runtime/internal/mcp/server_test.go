package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
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

// recordingACL captures the req.UserID the engine checks against, so a test can prove the
// MCP transport canonicalized the identity before Engine.Execute. It allows every chunk so a
// candidate always reaches the ACL stage.
type recordingACL struct {
	mu    sync.Mutex
	users map[string]bool
}

func (r *recordingACL) CanAccess(_ context.Context, req runtime.QueryRequest, _ runtime.Chunk) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == nil {
		r.users = map[string]bool{}
	}
	r.users[req.UserID] = true
	return true, nil
}

func (r *recordingACL) saw(user string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.users[user]
}

// /mcp (and stdio, which shares executeSearch) must canonicalize the verified identity: the
// engine should see user_id = "principal:<uuid>", not the raw token subject.
func TestMCPUsesCanonicalIdentity(t *testing.T) {
	t.Setenv("GROUNDWORK_JWT_HS_SECRET", "mcp-secret")
	verifier, err := runtime.BuildIdentityVerifier()
	if err != nil || verifier == nil {
		t.Fatalf("build verifier: err=%v nil=%v", err, verifier == nil)
	}
	token := signMCP(t, "mcp-secret", "finance_user")

	backend := runtime.NewMemoryBackend()
	rec := &recordingACL{}
	eng := &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        500 * time.Millisecond,
			QdrantSearch: 100 * time.Millisecond,
			OpenFGACheck: 150 * time.Millisecond,
			AuditWrite:   50 * time.Millisecond,
		},
		Backend: engine.VectorRetrievalClient{Vector: backend.Vector},
		ACL:     rec,
		Auditor: engine.RuntimeTraceAuditWriter{Trace: backend.Trace},
	}

	resolver := runtime.NewMemoryPrincipalResolver()
	resolver.Seed("tenant_demo", "p-fin", []runtime.IdentityAssertion{{Namespace: "jwt:sub", Value: "finance_user", Verified: true}})

	srv := NewServer(eng, "tenant_demo", "uk", verifier, false)
	srv.SetCanonicalIdentity(resolver, true)
	var buf bytes.Buffer
	srv.writer = &buf

	args, _ := json.Marshal(map[string]string{"user_token": token, "question": aclQuestion})
	srv.handleGroundworkSearch(context.Background(), 1, json.RawMessage(args))

	if !rec.saw("principal:p-fin") {
		t.Fatalf("engine must check the canonical principal, observed users=%v", rec.users)
	}
	if rec.saw("finance_user") {
		t.Fatalf("raw token subject must not reach the engine when canonical identity is on, observed=%v", rec.users)
	}
}

// /mcp with canonical identity on and an unknown verified identity must fail closed.
func TestMCPCanonicalUnresolvedFailsClosed(t *testing.T) {
	t.Setenv("GROUNDWORK_JWT_HS_SECRET", "mcp-secret")
	verifier, err := runtime.BuildIdentityVerifier()
	if err != nil || verifier == nil {
		t.Fatalf("build verifier: err=%v nil=%v", err, verifier == nil)
	}
	token := signMCP(t, "mcp-secret", "nobody")

	srv := NewServer(newTestEngine(), "tenant_demo", "uk", verifier, false)
	srv.SetCanonicalIdentity(runtime.NewMemoryPrincipalResolver(), true) // empty resolver
	var buf bytes.Buffer
	srv.writer = &buf

	args, _ := json.Marshal(map[string]string{"user_token": token, "question": aclQuestion})
	srv.handleGroundworkSearch(context.Background(), 9, json.RawMessage(args))

	out := buf.String()
	if !strings.Contains(out, "FAIL CLOSED") || !strings.Contains(out, "identity_unresolved") {
		t.Fatalf("expected fail-closed identity_unresolved, got: %s", out)
	}
	if strings.Contains(out, "sharepoint-policy") {
		t.Fatalf("no document may be returned for an unresolved identity, got: %s", out)
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
