package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrACLUnavailable = errors.New("live acl unavailable")
var ErrACLTimeout = errors.New("acl timeout")
var ErrACLBackendUnavailable = errors.New("acl backend unavailable")
var ErrACLModelMissing = errors.New("acl model missing")
var ErrCircuitOpen = errors.New("qdrant circuit open")

type VectorSearcher interface {
	SearchVector(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error)
}

type LexicalSearcher interface {
	SearchLexical(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error)
}

type ACLChecker interface {
	CanAccess(ctx context.Context, req QueryRequest, chunk Chunk) (bool, error)
}

type TraceWriter interface {
	WriteTrace(ctx context.Context, trace RuntimeTrace) error
}

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type Backend struct {
	Vector  VectorSearcher
	Lexical LexicalSearcher
	ACL     ACLChecker
	Trace   TraceWriter
}

type BackendConfig struct {
	HTTPTimeout          time.Duration
	EmbeddingURL         string
	EmbeddingTimeout     time.Duration
	CircuitOpenTimeout   time.Duration
	CircuitFailureLimit  int
	CircuitHalfOpenLimit int
}

func NewMemoryBackend() Backend {
	store := NewMemoryStore()
	return Backend{
		Vector:  MemoryVectorSearcher{Store: store},
		Lexical: MemoryLexicalSearcher{Store: store},
		ACL:     MemoryACLChecker{},
		Trace:   store,
	}
}

func NewHTTPBackend(qdrantURL string, qdrantCollection string, elasticURL string, elasticIndex string, embeddingURL string, cfg Config) Backend {
	backendCfg := BackendConfig{
		HTTPTimeout:          cfg.BackendHTTPTimeout,
		EmbeddingURL:         embeddingURL,
		EmbeddingTimeout:     cfg.EmbeddingTimeout,
		CircuitOpenTimeout:   cfg.CircuitOpenTimeout,
		CircuitFailureLimit:  cfg.CircuitFailureLimit,
		CircuitHalfOpenLimit: cfg.CircuitHalfOpenLimit,
	}
	if backendCfg.HTTPTimeout <= 0 {
		backendCfg.HTTPTimeout = 15 * time.Second
	}
	if backendCfg.EmbeddingTimeout <= 0 {
		backendCfg.EmbeddingTimeout = backendCfg.HTTPTimeout
	}
	if backendCfg.CircuitOpenTimeout <= 0 {
		backendCfg.CircuitOpenTimeout = 10 * time.Second
	}
	if backendCfg.CircuitFailureLimit <= 0 {
		backendCfg.CircuitFailureLimit = 3
	}
	if backendCfg.CircuitHalfOpenLimit <= 0 {
		backendCfg.CircuitHalfOpenLimit = 1
	}
	client := &http.Client{Timeout: backendCfg.HTTPTimeout}
	return Backend{
		Vector: QdrantVectorSearcher{
			Endpoint: qdrantURL, Collection: qdrantCollection, Client: client,
			EmbeddingURL: embeddingURL, EmbeddingTimeout: backendCfg.EmbeddingTimeout,
			Breaker: NewCircuitBreaker(CircuitBreakerSettings{
				Name: "qdrant", FailureLimit: backendCfg.CircuitFailureLimit,
				OpenTimeout: backendCfg.CircuitOpenTimeout, HalfOpenLimit: backendCfg.CircuitHalfOpenLimit,
			}),
			EmbeddingBreaker: NewCircuitBreaker(CircuitBreakerSettings{
				Name: "embedding", FailureLimit: backendCfg.CircuitFailureLimit,
				OpenTimeout: backendCfg.CircuitOpenTimeout, HalfOpenLimit: backendCfg.CircuitHalfOpenLimit,
			}),
		},
		Lexical: ElasticsearchLexicalSearcher{
			Endpoint: elasticURL, Index: elasticIndex, Client: client,
			Breaker: NewCircuitBreaker(CircuitBreakerSettings{
				Name: "elasticsearch", FailureLimit: backendCfg.CircuitFailureLimit,
				OpenTimeout: backendCfg.CircuitOpenTimeout, HalfOpenLimit: backendCfg.CircuitHalfOpenLimit,
			}),
		},
		ACL:   aclCheckerForConfig(cfg),
		Trace: NewMemoryStore(),
	}
}

func aclCheckerForConfig(cfg Config) ACLChecker {
	if cfg.OpenFGAURL == "" {
		return MemoryACLChecker{}
	}
	timeout := cfg.OpenFGATimeout
	if timeout <= 0 {
		timeout = 80 * time.Millisecond
	}
	return NewOpenFGAChecker(cfg.OpenFGAURL, cfg.OpenFGAStoreName, timeout)
}

type MemoryStore struct {
	mu     sync.RWMutex
	chunks []Chunk
	traces []RuntimeTrace
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		chunks: []Chunk{
			newChunk("tenant_demo", "uk", "sharepoint-policy", "SharePoint", []string{"finance_user"}, "Live ACL checks must fail closed when Microsoft Graph cannot confirm source permissions."),
			newChunk("tenant_demo", "uk", "runtime-policy", "platform", []string{"engineering"}, "Groundwork runs vector and BM25 searches in parallel, fuses candidates with RRF, and drops unauthorized chunks before prompt assembly."),
			newChunk("tenant_demo", "eu", "residency-policy", "security", []string{"compliance_officer"}, "EU tenant data is isolated to approved European regions and must not cross into US or UK indexes."),
		},
	}
}

type MemoryVectorSearcher struct {
	Store *MemoryStore
}

func (m MemoryVectorSearcher) SearchVector(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error) {
	return m.Store.search(ctx, req, limit, 1.00)
}

type MemoryLexicalSearcher struct {
	Store *MemoryStore
}

func (m MemoryLexicalSearcher) SearchLexical(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error) {
	return m.Store.search(ctx, req, limit, 0.88)
}

type MemoryACLChecker struct{}

func (MemoryACLChecker) CanAccess(ctx context.Context, req QueryRequest, chunk Chunk) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ErrACLUnavailable
	default:
	}
	if req.TenantID == "" || req.UserID == "" {
		return false, ErrACLUnavailable
	}
	if chunk.SoftDeleted || chunk.TenantID != req.TenantID {
		return false, nil
	}
	for _, scope := range req.SourceScopes {
		if scope == chunk.RequiredScope {
			return true, nil
		}
	}
	userTags := aclTagsForUser(req.UserID)
	for _, required := range chunk.OwnerACLTags {
		for _, userTag := range userTags {
			if strings.EqualFold(required, userTag) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *MemoryStore) WriteTrace(ctx context.Context, trace RuntimeTrace) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traces = append(m.traces, trace)
	return nil
}

func (m *MemoryStore) search(ctx context.Context, req QueryRequest, limit int, multiplier float64) ([]Candidate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	terms := tokenize(req.Question)
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Candidate
	for _, chunk := range m.chunks {
		if chunk.TenantID != req.TenantID || chunk.SoftDeleted {
			continue
		}
		score := lexicalScore(terms, chunk.Text) * multiplier * chunk.FreshnessScore
		if score <= 0 {
			continue
		}
		out = append(out, Candidate{Chunk: chunk, Score: score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

type QdrantVectorSearcher struct {
	Endpoint         string
	Collection       string
	Client           *http.Client
	EmbeddingURL     string
	EmbeddingTimeout time.Duration
	Breaker          *CircuitBreaker
	EmbeddingBreaker *CircuitBreaker
}

func (q QdrantVectorSearcher) SearchVector(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error) {
	if q.Endpoint == "" || q.Collection == "" {
		return nil, errors.New("qdrant endpoint and collection are required")
	}
	vector, err := getRealEmbedding(ctx, q.EmbeddingURL, q.EmbeddingTimeout, q.EmbeddingBreaker, req.Question)
	if err != nil {
		return nil, err
	}
	if q.Breaker != nil {
		if err := q.Breaker.Allow(); err != nil {
			return nil, err
		}
	}
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "metadata.tenant_id", "match": map[string]any{"value": req.TenantID}},
			},
		},
	}
	payload, _ := json.Marshal(body)
	url := strings.TrimRight(q.Endpoint, "/") + "/collections/" + q.Collection + "/points/search"
	var parsed qdrantResponse
	err = retryWithBackoff(ctx, retryConfig{Attempts: 3, Base: 75 * time.Millisecond, Max: 750 * time.Millisecond}, func() error {
		attemptReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		attemptReq.Header.Set("Content-Type", "application/json")
		resp, err := q.client().Do(attemptReq)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("qdrant search failed: %s", resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(&parsed)
	})
	if err != nil {
		if q.Breaker != nil {
			q.Breaker.ReportFailure()
		}
		return nil, err
	}
	if q.Breaker != nil {
		q.Breaker.ReportSuccess()
	}
	return qdrantCandidates(parsed.Result), nil
}

func (q QdrantVectorSearcher) client() *http.Client {
	if q.Client != nil {
		return q.Client
	}
	return http.DefaultClient
}

type ElasticsearchLexicalSearcher struct {
	Endpoint string
	Index    string
	Client   *http.Client
	Breaker  *CircuitBreaker
}

func NewServiceCircuitBreaker(name string, failureLimit int, openTimeout time.Duration) *CircuitBreaker {
	return NewCircuitBreaker(CircuitBreakerSettings{
		Name: name, FailureLimit: failureLimit, OpenTimeout: openTimeout, HalfOpenLimit: 1,
	})
}

func (e ElasticsearchLexicalSearcher) SearchLexical(ctx context.Context, req QueryRequest, limit int) ([]Candidate, error) {
	if e.Endpoint == "" || e.Index == "" {
		return nil, errors.New("elasticsearch endpoint and index are required")
	}
	if e.Breaker != nil {
		if err := e.Breaker.Allow(); err != nil {
			return nil, err
		}
	}
	body := map[string]any{
		"size": limit,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{{"term": map[string]any{"metadata.tenant_id": req.TenantID}}},
				"must":   []map[string]any{{"multi_match": map[string]any{"query": req.Question, "fields": []string{"text^2", "metadata_prefix"}}}},
			},
		},
	}
	payload, _ := json.Marshal(body)
	url := strings.TrimRight(e.Endpoint, "/") + "/" + e.Index + "/_search"
	var parsed elasticsearchResponse
	err := retryWithBackoff(ctx, retryConfig{Attempts: 3, Base: 75 * time.Millisecond, Max: 750 * time.Millisecond}, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := e.client().Do(httpReq)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("elasticsearch search failed: %s", resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(&parsed)
	})
	if err != nil {
		if e.Breaker != nil {
			e.Breaker.ReportFailure()
		}
		return nil, err
	}
	if e.Breaker != nil {
		e.Breaker.ReportSuccess()
	}
	return elasticsearchCandidates(parsed.Hits.Hits), nil
}

func (e ElasticsearchLexicalSearcher) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

type qdrantResponse struct {
	Result []struct {
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	} `json:"result"`
}

type elasticsearchResponse struct {
	Hits struct {
		Hits []struct {
			Score  float64        `json:"_score"`
			Source map[string]any `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

func qdrantCandidates(points []struct {
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}) []Candidate {
	candidates := make([]Candidate, 0, len(points))
	for i, point := range points {
		candidates = append(candidates, Candidate{Chunk: chunkFromPayload(point.Payload), Rank: i + 1, Score: point.Score})
	}
	return candidates
}

func elasticsearchCandidates(hits []struct {
	Score  float64        `json:"_score"`
	Source map[string]any `json:"_source"`
}) []Candidate {
	candidates := make([]Candidate, 0, len(hits))
	for i, hit := range hits {
		candidates = append(candidates, Candidate{Chunk: chunkFromPayload(hit.Source), Rank: i + 1, Score: hit.Score})
	}
	return candidates
}

func chunkFromPayload(payload map[string]any) Chunk {
	metadata, _ := payload["metadata"].(map[string]any)
	return Chunk{
		TenantID:       stringValue(metadata["tenant_id"]),
		Region:         stringValue(metadata["region"]),
		DocumentID:     stringValue(payload["document_id"]),
		ChunkID:        stringValue(payload["chunk_id"]),
		ChunkHash:      stringValue(payload["chunk_hash"]),
		Page:           intValue(payload["page"], 1),
		Offset:         intValue(payload["offset"], 0),
		Text:           stringValue(payload["text"]),
		RequiredScope:  stringValue(metadata["source_scope"]),
		OwnerACLTags:   stringSliceValue(metadata["owner_acl_tags"]),
		FreshnessScore: floatValue(payload["freshness_score"], 1),
		SoftDeleted:    boolValue(payload["soft_deleted"]),
	}
}

func getRealEmbedding(ctx context.Context, embeddingURL string, timeout time.Duration, breaker *CircuitBreaker, queryText string) ([]float32, error) {
	if embeddingURL == "" {
		embeddingURL = "http://ingestion:8000"
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	payload, _ := json.Marshal(map[string]string{"text": queryText})
	embedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if breaker != nil {
		if err := breaker.Allow(); err != nil {
			return nil, err
		}
	}

	client := &http.Client{Timeout: timeout}
	var parsed struct {
		Embedding []float32 `json:"embedding"`
	}
	err := retryWithBackoff(embedCtx, retryConfig{Attempts: 3, Base: 75 * time.Millisecond, Max: 750 * time.Millisecond}, func() error {
		req, err := http.NewRequestWithContext(embedCtx, http.MethodPost, strings.TrimRight(embeddingURL, "/")+"/embed", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("embedding request failed: %s", resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(&parsed)
	})
	if err != nil {
		if breaker != nil {
			breaker.ReportFailure()
		}
		return nil, err
	}
	if breaker != nil {
		breaker.ReportSuccess()
	}
	if len(parsed.Embedding) == 0 {
		return nil, errors.New("embedding response was empty")
	}
	return parsed.Embedding, nil
}

type HTTPEmbedder struct {
	Endpoint string
	Client   *http.Client
}

func (h HTTPEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if h.Endpoint == "" {
		return nil, errors.New("embedding endpoint is required")
	}
	payload, _ := json.Marshal(map[string]any{"texts": []string{text}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.Endpoint, "/")+"/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request failed: %s", resp.Status)
	}
	var parsed struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Vectors) != 1 {
		return nil, errors.New("embedding response did not contain exactly one vector")
	}
	return parsed.Vectors[0], nil
}

func (h HTTPEmbedder) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func newChunk(tenantID string, region string, documentID string, scope string, owners []string, text string) Chunk {
	hashBytes := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(hashBytes[:])
	return Chunk{
		TenantID:       tenantID,
		Region:         region,
		DocumentID:     documentID,
		ChunkID:        "chk_" + hash[:20],
		ChunkHash:      hash,
		Page:           1,
		Offset:         0,
		Text:           text,
		RequiredScope:  scope,
		OwnerACLTags:   owners,
		FreshnessScore: 1,
	}
}

func tokenize(value string) []string {
	fields := strings.Fields(strings.ToLower(value))
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,:;!?()[]{}\"'")
		if len(field) > 2 {
			terms = append(terms, field)
		}
	}
	return terms
}

func lexicalScore(terms []string, text string) float64 {
	lower := strings.ToLower(text)
	var score float64
	for _, term := range terms {
		if strings.Contains(lower, term) {
			score += 1
		}
	}
	return score
}

func stringValue(value any) string {
	if out, ok := value.(string); ok {
		return out
	}
	return ""
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return typed
	default:
		return nil
	}
}

func aclTagsForUser(userID string) []string {
	normalized := strings.ToLower(strings.TrimSpace(userID))
	tags := []string{normalized}
	for _, separator := range []string{"_", "-", ".", "@"} {
		parts := strings.Split(normalized, separator)
		for _, part := range parts {
			if part != "" {
				tags = append(tags, part)
			}
		}
	}
	return tags
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return fallback
	}
}

func floatValue(value any, fallback float64) float64 {
	if out, ok := value.(float64); ok {
		return out
	}
	return fallback
}

func boolValue(value any) bool {
	if out, ok := value.(bool); ok {
		return out
	}
	return false
}
