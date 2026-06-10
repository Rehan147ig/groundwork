package aclsync

import (
	"net/http"
	"strings"
)

// AuthzModelInvariantViolation is the message logged and surfaced when the
// connector attempts to call OpenFGA's /authorization-models endpoint. The
// single-owner rule for the OpenFGA authorization model is that the
// query-runtime (services/query-runtime/internal/runtime/openfga.go) is the
// only writer. Any sync connector calling /authorization-models is a bug
// that risks model drift — the same class of bug Patch 3 fixed when the
// ingestion service was rewriting the model behind the runtime's back.
const AuthzModelInvariantViolation = "fga_invariant_violation: connector attempted to call /authorization-models"

// AuthzModelGuardTransport wraps an http.RoundTripper and refuses any request
// whose URL path contains /authorization-models. It is intended to wrap the
// transport used by OpenFGASink (and any future OpenFGA client) so that even
// an accidentally introduced model-write call from inside a connector is
// caught at the transport boundary, before it reaches OpenFGA.
//
// Behavior on violation:
//  1. The OnViolation callback is invoked with the offending URL.
//  2. RoundTrip returns ErrAuthzModelInvariantViolation; the request never
//     leaves the process.
//
// The cmd/msgraph-connector binary installs an OnViolation that logs at
// ERROR and calls os.Exit(2). Tests can install a callback that records the
// violation for assertion without exiting the test process.
type AuthzModelGuardTransport struct {
	Next        http.RoundTripper
	OnViolation func(url string)
}

// ErrAuthzModelInvariantViolation is returned by RoundTrip when a guarded
// request is refused. The string form is stable so it can be tested.
type errAuthzModelInvariantViolation struct{ url string }

func (e errAuthzModelInvariantViolation) Error() string {
	return AuthzModelInvariantViolation + ": " + e.url
}

// NewAuthzModelGuardTransport returns a transport that refuses any request
// to a path containing /authorization-models. A nil next defaults to
// http.DefaultTransport so callers can install the guard ahead of a default
// http.Client. The OnViolation callback may be nil; when nil, the guard
// still refuses the request and returns the typed error.
func NewAuthzModelGuardTransport(next http.RoundTripper, onViolation func(url string)) *AuthzModelGuardTransport {
	if next == nil {
		next = http.DefaultTransport
	}
	return &AuthzModelGuardTransport{Next: next, OnViolation: onViolation}
}

// RoundTrip implements http.RoundTripper. It inspects the URL path of every
// outgoing request and refuses any that target /authorization-models on any
// OpenFGA store.
func (g *AuthzModelGuardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil && req.URL != nil && isAuthzModelPath(req.URL.Path) {
		urlStr := req.URL.String()
		if g.OnViolation != nil {
			g.OnViolation(urlStr)
		}
		return nil, errAuthzModelInvariantViolation{url: urlStr}
	}
	return g.Next.RoundTrip(req)
}

// isAuthzModelPath returns true for any OpenFGA path that writes or reads
// the authorization model — the canonical pattern is
// /stores/{id}/authorization-models. The substring check is intentionally
// permissive (matches both writes via POST and reads via GET) so the guard
// can be used to enforce "this binary is not allowed to even read the
// model definitions" if that ever becomes desired.
func isAuthzModelPath(p string) bool {
	return strings.Contains(p, "/authorization-models")
}
