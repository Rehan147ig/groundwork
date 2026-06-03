package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddingRetriesBeforeSuccess(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
	}))
	defer server.Close()

	vector, err := getRealEmbedding(
		context.Background(),
		server.URL,
		2*time.Second,
		NewServiceCircuitBreaker("embedding-test", 3, time.Second),
		"policy",
	)
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if len(vector) != 3 {
		t.Fatalf("expected embedding vector, got %v", vector)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 embedding calls, got %d", got)
	}
}
