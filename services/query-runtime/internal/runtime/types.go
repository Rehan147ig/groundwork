package runtime

import "time"

type Config struct {
	Addr                  string
	QueryTimeout          time.Duration
	ACLTimeout            time.Duration
	BackendHTTPTimeout    time.Duration
	EmbeddingTimeout      time.Duration
	CircuitOpenTimeout    time.Duration
	CircuitFailureLimit   int
	CircuitHalfOpenLimit  int
	OpenFGAURL            string
	OpenFGAStoreName      string
	OpenFGATimeout        time.Duration
	DatabaseURL           string
	BootstrapAPIKey       string
	BootstrapTenantID     string
	BootstrapTenantRegion string
	IDKThreshold          float64
	VectorWeight          float64
	KeywordWeight         float64
}

type QueryRequest struct {
	TenantID     string   `json:"-"`
	UserID       string   `json:"user_id"`
	Region       string   `json:"-"`
	Question     string   `json:"question"`
	SourceScopes []string `json:"source_scopes,omitempty"`
	IDKThreshold float64  `json:"idk_threshold,omitempty"`
}

type CreateAPIKeyRequest struct {
	Name         string   `json:"name"`
	Scopes       []string `json:"scopes"`
	RateLimitRPM int      `json:"rate_limit_rpm,omitempty"`
}

type CreateAPIKeyResponse struct {
	ID           int64     `json:"id"`
	Key          string    `json:"key"`
	KeyPrefix    string    `json:"key_prefix"`
	Name         string    `json:"name"`
	TenantID     string    `json:"tenant_id"`
	Region       string    `json:"region"`
	Scopes       []string  `json:"scopes"`
	RateLimitRPM int       `json:"rate_limit_rpm"`
	CreatedAt    time.Time `json:"created_at"`
}

type RevokeAPIKeyResponse struct {
	ID      int64  `json:"id"`
	Revoked bool   `json:"revoked"`
	Status  string `json:"status"`
}

type RotateAPIKeyResponse struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	KeyPrefix string    `json:"key_prefix"`
	RotatedAt time.Time `json:"rotated_at"`
	Status    string    `json:"status"`
}

type QueryResponse struct {
	Answer     string       `json:"answer"`
	Confidence float64      `json:"confidence"`
	Citations  []Citation   `json:"citations"`
	Trace      RuntimeTrace `json:"trace"`
}

type Citation struct {
	DocumentID     string  `json:"document_id"`
	ChunkID        string  `json:"chunk_id"`
	ChunkHash      string  `json:"chunk_hash"`
	Page           int     `json:"page"`
	Offset         int     `json:"offset"`
	Text           string  `json:"text"`
	Score          float64 `json:"score"`
	FreshnessScore float64 `json:"freshness_score"`
}

type Chunk struct {
	TenantID       string
	Region         string
	DocumentID     string
	ChunkID        string
	ChunkHash      string
	Page           int
	Offset         int
	Text           string
	RequiredScope  string
	OwnerACLTags   []string
	FreshnessScore float64
	SoftDeleted    bool
}

type Candidate struct {
	Chunk Chunk
	Rank  int
	Score float64
}

type RuntimeTrace struct {
	TraceID            string           `json:"trace_id"`
	TenantID           string           `json:"tenant_id"`
	UserID             string           `json:"user_id"`
	Region             string           `json:"region"`
	StartedAt          time.Time        `json:"started_at"`
	LatencyMs          int64            `json:"latency_ms"`
	VectorCandidates   int              `json:"vector_candidates"`
	KeywordCandidates  int              `json:"keyword_candidates"`
	BlockedByACL       int              `json:"blocked_by_acl"`
	BlockedByResidency int              `json:"blocked_by_residency"`
	RerankedCandidates int              `json:"reranked_candidates"`
	DecisionMode       string           `json:"decision_mode"`
	ShadowMode         bool             `json:"shadow_mode,omitempty"`
	WouldBlockByACL    int              `json:"would_block_by_acl,omitempty"`
	FailureStage       string           `json:"failure_stage,omitempty"`
	ErrorCode          string           `json:"error_code,omitempty"`
	ErrorMessage       string           `json:"error_message,omitempty"`
	AccessDecisions    []AccessDecision `json:"access_decisions"`
	ImmutableDigest    string           `json:"immutable_digest"`
}

type AccessDecision struct {
	ChunkID       string `json:"chunk_id"`
	ChunkHash     string `json:"chunk_hash"`
	DocumentID    string `json:"document_id"`
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
	Region        string `json:"region"`
	RequiredScope string `json:"required_scope"`
}
