package runtime

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimiterFixedWindow(t *testing.T) {
	rl := NewRateLimiter()

	// rpm=2: two allowed, third blocked within the same window.
	if ok, _ := rl.Allow(1, 2); !ok {
		t.Fatal("request 1 should be allowed")
	}
	if ok, _ := rl.Allow(1, 2); !ok {
		t.Fatal("request 2 should be allowed")
	}
	ok, retry := rl.Allow(1, 2)
	if ok {
		t.Fatal("request 3 should be blocked")
	}
	if retry <= 0 {
		t.Fatalf("blocked request should report a positive retry-after, got %v", retry)
	}

	// Different key has its own independent budget.
	if ok, _ := rl.Allow(2, 2); !ok {
		t.Fatal("a different key must not be affected by key 1's budget")
	}

	// rpm<=0 means unlimited.
	for i := 0; i < 100; i++ {
		if ok, _ := rl.Allow(3, 0); !ok {
			t.Fatal("rpm=0 must be unlimited")
		}
	}
}

func TestNilRateLimiterAllows(t *testing.T) {
	var rl *RateLimiter // nil
	if ok, _ := rl.Allow(1, 1); !ok {
		t.Fatal("a nil limiter must allow (no enforcement configured)")
	}
}

// A request that exceeds the key's rate_limit_rpm budget is rejected with 429.
func TestRateLimitMiddlewareReturns429(t *testing.T) {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_rl_key", TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "rl", RateLimitRPM: 1,
	})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.allowDemoIdentity = true
	s.SetRateLimiter(NewRateLimiter())

	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"user_id":"u","question":"q"}`))
		req.Header.Set("X-Groundwork-API-Key", "gw_rl_key")
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send(); code != http.StatusOK {
		t.Fatalf("first request should pass (200), got %d", code)
	}
	if code := send(); code != http.StatusTooManyRequests {
		t.Fatalf("second request should be rate-limited (429), got %d", code)
	}
}

// Without a configured limiter, no throttling occurs (local/demo + existing behavior).
func TestNoLimiterNoThrottle(t *testing.T) {
	server := newTestServer(Config{}) // no SetRateLimiter
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"user_id":"u","question":"q"}`))
		req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d should pass without a limiter, got %d", i, rec.Code)
		}
	}
}
