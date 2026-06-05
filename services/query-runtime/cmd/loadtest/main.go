// Command loadtest seeds a bank-shaped dataset and drives a concurrency load test against a
// running Groundwork query runtime, reporting p50/p95/p99 latency, throughput, and the
// fail-closed rate. It is an operator tool (not part of the service) and talks to everything
// over HTTP, so it can run from any machine against a local or deployed stack.
//
// Two modes:
//
//	-mode=seed   Populate Qdrant with N documents and OpenFGA with grants for N users, so the
//	             load run exercises a realistic mix of authorized + unauthorized queries. The
//	             query runtime must already be running (it provisions the OpenFGA store + model
//	             on its first query); seed finds that store by name and writes tuples.
//	-mode=load   Drive POST /v1/query concurrently with signed end-user JWTs and report stats.
//
// Example:
//
//	go run ./cmd/loadtest -mode=seed  -openfga=http://localhost:8081 -qdrant=http://localhost:6333 \
//	    -tenant=acme -users=500 -docs=2000
//	go run ./cmd/loadtest -mode=load  -runtime=http://localhost:8080 -apikey=$KEY \
//	    -jwt-secret=$SECRET -tenant=acme -users=500 -concurrency=50 -duration=30s
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type config struct {
	mode        string
	runtime     string
	openfga     string
	qdrant      string
	collection  string
	storeName   string
	tenant      string
	region      string
	apiKey      string
	jwtSecret   string
	question    string
	users       int
	docs        int
	dim         int
	concurrency int
	duration    time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.mode, "mode", "load", "seed | load")
	flag.StringVar(&c.runtime, "runtime", "http://localhost:8080", "query-runtime base URL")
	flag.StringVar(&c.openfga, "openfga", "http://localhost:8081", "OpenFGA base URL (seed)")
	flag.StringVar(&c.qdrant, "qdrant", "http://localhost:6333", "Qdrant base URL (seed)")
	flag.StringVar(&c.collection, "collection", "groundwork_chunks", "Qdrant collection")
	flag.StringVar(&c.storeName, "openfga-store", "groundwork_local", "OpenFGA store name")
	flag.StringVar(&c.tenant, "tenant", "acme", "tenant id")
	flag.StringVar(&c.region, "region", "uk", "region of seeded chunks")
	flag.StringVar(&c.apiKey, "apikey", os.Getenv("GROUNDWORK_API_KEY"), "Groundwork API key (load)")
	flag.StringVar(&c.jwtSecret, "jwt-secret", os.Getenv("GROUNDWORK_JWT_HS_SECRET"), "HS256 secret to mint user JWTs (load)")
	flag.StringVar(&c.question, "question", "quarterly finance policy", "query text")
	flag.IntVar(&c.users, "users", 500, "number of users")
	flag.IntVar(&c.docs, "docs", 2000, "number of documents (seed)")
	flag.IntVar(&c.dim, "dim", 384, "embedding dimension (seed)")
	flag.IntVar(&c.concurrency, "concurrency", 50, "concurrent workers (load)")
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "load duration")
	flag.Parse()

	switch c.mode {
	case "seed":
		if err := seed(c); err != nil {
			log.Fatalf("seed failed: %v", err)
		}
	case "load":
		if err := loadTest(c); err != nil {
			log.Fatalf("load failed: %v", err)
		}
	default:
		log.Fatalf("unknown -mode %q (want seed|load)", c.mode)
	}
}

// userID / docID are deterministic so seed and load agree on which users are authorized.
func userID(i int) string { return fmt.Sprintf("user_%d@corp.test", i) }
func docID(i int) string  { return fmt.Sprintf("doc-%d", i) }

// authorizedDoc maps each user to the one document they're granted (a simple, deterministic
// scheme: user i can see doc i mod docs). Users beyond the granted set hit the fail-closed path.
func authorizedDoc(userIdx, docs int) string { return docID(userIdx % docs) }

func seed(c config) error {
	httpc := &http.Client{Timeout: 30 * time.Second}
	// 1) Qdrant: collection + N points (tenant-scoped, region-tagged).
	base := strings.TrimRight(c.qdrant, "/")
	if err := putJSON(httpc, base+"/collections/"+c.collection, map[string]any{
		"vectors": map[string]any{"size": c.dim, "distance": "Cosine"},
	}); err != nil {
		// Ignore "already exists"; otherwise fail.
		if !strings.Contains(strings.ToLower(err.Error()), "already") {
			return fmt.Errorf("create collection: %w", err)
		}
	}
	vec := make([]float64, c.dim)
	for i := range vec {
		vec[i] = 0.1
	}
	points := make([]map[string]any, 0, c.docs)
	for i := 0; i < c.docs; i++ {
		text := fmt.Sprintf("Document %d. Quarterly finance policy and live ACL enforcement notes.", i)
		hash := sha256hex(text)
		points = append(points, map[string]any{
			"id": i + 1, "vector": vec,
			"payload": map[string]any{
				"document_id": docID(i), "chunk_id": "chk_" + hash[:20], "chunk_hash": hash,
				"text": text, "page": 1, "offset": 0, "freshness_score": 1.0, "soft_deleted": false,
				"metadata": map[string]any{"tenant_id": c.tenant, "region": c.region, "source_scope": "SharePoint", "owner_acl_tags": []string{}},
			},
		})
	}
	if err := putJSON(httpc, base+"/collections/"+c.collection+"/points?wait=true", map[string]any{"points": points}); err != nil {
		return fmt.Errorf("upsert points: %w", err)
	}
	log.Printf("seeded %d documents into Qdrant collection %q", c.docs, c.collection)

	// 2) OpenFGA: find the store the runtime provisioned, then write user->document grants.
	storeID, err := openfgaStoreID(httpc, strings.TrimRight(c.openfga, "/"), c.storeName)
	if err != nil {
		return fmt.Errorf("find OpenFGA store %q (start the runtime + run one query to provision it first): %w", c.storeName, err)
	}
	tuples := make([]map[string]string, 0, c.users)
	for i := 0; i < c.users; i++ {
		tuples = append(tuples, map[string]string{
			"user": "user:" + userID(i), "relation": "viewer", "object": "document:" + authorizedDoc(i, c.docs),
		})
	}
	// Write in batches (OpenFGA caps writes per call).
	const batch = 100
	for i := 0; i < len(tuples); i += batch {
		end := i + batch
		if end > len(tuples) {
			end = len(tuples)
		}
		if err := postJSON(httpc, strings.TrimRight(c.openfga, "/")+"/stores/"+storeID+"/write",
			map[string]any{"writes": map[string]any{"tuple_keys": tuples[i:end]}}, nil); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already") {
				return fmt.Errorf("write tuples [%d:%d]: %w", i, end, err)
			}
		}
	}
	log.Printf("granted %d users one document each in OpenFGA store %s", c.users, storeID)
	return nil
}

func loadTest(c config) error {
	if c.jwtSecret == "" || c.apiKey == "" {
		return fmt.Errorf("-jwt-secret and -apikey are required for load mode")
	}
	httpc := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(c.duration)

	var (
		mu        sync.Mutex
		latencies []time.Duration
		total     atomic.Int64
		allowed   atomic.Int64
		denied    atomic.Int64 // fail-closed: 200 but zero citations
		errors    atomic.Int64
		throttled atomic.Int64 // 429
	)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < c.concurrency; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed) + time.Now().UnixNano()))
			for time.Now().Before(deadline) {
				uidx := rng.Intn(c.users)
				tok := mintJWT(c.jwtSecret, userID(uidx))
				t0 := time.Now()
				status, citations, err := postQuery(httpc, c.runtime, c.apiKey, tok, c.question)
				lat := time.Since(t0)
				total.Add(1)
				mu.Lock()
				latencies = append(latencies, lat)
				mu.Unlock()
				switch {
				case err != nil:
					errors.Add(1)
				case status == http.StatusTooManyRequests:
					throttled.Add(1)
				case status != http.StatusOK:
					errors.Add(1)
				case citations > 0:
					allowed.Add(1)
				default:
					denied.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	mu.Lock()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	mu.Unlock()
	n := int64(len(latencies))
	if n == 0 {
		return fmt.Errorf("no requests completed")
	}
	failClosedRate := float64(denied.Load()) / float64(total.Load()) * 100

	fmt.Println("=== Groundwork load test ===")
	fmt.Printf("duration:        %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("concurrency:     %d\n", c.concurrency)
	fmt.Printf("requests:        %d  (%.0f req/s)\n", total.Load(), float64(total.Load())/elapsed.Seconds())
	fmt.Printf("allowed (200,>0):%d\n", allowed.Load())
	fmt.Printf("fail-closed (0): %d  (%.1f%%)\n", denied.Load(), failClosedRate)
	fmt.Printf("429 throttled:   %d\n", throttled.Load())
	fmt.Printf("errors:          %d\n", errors.Load())
	fmt.Printf("latency p50:     %s\n", pct(latencies, 50))
	fmt.Printf("latency p95:     %s\n", pct(latencies, 95))
	fmt.Printf("latency p99:     %s\n", pct(latencies, 99))
	fmt.Printf("latency max:     %s\n", latencies[n-1].Round(time.Millisecond))
	return nil
}

func pct(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx].Round(time.Millisecond)
}

func mintJWT(secret, sub string) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := tok.SignedString([]byte(secret))
	return signed
}

func postQuery(httpc *http.Client, runtimeURL, apiKey, token, question string) (int, int, error) {
	body, _ := json.Marshal(map[string]any{"question": question})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(runtimeURL, "/")+"/v1/query", bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Groundwork-API-Key", apiKey)
	req.Header.Set("X-Groundwork-User-Assertion", token)
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, 0, nil
	}
	var parsed struct {
		Citations []json.RawMessage `json:"citations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return resp.StatusCode, 0, err
	}
	return resp.StatusCode, len(parsed.Citations), nil
}

func openfgaStoreID(httpc *http.Client, base, name string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, base+"/stores", nil)
	resp, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var parsed struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	for _, s := range parsed.Stores {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("store %q not found", name)
}

func putJSON(httpc *http.Client, url string, body any) error {
	return doJSON(httpc, http.MethodPut, url, body, nil)
}
func postJSON(httpc *http.Client, url string, body, out any) error {
	return doJSON(httpc, http.MethodPost, url, body, out)
}

func doJSON(httpc *http.Client, method, url string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> %s: %s", method, url, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
