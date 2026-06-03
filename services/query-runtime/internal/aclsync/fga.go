package aclsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

// TupleSink is the write target for synced permissions. Production uses OpenFGASink
// (the real OpenFGA server); tests and local dev use MemoryFGA.
type TupleSink interface {
	ListTuples(ctx context.Context, tenantID string) ([]Tuple, error)
	WriteTuples(ctx context.Context, tenantID string, tuples []Tuple) error
	DeleteTuples(ctx context.Context, tenantID string, tuples []Tuple) error
}

// --- MemoryFGA: in-memory sink + checker (dev/test double) ---

// MemoryFGA is an in-memory tuple store that mirrors the Groundwork OpenFGA model's
// resolution semantics (nested group membership + folder→document viewer inheritance).
// It is a development/test double, NOT a replacement for OpenFGA: production feeds the
// real OpenFGA server via OpenFGASink and enforces with the unchanged query-runtime
// OpenFGA checker. MemoryFGA additionally implements runtime.ACLChecker so tests can
// drive the real engine.Execute path against synced tuples.
type MemoryFGA struct {
	mu       sync.RWMutex
	byTenant map[string]map[Tuple]bool
}

func NewMemoryFGA() *MemoryFGA {
	return &MemoryFGA{byTenant: map[string]map[Tuple]bool{}}
}

func (m *MemoryFGA) tenant(tenantID string) map[Tuple]bool {
	if m.byTenant[tenantID] == nil {
		m.byTenant[tenantID] = map[Tuple]bool{}
	}
	return m.byTenant[tenantID]
}

func (m *MemoryFGA) ListTuples(_ context.Context, tenantID string) ([]Tuple, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	set := m.byTenant[tenantID]
	out := make([]Tuple, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out, nil
}

func (m *MemoryFGA) WriteTuples(_ context.Context, tenantID string, tuples []Tuple) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.tenant(tenantID)
	for _, t := range tuples {
		set[t] = true
	}
	return nil
}

func (m *MemoryFGA) DeleteTuples(_ context.Context, tenantID string, tuples []Tuple) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.tenant(tenantID)
	for _, t := range tuples {
		delete(set, t)
	}
	return nil
}

// Check answers a relation query (mirrors OpenFGA). Supported objects:
//
//	viewer document:D — direct viewer, group viewer, or inherited from parent folder
//	viewer folder:F   — direct viewer or group viewer
//	member group:G    — direct or nested group membership
func (m *MemoryFGA) Check(tenantID, user, relation, object string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	set := m.byTenant[tenantID]
	if set == nil {
		return false
	}
	switch {
	case relation == "viewer" && strings.HasPrefix(object, "document:"):
		return viewerDocument(set, user, object)
	case relation == "viewer" && strings.HasPrefix(object, "folder:"):
		return viewerFolder(set, user, object)
	case relation == "member" && strings.HasPrefix(object, "group:"):
		return memberOf(set, user, object, map[string]bool{})
	default:
		return false
	}
}

// CanAccess implements runtime.ACLChecker so the engine can enforce against synced
// tuples in dev/test: it resolves "is req.UserID a viewer of chunk.DocumentID".
func (m *MemoryFGA) CanAccess(_ context.Context, req runtime.QueryRequest, chunk runtime.Chunk) (bool, error) {
	if req.TenantID == "" || req.UserID == "" || chunk.DocumentID == "" || chunk.SoftDeleted || chunk.TenantID != req.TenantID {
		return false, nil
	}
	return m.Check(req.TenantID, userRef(req.UserID), "viewer", documentRef(chunk.DocumentID)), nil
}

func memberOf(set map[Tuple]bool, user, group string, seen map[string]bool) bool {
	if seen[group] {
		return false
	}
	seen[group] = true
	if set[Tuple{user, "member", group}] {
		return true
	}
	// Nested: group:H#member member group  AND  user is a member of H.
	for t := range set {
		if t.Relation == "member" && t.Object == group && strings.HasPrefix(t.User, "group:") && strings.HasSuffix(t.User, "#member") {
			h := "group:" + strings.TrimSuffix(strings.TrimPrefix(t.User, "group:"), "#member")
			if memberOf(set, user, h, seen) {
				return true
			}
		}
	}
	return false
}

func viewerFolder(set map[Tuple]bool, user, folder string) bool {
	if set[Tuple{user, "viewer", folder}] {
		return true
	}
	for t := range set {
		if t.Relation == "viewer" && t.Object == folder && strings.HasSuffix(t.User, "#member") {
			g := "group:" + strings.TrimSuffix(strings.TrimPrefix(t.User, "group:"), "#member")
			if memberOf(set, user, g, map[string]bool{}) {
				return true
			}
		}
	}
	return false
}

func viewerDocument(set map[Tuple]bool, user, document string) bool {
	if set[Tuple{user, "viewer", document}] {
		return true
	}
	for t := range set {
		if t.Relation == "viewer" && t.Object == document && strings.HasSuffix(t.User, "#member") {
			g := "group:" + strings.TrimSuffix(strings.TrimPrefix(t.User, "group:"), "#member")
			if memberOf(set, user, g, map[string]bool{}) {
				return true
			}
		}
	}
	// Inherit viewers from the parent folder(s).
	for t := range set {
		if t.Relation == "parent" && t.Object == document && strings.HasPrefix(t.User, "folder:") {
			if viewerFolder(set, user, t.User) {
				return true
			}
		}
	}
	return false
}

// --- OpenFGASink: real OpenFGA write target (production) ---

// OpenFGASink writes/deletes/reads tuples on a live OpenFGA server. The store and
// authorization model are provisioned by query-runtime (internal/runtime/openfga.go);
// this sink only manages tuples. It is exercised against a live OpenFGA in integration
// (not in unit tests).
type OpenFGASink struct {
	Endpoint             string
	StoreName            string
	StoreID              string // if set, used directly (skips store lookup by name)
	AuthorizationModelID string // optional; pinned model id included in write requests
	Client               *http.Client

	mu      sync.Mutex
	storeID string
}

func NewOpenFGASink(endpoint, storeName string) *OpenFGASink {
	if storeName == "" {
		storeName = "groundwork_local"
	}
	return &OpenFGASink{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		StoreName: storeName,
		Client:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (o *OpenFGASink) ensureStoreID(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.StoreID != "" {
		return o.StoreID, nil
	}
	if o.storeID != "" {
		return o.storeID, nil
	}
	var stores struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := o.getJSON(ctx, "/stores", &stores); err != nil {
		return "", err
	}
	for _, s := range stores.Stores {
		if s.Name == o.StoreName {
			o.storeID = s.ID
			return o.storeID, nil
		}
	}
	return "", fmt.Errorf("openfga store %q not found; provision it via query-runtime first", o.StoreName)
}

func (o *OpenFGASink) WriteTuples(ctx context.Context, _ string, tuples []Tuple) error {
	return o.writeOrDelete(ctx, "writes", tuples)
}

func (o *OpenFGASink) DeleteTuples(ctx context.Context, _ string, tuples []Tuple) error {
	return o.writeOrDelete(ctx, "deletes", tuples)
}

func (o *OpenFGASink) writeOrDelete(ctx context.Context, op string, tuples []Tuple) error {
	if len(tuples) == 0 {
		return nil
	}
	storeID, err := o.ensureStoreID(ctx)
	if err != nil {
		return err
	}
	keys := make([]map[string]string, 0, len(tuples))
	for _, t := range tuples {
		keys = append(keys, map[string]string{"user": t.User, "relation": t.Relation, "object": t.Object})
	}
	body := map[string]any{op: map[string]any{"tuple_keys": keys}}
	if o.AuthorizationModelID != "" {
		body["authorization_model_id"] = o.AuthorizationModelID
	}
	return o.postJSON(ctx, "/stores/"+storeID+"/write", body, nil)
}

func (o *OpenFGASink) ListTuples(ctx context.Context, _ string) ([]Tuple, error) {
	storeID, err := o.ensureStoreID(ctx)
	if err != nil {
		return nil, err
	}
	var out []Tuple
	continuation := ""
	for {
		req := map[string]any{"page_size": 100}
		if continuation != "" {
			req["continuation_token"] = continuation
		}
		var resp struct {
			Tuples []struct {
				Key struct {
					User     string `json:"user"`
					Relation string `json:"relation"`
					Object   string `json:"object"`
				} `json:"key"`
			} `json:"tuples"`
			ContinuationToken string `json:"continuation_token"`
		}
		if err := o.postJSON(ctx, "/stores/"+storeID+"/read", req, &resp); err != nil {
			return nil, err
		}
		for _, t := range resp.Tuples {
			out = append(out, Tuple{User: t.Key.User, Relation: t.Key.Relation, Object: t.Key.Object})
		}
		if resp.ContinuationToken == "" || resp.ContinuationToken == continuation {
			break
		}
		continuation = resp.ContinuationToken
	}
	return out, nil
}

func (o *OpenFGASink) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint+path, nil)
	if err != nil {
		return err
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openfga GET %s: %s", path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (o *OpenFGASink) postJSON(ctx context.Context, path string, body, out any) error {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("openfga POST %s: %s", path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// compile-time checks
var (
	_ TupleSink          = (*MemoryFGA)(nil)
	_ TupleSink          = (*OpenFGASink)(nil)
	_ runtime.ACLChecker = (*MemoryFGA)(nil)
)
