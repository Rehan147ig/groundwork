package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type OpenFGAChecker struct {
	Endpoint  string
	StoreName string
	Client    *http.Client
	// AuthToken, when set, is sent as "Authorization: Bearer <token>" — OpenFGA's pre-shared
	// key auth. With OpenFGA configured to require it, only the runtime can query the
	// authorization store; the network firewalling is completed at deploy time.
	AuthToken string

	mu      sync.Mutex
	storeID string
	ready   bool
	breaker *CircuitBreaker
}

func NewOpenFGAChecker(endpoint string, storeName string, timeout time.Duration) *OpenFGAChecker {
	if storeName == "" {
		storeName = "groundwork_local"
	}
	if timeout <= 0 {
		timeout = 80 * time.Millisecond
	}
	return &OpenFGAChecker{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		StoreName: storeName,
		Client:    &http.Client{Timeout: timeout},
		AuthToken: strings.TrimSpace(os.Getenv("OPENFGA_API_TOKEN")),
		breaker: NewCircuitBreaker(CircuitBreakerSettings{
			Name: "openfga", FailureLimit: 3, OpenTimeout: 10 * time.Second, HalfOpenLimit: 1,
		}),
	}
}

func (o *OpenFGAChecker) CanAccess(ctx context.Context, req QueryRequest, chunk Chunk) (bool, error) {
	if req.TenantID == "" || req.UserID == "" || chunk.DocumentID == "" || chunk.TenantID != req.TenantID || chunk.SoftDeleted {
		return false, nil
	}
	if err := o.ensure(ctx); err != nil {
		return false, err
	}
	if o.breaker != nil {
		if err := o.breaker.Allow(); err != nil {
			return false, fmt.Errorf("%w: %v", ErrCircuitOpen, err)
		}
	}

	var parsed struct {
		Allowed bool `json:"allowed"`
	}
	err := retryWithBackoff(ctx, retryConfig{Attempts: 3, Base: 50 * time.Millisecond, Max: 500 * time.Millisecond}, func() error {
		return o.postJSON(ctx, "/stores/"+o.storeID+"/check", map[string]any{
			"tuple_key": map[string]string{
				"user":     "user:" + req.UserID,
				"relation": "viewer",
				"object":   "document:" + chunk.DocumentID,
			},
		}, &parsed)
	})
	if err != nil {
		if o.breaker != nil {
			o.breaker.ReportFailure()
		}
		return false, err
	}
	if o.breaker != nil {
		o.breaker.ReportSuccess()
	}
	return parsed.Allowed, nil
}

// ensure provisions the OpenFGA store and authorization model lazily on first use.
//
// If the store does not exist, it creates one, writes the current model, and seeds
// the default group memberships.
//
// If the store already exists (e.g. provisioned by a previous binary), it reconciles
// the model: when the latest stored model is missing a type the current binary
// expects (today: "folder"), it writes the current model as a new version. OpenFGA's
// /check uses the latest model when none is pinned, so the new model takes effect
// immediately for subsequent queries without restarting the runtime.
//
// This closes the "authorization model drift" gap that previously left an old
// 3-type model in place after a binary update — which made every folder-grant
// permission check fail closed.
func (o *OpenFGAChecker) ensure(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ready {
		return nil
	}

	storeID, created, err := o.ensureStore(ctx)
	if err != nil {
		return err
	}
	o.storeID = storeID

	if created {
		if err := o.writeAuthorizationModel(ctx); err != nil {
			return err
		}
		if err := o.seedDefaultMemberships(ctx); err != nil {
			return err
		}
	} else {
		// Drift detection: the store survived from a previous run but its model
		// may pre-date the current binary's expected types. Write the current
		// model as a new version if a required type is missing, and also seed
		// the default memberships — they're idempotent and the ingestion service
		// no longer guarantees they're written (it used to as part of its own
		// bootstrap, which has been removed to make this runtime the sole model
		// owner). Skipping the seed here would leave the default tuples missing
		// when ingestion creates the store first.
		hasFolder, err := o.latestModelHasType(ctx, "folder")
		if err != nil {
			return err
		}
		if !hasFolder {
			if err := o.writeAuthorizationModel(ctx); err != nil {
				return err
			}
			if err := o.seedDefaultMemberships(ctx); err != nil {
				return err
			}
		}
	}
	o.ready = true
	return nil
}

func (o *OpenFGAChecker) ensureStore(ctx context.Context) (string, bool, error) {
	var stores struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := o.getJSON(ctx, "/stores", &stores); err != nil {
		return "", false, err
	}
	for _, store := range stores.Stores {
		if store.Name == o.StoreName {
			return store.ID, false, nil
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := o.postJSON(ctx, "/stores", map[string]string{"name": o.StoreName}, &created); err != nil {
		return "", false, err
	}
	if created.ID == "" {
		return "", false, errors.New("openfga store creation returned empty id")
	}
	return created.ID, true, nil
}

func (o *OpenFGAChecker) writeAuthorizationModel(ctx context.Context) error {
	return o.postJSON(ctx, "/stores/"+o.storeID+"/authorization-models", openFGAModel(), nil)
}

// latestModelHasType returns true when the most recent authorization model in the
// current store contains a type definition for the named type. Used by ensure() to
// detect model drift after a binary update (e.g. an older binary wrote a 3-type
// model and a newer binary expects 4 types including "folder").
//
// OpenFGA's /authorization-models endpoint returns models newest-first. We fetch
// only the first page (page_size=1) — that's the latest model and the one /check
// will use when no model id is pinned.
func (o *OpenFGAChecker) latestModelHasType(ctx context.Context, typeName string) (bool, error) {
	var parsed struct {
		AuthorizationModels []struct {
			TypeDefinitions []struct {
				Type string `json:"type"`
			} `json:"type_definitions"`
		} `json:"authorization_models"`
	}
	if err := o.getJSON(ctx, "/stores/"+o.storeID+"/authorization-models?page_size=1", &parsed); err != nil {
		return false, err
	}
	if len(parsed.AuthorizationModels) == 0 {
		return false, nil
	}
	for _, td := range parsed.AuthorizationModels[0].TypeDefinitions {
		if td.Type == typeName {
			return true, nil
		}
	}
	return false, nil
}

func (o *OpenFGAChecker) seedDefaultMemberships(ctx context.Context) error {
	return o.writeTuples(ctx, []map[string]string{
		{"user": "user:finance_user", "relation": "member", "object": "group:finance"},
		{"user": "user:executive_user", "relation": "member", "object": "group:executive"},
		{"user": "user:security_user", "relation": "member", "object": "group:security"},
	})
}

func (o *OpenFGAChecker) writeTuples(ctx context.Context, tuples []map[string]string) error {
	if len(tuples) == 0 {
		return nil
	}
	err := o.postJSON(ctx, "/stores/"+o.storeID+"/write", map[string]any{
		"writes": map[string]any{"tuple_keys": tuples},
	}, nil)
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "already") {
		return nil
	}
	return err
}

func (o *OpenFGAChecker) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Endpoint+path, nil)
	if err != nil {
		return err
	}
	if o.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+o.AuthToken)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %v", ErrACLTimeout, err)
		}
		return fmt.Errorf("%w: %v", ErrACLBackendUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: openfga get %s failed: %s %s", ErrACLModelMissing, path, resp.Status, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("openfga get %s failed: %s %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (o *OpenFGAChecker) postJSON(ctx context.Context, path string, body any, out any) error {
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+o.AuthToken)
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %v", ErrACLTimeout, err)
		}
		return fmt.Errorf("%w: %v", ErrACLBackendUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: openfga post %s failed: %s %s", ErrACLModelMissing, path, resp.Status, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("openfga post %s failed: %s %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// openFGAModel defines the Groundwork authorization model. It supports users,
// (nested) groups, folders, and documents that inherit viewers from their parent
// folder. The query-time check ("user X viewer document Y") is unchanged — OpenFGA
// resolves group membership and folder inheritance through this model, so the
// CanAccess logic does not need to know about folders. The ACL-sync framework
// (internal/aclsync) writes tuples that conform to this model.
func openFGAModel() map[string]any {
	// directly_related_user_types entry sets reused across relations.
	userAndGroupMembers := []map[string]string{{"type": "user"}, {"type": "group", "relation": "member"}}
	return map[string]any{
		"schema_version": "1.1",
		"type_definitions": []map[string]any{
			{"type": "user"},
			{
				// Groups support nested membership: a group#member can be a member of
				// another group (e.g. group:finance#member member group:employees).
				"type":      "group",
				"relations": map[string]any{"member": map[string]any{"this": map[string]any{}}},
				"metadata": map[string]any{"relations": map[string]any{"member": map[string]any{
					"directly_related_user_types": userAndGroupMembers,
				}}},
			},
			{
				// Folders carry viewer grants for users and groups.
				"type":      "folder",
				"relations": map[string]any{"viewer": map[string]any{"this": map[string]any{}}},
				"metadata": map[string]any{"relations": map[string]any{"viewer": map[string]any{
					"directly_related_user_types": userAndGroupMembers,
				}}},
			},
			{
				// A document has a parent folder and inherits that folder's viewers in
				// addition to any directly-granted viewers (union with "viewer from parent").
				"type": "document",
				"relations": map[string]any{
					"parent": map[string]any{"this": map[string]any{}},
					"viewer": map[string]any{
						"union": map[string]any{"child": []map[string]any{
							{"this": map[string]any{}},
							{"tupleToUserset": map[string]any{
								"tupleset":        map[string]any{"relation": "parent"},
								"computedUserset": map[string]any{"relation": "viewer"},
							}},
						}},
					},
				},
				"metadata": map[string]any{"relations": map[string]any{
					"parent": map[string]any{"directly_related_user_types": []map[string]string{{"type": "folder"}}},
					"viewer": map[string]any{"directly_related_user_types": userAndGroupMembers},
				}},
			},
		},
	}
}
