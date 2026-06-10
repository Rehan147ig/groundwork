package aclsync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// passThroughTransport is used to confirm non-guarded requests are forwarded
// to the next RoundTripper unchanged.
type passThroughTransport struct {
	called bool
}

func (p *passThroughTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.called = true
	return http.DefaultTransport.RoundTrip(req)
}

// TestFGAInvariantViolation: a request whose URL path contains
// /authorization-models is refused, the OnViolation callback fires with the
// full URL, and the next transport is never invoked.
func TestFGAInvariantViolation(t *testing.T) {
	var violation string
	pass := &passThroughTransport{}
	guard := NewAuthzModelGuardTransport(pass, func(u string) { violation = u })
	client := &http.Client{Transport: guard}

	// Synthetic OpenFGA-shaped URL. The test does not need a live server: the
	// guard refuses the request before any I/O happens.
	url := "http://localhost:8080/stores/01ABCDEF/authorization-models"
	req, _ := http.NewRequest(http.MethodPost, url, nil)

	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("expected guard to refuse request, got nil error")
	}
	if !strings.Contains(err.Error(), AuthzModelInvariantViolation) {
		t.Fatalf("error message missing invariant marker: %v", err)
	}
	if !strings.Contains(violation, "/authorization-models") {
		t.Fatalf("OnViolation captured wrong URL: %q", violation)
	}
	if pass.called {
		t.Fatalf("next transport must NOT be invoked when guard refuses")
	}
}

// TestFGAGuardForwardsNonGuardedRequests: a request to a non-authorization-
// models path is forwarded to the next transport unchanged. This is what
// lets the OpenFGASink's writes/reads (which target /stores/{id}/write and
// /stores/{id}/read) keep working when the guard is installed.
func TestFGAGuardForwardsNonGuardedRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var fired bool
	guard := NewAuthzModelGuardTransport(http.DefaultTransport, func(string) { fired = true })
	client := &http.Client{Transport: guard}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/stores/abc/write", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("forwarded request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forwarded request: want 200, got %d", resp.StatusCode)
	}
	if fired {
		t.Fatalf("OnViolation must NOT fire for non-guarded paths")
	}
}

// TestFGAGuardWithNilCallback: a nil OnViolation is tolerated; the guard
// still refuses the request and returns the typed error.
func TestFGAGuardWithNilCallback(t *testing.T) {
	guard := NewAuthzModelGuardTransport(nil, nil)
	client := &http.Client{Transport: guard}
	req, _ := http.NewRequest(http.MethodPost, "http://example.test/stores/x/authorization-models", nil)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), AuthzModelInvariantViolation) {
		t.Fatalf("expected invariant error, got: %v", err)
	}
}
