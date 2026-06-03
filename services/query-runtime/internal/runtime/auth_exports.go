package runtime

import "net/http"

// ExtractAPIKey returns the Groundwork API key from a request, accepting either an
// "Authorization: Bearer <key>" header or the "X-Groundwork-API-Key" header. It is a
// thin exported wrapper over the internal extractor so additional transports (e.g. the
// Cloud MCP HTTP endpoint) authenticate identically without duplicating the logic.
func ExtractAPIKey(r *http.Request) string {
	return extractAPIKey(r)
}

// HasScope reports whether the resolved tenant's API key carries the given scope (the
// "admin" scope satisfies any scope check). Exported wrapper over the internal check.
func HasScope(tenant TenantContext, scope string) bool {
	return hasScope(tenant, scope)
}
