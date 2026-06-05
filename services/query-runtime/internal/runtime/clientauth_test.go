package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The OpenFGA client sends the pre-shared key as a Bearer token when OPENFGA_API_TOKEN is set,
// so a locked-down OpenFGA only answers the runtime.
func TestOpenFGASendsAuthToken(t *testing.T) {
	var mu sync.Mutex
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/stores" {
			_ = json.NewEncoder(w).Encode(map[string]any{"stores": []map[string]string{{"id": "s1", "name": "t"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"allowed": false})
	}))
	defer srv.Close()

	t.Setenv("OPENFGA_API_TOKEN", "secret-tok")
	c := NewOpenFGAChecker(srv.URL, "t", time.Second)
	_, _ = c.CanAccess(context.Background(),
		QueryRequest{TenantID: "t1", UserID: "u", Region: "uk"},
		Chunk{TenantID: "t1", DocumentID: "d"})

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer secret-tok" {
		t.Fatalf("expected OpenFGA request to carry Bearer auth, got %q", gotAuth)
	}
}

// The Qdrant client sends the "api-key" header when APIKey is set.
func TestQdrantSendsAPIKey(t *testing.T) {
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
	}))
	defer embed.Close()

	var mu sync.Mutex
	var gotKey string
	qd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotKey = r.Header.Get("api-key")
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{}})
	}))
	defer qd.Close()

	q := QdrantVectorSearcher{
		Endpoint: qd.URL, Collection: "c", Client: qd.Client(),
		EmbeddingURL: embed.URL, EmbeddingTimeout: time.Second, APIKey: "qd-key",
	}
	if _, err := q.SearchVector(context.Background(), QueryRequest{TenantID: "t1", Question: "q"}, 5); err != nil {
		t.Fatalf("search: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotKey != "qd-key" {
		t.Fatalf("expected Qdrant request to carry the api-key header, got %q", gotKey)
	}
}
