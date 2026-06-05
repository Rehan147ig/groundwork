//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	testRegion = "uk"
	testDoc    = "doc-fin"
	vecDim     = 384 // must match the seeded Qdrant collection + the stub embedder
)

// --- environment ---

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func testDatabaseURL() string { return strings.TrimSpace(os.Getenv("GROUNDWORK_TEST_DATABASE_URL")) }
func testOpenFGAURL() string {
	return strings.TrimRight(envOr("GROUNDWORK_TEST_OPENFGA_URL", "http://localhost:8080"), "/")
}
func testQdrantURL() string {
	return strings.TrimRight(envOr("GROUNDWORK_TEST_QDRANT_URL", "http://localhost:6333"), "/")
}

func migrationsDir() string {
	// Default is relative to this package dir (services/query-runtime/test/integration);
	// the repo's migrations/ live four levels up. scripts/integration-test.sh sets an
	// absolute path via GROUNDWORK_TEST_MIGRATIONS_DIR.
	return envOr("GROUNDWORK_TEST_MIGRATIONS_DIR", filepath.Join("..", "..", "..", "..", "migrations"))
}

// requireIntegration skips a test unless the live-backend env is configured. A skip (not a
// silent pass) keeps `go test -tags integration` honest when the stack isn't running.
func requireIntegration(t *testing.T) {
	t.Helper()
	if testDatabaseURL() == "" {
		t.Skip("integration backends not configured; run scripts/integration-test.sh (sets GROUNDWORK_TEST_DATABASE_URL/OPENFGA_URL/QDRANT_URL)")
	}
}

// --- TestMain: apply migrations + wait for services ---

func TestMain(m *testing.M) {
	if testDatabaseURL() != "" {
		if err := applyMigrations(testDatabaseURL()); err != nil {
			fmt.Fprintf(os.Stderr, "integration setup: %v\n", err)
			os.Exit(1)
		}
		waitHTTP(testOpenFGAURL()+"/healthz", 30*time.Second)
		waitHTTP(testQdrantURL()+"/readyz", 30*time.Second)
	}
	os.Exit(m.Run())
}

func applyMigrations(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := waitDB(db, 60*time.Second); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(migrationsDir(), "*.up.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no *.up.sql migrations found in %s", migrationsDir())
	}
	sort.Strings(files) // 003 < 004 < 005 < 006 < 007 — order matters (005 ALTERs audit_log)
	for _, f := range files {
		stmt, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(stmt)); err != nil {
			if alreadyApplied(err) {
				continue // re-run against an already-migrated DB
			}
			return fmt.Errorf("apply %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

func alreadyApplied(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "duplicate")
}

func waitDB(db *sql.DB, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres not ready within %s: %w", d, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func waitHTTP(url string, d time.Duration) {
	deadline := time.Now().Add(d)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// --- shared helpers ---

var uniqueCounter atomic.Int64

func unique() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), uniqueCounter.Add(1))
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", testDatabaseURL())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if err := waitDB(db, 15*time.Second); err != nil {
		t.Fatalf("db not ready: %v", err)
	}
	return db
}

func httpJSON(t *testing.T, method, url string, body any) []byte {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s -> %s: %s", method, url, resp.Status, strings.TrimSpace(string(data)))
	}
	return data
}

// --- Qdrant seeding (real REST API) ---

// constVector is the fixed embedding used for both the seeded point and the stub embedder.
// Vector *values* are irrelevant to these tests: Qdrant returns the nearest of however many
// points exist, so a single seeded point is always retrieved regardless of the query vector.
func constVector() []float32 {
	v := make([]float32, vecDim)
	for i := range v {
		v[i] = 0.1
	}
	return v
}

// seedQdrantChunk (re)creates a collection with exactly one point for documentID under
// tenantID, so the engine's vector retrieval returns it and the ACL/audit stages run.
func seedQdrantChunk(t *testing.T, collection, tenantID, documentID, text string) {
	t.Helper()
	base := testQdrantURL()
	// Fresh collection each run.
	deleteBestEffort(base + "/collections/" + collection)
	httpJSON(t, http.MethodPut, base+"/collections/"+collection, map[string]any{
		"vectors": map[string]any{"size": vecDim, "distance": "Cosine"},
	})
	hash := sha256hex(text)
	httpJSON(t, http.MethodPut, base+"/collections/"+collection+"/points?wait=true", map[string]any{
		"points": []map[string]any{{
			"id":     1,
			"vector": constVector(),
			"payload": map[string]any{
				"document_id":     documentID,
				"chunk_id":        "chk_" + hash[:20],
				"chunk_hash":      hash,
				"text":            text,
				"page":            1,
				"offset":          0,
				"freshness_score": 1.0,
				"soft_deleted":    false,
				"metadata": map[string]any{
					"tenant_id":      tenantID,
					"region":         testRegion,
					"source_scope":   "SharePoint",
					"owner_acl_tags": []string{},
				},
			},
		}},
	})
}

func deleteBestEffort(url string) {
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// --- stub embedder (so we exercise the real QdrantVectorSearcher without the ML model) ---

func startStubEmbedder(t *testing.T) string {
	t.Helper()
	vec := constVector()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": vec})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func qdrantSearcher(collection, embedderURL string) runtime.QdrantVectorSearcher {
	return runtime.QdrantVectorSearcher{
		Endpoint:         testQdrantURL(),
		Collection:       collection,
		Client:           &http.Client{Timeout: 10 * time.Second},
		EmbeddingURL:     embedderURL,
		EmbeddingTimeout: 10 * time.Second,
	}
}

// --- OpenFGA (real OpenFGAChecker provisions the store + model; we add tuples) ---

// initOpenFGAStore triggers the checker's lazy store+model provisioning (one CanAccess call)
// and returns the created store id so the test can write its own tuples.
func initOpenFGAStore(t *testing.T, checker *runtime.OpenFGAChecker, storeName string) string {
	t.Helper()
	_, err := checker.CanAccess(context.Background(),
		runtime.QueryRequest{TenantID: "tenant_warmup", UserID: "warmup", Region: testRegion},
		runtime.Chunk{TenantID: "tenant_warmup", DocumentID: "warmup-doc"})
	if err != nil {
		t.Fatalf("openfga warmup/provision failed (is OpenFGA reachable at %s?): %v", testOpenFGAURL(), err)
	}
	var parsed struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	_ = json.Unmarshal(httpJSON(t, http.MethodGet, testOpenFGAURL()+"/stores", nil), &parsed)
	for _, s := range parsed.Stores {
		if s.Name == storeName {
			return s.ID
		}
	}
	t.Fatalf("openfga store %q not found after provisioning", storeName)
	return ""
}

func writeOpenFGATuple(t *testing.T, storeID, user, relation, object string) {
	t.Helper()
	httpJSON(t, http.MethodPost, testOpenFGAURL()+"/stores/"+storeID+"/write", map[string]any{
		"writes": map[string]any{"tuple_keys": []map[string]string{
			{"user": user, "relation": relation, "object": object},
		}},
	})
}

// deadEndpoint returns a URL whose port is closed, so connections are refused — a
// deterministic stand-in for "OpenFGA is down / unreachable".
func deadEndpoint(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

// --- engine assembly ---

func newEngine(vector runtime.VectorSearcher, acl engine.ACLChecker, auditor engine.AuditWriter) *engine.Engine {
	return &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        15 * time.Second,
			Embedding:    10 * time.Second,
			QdrantSearch: 10 * time.Second,
			OpenFGACheck: 10 * time.Second,
			AuditWrite:   10 * time.Second,
		},
		Backend: engine.VectorRetrievalClient{Vector: vector},
		ACL:     acl,
		Auditor: auditor,
	}
}

func postgresAuditor(db *sql.DB) engine.AuditWriter {
	// Generous timeout: the default NewPostgresAuditWriter uses 30ms, which is too tight for a
	// real Postgres round-trip and would make audit writes (and thus queries) fail closed.
	return engine.NewPostgresAuditWriterWithTimeout(db, 10*time.Second)
}
