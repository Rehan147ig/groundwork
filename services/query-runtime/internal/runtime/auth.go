package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidAPIKey = errors.New("invalid api key")
var ErrAPIKeyManagementUnavailable = errors.New("api key management unavailable")

type TenantContext struct {
	TenantID     string
	Region       string
	KeyName      string
	Scopes       []string
	KeyID        int64
	RateLimitRPM int // per-key requests/minute budget; 0 means unlimited
}

type APIKeyResolver interface {
	Resolve(ctx context.Context, rawKey string) (TenantContext, error)
}

type APIKeyManager interface {
	Create(ctx context.Context, tenant TenantContext, req CreateAPIKeyRequest) (CreateAPIKeyResponse, error)
	Rotate(ctx context.Context, tenant TenantContext, id int64) (RotateAPIKeyResponse, error)
	Revoke(ctx context.Context, tenant TenantContext, id int64) (bool, error)
}

type MemoryAPIKeyResolver struct {
	mu   sync.RWMutex
	keys map[string]TenantContext
	next int64
}

func NewMemoryAPIKeyResolver(rawKey string, tenant TenantContext) *MemoryAPIKeyResolver {
	resolver := &MemoryAPIKeyResolver{keys: map[string]TenantContext{}, next: 1}
	if rawKey != "" {
		tenant.KeyID = resolver.next
		resolver.next++
		if len(tenant.Scopes) == 0 {
			tenant.Scopes = []string{"query", "admin"}
		}
		resolver.keys[hashAPIKey(rawKey)] = tenant
	}
	return resolver
}

func (m *MemoryAPIKeyResolver) Resolve(ctx context.Context, rawKey string) (TenantContext, error) {
	select {
	case <-ctx.Done():
		return TenantContext{}, ctx.Err()
	default:
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	tenant, ok := m.keys[hashAPIKey(rawKey)]
	if !ok || tenant.TenantID == "" || tenant.Region == "" {
		return TenantContext{}, ErrInvalidAPIKey
	}
	if len(tenant.Scopes) == 0 {
		tenant.Scopes = []string{"query"}
	}
	return tenant, nil
}

func (m *MemoryAPIKeyResolver) Create(ctx context.Context, tenant TenantContext, req CreateAPIKeyRequest) (CreateAPIKeyResponse, error) {
	select {
	case <-ctx.Done():
		return CreateAPIKeyResponse{}, ctx.Err()
	default:
	}
	rawKey, prefix, err := generateAPIKey()
	if err != nil {
		return CreateAPIKeyResponse{}, err
	}
	req = normalizeCreateAPIKeyRequest(req)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	id := m.next
	m.keys[hashAPIKey(rawKey)] = TenantContext{
		TenantID:     tenant.TenantID,
		Region:       tenant.Region,
		KeyName:      req.Name,
		Scopes:       req.Scopes,
		KeyID:        id,
		RateLimitRPM: req.RateLimitRPM,
	}
	return CreateAPIKeyResponse{
		ID:           id,
		Key:          rawKey,
		KeyPrefix:    prefix,
		Name:         req.Name,
		TenantID:     tenant.TenantID,
		Region:       tenant.Region,
		Scopes:       req.Scopes,
		RateLimitRPM: req.RateLimitRPM,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func (m *MemoryAPIKeyResolver) Revoke(ctx context.Context, tenant TenantContext, id int64) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for keyHash, current := range m.keys {
		if current.KeyID == id && current.TenantID == tenant.TenantID {
			delete(m.keys, keyHash)
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryAPIKeyResolver) Rotate(ctx context.Context, tenant TenantContext, id int64) (RotateAPIKeyResponse, error) {
	select {
	case <-ctx.Done():
		return RotateAPIKeyResponse{}, ctx.Err()
	default:
	}
	rawKey, prefix, err := generateAPIKey()
	if err != nil {
		return RotateAPIKeyResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for keyHash, current := range m.keys {
		if current.KeyID == id && current.TenantID == tenant.TenantID {
			delete(m.keys, keyHash)
			m.keys[hashAPIKey(rawKey)] = current
			return RotateAPIKeyResponse{ID: id, Key: rawKey, KeyPrefix: prefix, RotatedAt: time.Now().UTC(), Status: "rotated"}, nil
		}
	}
	return RotateAPIKeyResponse{}, ErrInvalidAPIKey
}

type PostgresAPIKeyResolver struct {
	pool *pgxpool.Pool
}

func NewPostgresAPIKeyResolver(ctx context.Context, databaseURL string, bootstrapKey string, bootstrapTenant TenantContext) (*PostgresAPIKeyResolver, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	resolver := &PostgresAPIKeyResolver{pool: pool}
	if err := resolver.bootstrap(ctx, bootstrapKey, bootstrapTenant); err != nil {
		pool.Close()
		return nil, err
	}
	return resolver, nil
}

func (p *PostgresAPIKeyResolver) Resolve(ctx context.Context, rawKey string) (TenantContext, error) {
	if strings.TrimSpace(rawKey) == "" {
		return TenantContext{}, ErrInvalidAPIKey
	}
	var tenant TenantContext
	var scopesText string
	err := p.pool.QueryRow(ctx, `
		SELECT id, tenant_id, region, name, scopes, rate_limit_rpm
		FROM api_keys
		WHERE key_hash = $1 AND active = TRUE AND revoked_at IS NULL
	`, hashAPIKey(rawKey)).Scan(&tenant.KeyID, &tenant.TenantID, &tenant.Region, &tenant.KeyName, &scopesText, &tenant.RateLimitRPM)
	if err != nil {
		prefix := apiKeyPrefix(rawKey)
		if prefix == "" {
			return TenantContext{}, ErrInvalidAPIKey
		}
		rows, err := p.pool.Query(ctx, `
			SELECT id, key_hash, tenant_id, region, name, scopes, rate_limit_rpm
			FROM api_keys
			WHERE key_prefix = $1 AND active = TRUE AND revoked_at IS NULL
		`, prefix)
		if err != nil {
			return TenantContext{}, ErrInvalidAPIKey
		}
		defer rows.Close()
		var keyHash string
		for rows.Next() {
			if err := rows.Scan(&tenant.KeyID, &keyHash, &tenant.TenantID, &tenant.Region, &tenant.KeyName, &scopesText, &tenant.RateLimitRPM); err != nil {
				continue
			}
			if bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(rawKey)) == nil {
				tenant.Scopes = splitScopes(scopesText)
				_, _ = p.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, tenant.KeyID)
				return tenant, nil
			}
		}
		return TenantContext{}, ErrInvalidAPIKey
	}
	tenant.Scopes = splitScopes(scopesText)
	_, _ = p.pool.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, tenant.KeyID)
	return tenant, nil
}

func (p *PostgresAPIKeyResolver) bootstrap(ctx context.Context, bootstrapKey string, tenant TenantContext) error {
	if _, err := p.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS api_keys (
			id BIGSERIAL PRIMARY KEY,
			key_hash TEXT UNIQUE NOT NULL,
			key_prefix TEXT,
			tenant_id TEXT NOT NULL,
			region TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT 'default',
			scopes TEXT NOT NULL DEFAULT 'query',
			rate_limit_rpm INTEGER NOT NULL DEFAULT 60,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_used_at TIMESTAMPTZ,
			revoked_at TIMESTAMPTZ
		)
	`); err != nil {
		return err
	}
	for _, statement := range []string{
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix TEXT`,
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scopes TEXT NOT NULL DEFAULT 'query'`,
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS rate_limit_rpm INTEGER NOT NULL DEFAULT 60`,
		`ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS api_keys_key_prefix_idx ON api_keys (key_prefix)`,
		`CREATE INDEX IF NOT EXISTS api_keys_tenant_active_idx ON api_keys (tenant_id, active)`,
	} {
		if _, err := p.pool.Exec(ctx, statement); err != nil {
			return err
		}
	}
	if bootstrapKey == "" || tenant.TenantID == "" || tenant.Region == "" {
		return nil
	}
	if len(tenant.Scopes) == 0 {
		tenant.Scopes = []string{"query", "admin"}
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO api_keys (key_hash, key_prefix, tenant_id, region, name, scopes, rate_limit_rpm, active)
		VALUES ($1, NULL, $2, $3, $4, $5, 600, TRUE)
		ON CONFLICT (key_hash) DO UPDATE
		SET tenant_id = EXCLUDED.tenant_id,
			region = EXCLUDED.region,
			name = EXCLUDED.name,
			scopes = EXCLUDED.scopes,
			rate_limit_rpm = EXCLUDED.rate_limit_rpm,
			active = TRUE,
			revoked_at = NULL
	`, hashAPIKey(bootstrapKey), tenant.TenantID, tenant.Region, tenant.KeyName, strings.Join(tenant.Scopes, ","))
	return err
}

func (p *PostgresAPIKeyResolver) Create(ctx context.Context, tenant TenantContext, req CreateAPIKeyRequest) (CreateAPIKeyResponse, error) {
	rawKey, prefix, err := generateAPIKey()
	if err != nil {
		return CreateAPIKeyResponse{}, err
	}
	req = normalizeCreateAPIKeyRequest(req)
	keyHash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		return CreateAPIKeyResponse{}, err
	}
	var resp CreateAPIKeyResponse
	err = p.pool.QueryRow(ctx, `
		INSERT INTO api_keys (key_hash, key_prefix, tenant_id, region, name, scopes, rate_limit_rpm, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		RETURNING id, created_at
	`, string(keyHash), prefix, tenant.TenantID, tenant.Region, req.Name, strings.Join(req.Scopes, ","), req.RateLimitRPM).Scan(&resp.ID, &resp.CreatedAt)
	if err != nil {
		return CreateAPIKeyResponse{}, err
	}
	resp.Key = rawKey
	resp.KeyPrefix = prefix
	resp.Name = req.Name
	resp.TenantID = tenant.TenantID
	resp.Region = tenant.Region
	resp.Scopes = req.Scopes
	resp.RateLimitRPM = req.RateLimitRPM
	return resp, nil
}

func (p *PostgresAPIKeyResolver) Rotate(ctx context.Context, tenant TenantContext, id int64) (RotateAPIKeyResponse, error) {
	rawKey, prefix, err := generateAPIKey()
	if err != nil {
		return RotateAPIKeyResponse{}, err
	}
	keyHash, err := bcrypt.GenerateFromPassword([]byte(rawKey), bcrypt.DefaultCost)
	if err != nil {
		return RotateAPIKeyResponse{}, err
	}
	tag, err := p.pool.Exec(ctx, `
		UPDATE api_keys
		SET key_hash = $1,
			key_prefix = $2,
			last_used_at = NULL,
			revoked_at = NULL,
			active = TRUE
		WHERE id = $3 AND tenant_id = $4 AND active = TRUE AND revoked_at IS NULL
	`, string(keyHash), prefix, id, tenant.TenantID)
	if err != nil {
		return RotateAPIKeyResponse{}, err
	}
	if tag.RowsAffected() == 0 {
		return RotateAPIKeyResponse{}, ErrInvalidAPIKey
	}
	return RotateAPIKeyResponse{ID: id, Key: rawKey, KeyPrefix: prefix, RotatedAt: time.Now().UTC(), Status: "rotated"}, nil
}

func (p *PostgresAPIKeyResolver) Revoke(ctx context.Context, tenant TenantContext, id int64) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE api_keys
		SET active = FALSE, revoked_at = now()
		WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, id, tenant.TenantID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (p *PostgresAPIKeyResolver) Close() {
	p.pool.Close()
}

func BuildAPIKeyResolver(ctx context.Context, cfg Config) (APIKeyResolver, func(), error) {
	tenant := TenantContext{
		TenantID: cfg.BootstrapTenantID,
		Region:   cfg.BootstrapTenantRegion,
		KeyName:  "local-bootstrap",
		Scopes:   []string{"query", "admin"},
	}
	if tenant.TenantID == "" {
		tenant.TenantID = "acme"
	}
	if tenant.Region == "" {
		tenant.Region = "US"
	}
	if cfg.DatabaseURL == "" {
		if strings.ToLower(os.Getenv("ALLOW_MEMORY_API_KEYS")) != "true" {
			return nil, nil, errors.New("DATABASE_URL is required for persistent API keys; set ALLOW_MEMORY_API_KEYS=true only for local tests")
		}
		return NewMemoryAPIKeyResolver(cfg.BootstrapAPIKey, tenant), func() {}, nil
	}
	resolver, err := NewPostgresAPIKeyResolver(ctx, cfg.DatabaseURL, cfg.BootstrapAPIKey, tenant)
	if err != nil {
		return nil, nil, err
	}
	return resolver, resolver.Close, nil
}

func extractAPIKey(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("Authorization")); value != "" {
		prefix := "Bearer "
		if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Groundwork-API-Key"))
}

func hashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

func hasScope(tenant TenantContext, scope string) bool {
	for _, current := range tenant.Scopes {
		if current == scope || current == "admin" {
			return true
		}
	}
	return false
}

func generateAPIKey() (string, string, error) {
	var prefixBytes [4]byte
	var secretBytes [24]byte
	if _, err := rand.Read(prefixBytes[:]); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return "", "", err
	}
	prefix := hex.EncodeToString(prefixBytes[:])
	return fmt.Sprintf("gw_live_%s_%s", prefix, hex.EncodeToString(secretBytes[:])), prefix, nil
}

func apiKeyPrefix(rawKey string) string {
	parts := strings.Split(rawKey, "_")
	if len(parts) >= 3 && parts[0] == "gw" && parts[1] == "live" {
		return parts[2]
	}
	return ""
}

func normalizeCreateAPIKeyRequest(req CreateAPIKeyRequest) CreateAPIKeyRequest {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "generated"
	}
	if req.RateLimitRPM <= 0 {
		req.RateLimitRPM = 60
	}
	scopes := make([]string, 0, len(req.Scopes))
	seen := map[string]bool{}
	for _, scope := range req.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		scopes = append(scopes, scope)
	}
	if len(scopes) == 0 {
		scopes = []string{"query"}
	}
	req.Scopes = scopes
	return req
}

func splitScopes(scopesText string) []string {
	parts := strings.Split(scopesText, ",")
	scopes := make([]string, 0, len(parts))
	for _, scope := range parts {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return []string{"query"}
	}
	return scopes
}
