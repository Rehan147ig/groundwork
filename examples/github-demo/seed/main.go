// Command github-demo-seed is the offline Content Puller for the Acme
// Financial GitHub demo. It does two things, both over HTTP so it shares no
// code with (and never imports the internals of) the query-runtime module —
// the same self-contained pattern as examples/bank-demo/seed:
//
//  1. Content: walk examples/github-demo/repos/<repo>/**/*.md, chunk each
//     file, embed every chunk via the SAME /embed service the runtime uses
//     for queries (so doc and query vectors live in one space and Qdrant
//     nearest-neighbor search is meaningful), and push the points into
//     Qdrant with document_id = "gh:<repo>".
//
//  2. Permissions: write the Acme org's OpenFGA tuples (team memberships +
//     repo viewer grants) into the store the runtime provisioned. The tuple
//     shape is identical to what internal/aclsync/github.Connector emits
//     against a live org, including the deliberate engineering-team ->
//     finance-budget overexposure the Leak Report flags.
//
// document_id "gh:<repo>" is the join key: the connector authorizes
// document:gh:<repo>, the runtime checks document:<chunk.document_id>, and
// the chunks pushed here carry the matching id — so retrieval and
// enforcement line up. Real embeddings (NOT placeholder vectors) are
// load-bearing: the engine is vector-only, so pseudo-vectors would make
// "show me the Q4 budget" land on arbitrary chunks.
//
// Usage:
//
//	go run . \
//	  -qdrant=http://localhost:6333 -openfga=http://localhost:8081 \
//	  -embedding=http://localhost:9000 \
//	  -repos=../repos -tenant=acme-financial -region=US
//
// The query-runtime must have run once so the OpenFGA store exists. Idempotent.
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
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	collectionName = "groundwork_chunks"
	storeName      = "groundwork_local"
	vectorDim      = 384
	chunkMaxRunes  = 600
	chunkMinRunes  = 200
	sourceScope    = "GitHub"
)

// acmeRepos are the five canonical demo repositories. The groundwork-demo-guide
// folder is documentation and is intentionally NOT ingested (no tuples grant
// it; it isn't part of the access-control story).
var acmeRepos = []string{
	"finance-budget",
	"payroll-system",
	"engineering-platform",
	"security-audit",
	"executive-strategy",
}

// acmeTeams maps each team to its single demo member (login). Mirrors the
// connector's MockClient so the offline seed == the live connector output.
var acmeTeams = map[string]string{
	"finance-team":     "alice",
	"engineering-team": "bob",
	"hr-team":          "carol",
	"security-team":    "dave",
	"executive-team":   "eve",
}

// acmeGrants maps each team to the repos it can view. The
// engineering-team -> finance-budget grant is the deliberate overexposure
// (a real misconfiguration the Leak Report surfaces). HR has no grants.
var acmeGrants = map[string][]string{
	"finance-team":     {"finance-budget"},
	"engineering-team": {"payroll-system", "engineering-platform", "finance-budget"},
	"security-team":    {"security-audit"},
	"executive-team":   {"executive-strategy"},
}

func main() {
	qdrantURL := flag.String("qdrant", "http://localhost:6333", "Qdrant base URL")
	openfgaURL := flag.String("openfga", "http://localhost:8081", "OpenFGA base URL")
	embeddingURL := flag.String("embedding", "http://localhost:9000", "embedding service base URL (the SAME one the runtime queries)")
	reposDir := flag.String("repos", "../repos", "github-demo repos root")
	tenantID := flag.String("tenant", "acme-financial", "tenant id")
	region := flag.String("region", "US", "region")
	flag.Parse()

	httpc := &http.Client{Timeout: 30 * time.Second}

	// 1. Qdrant collection
	if err := ensureQdrantCollection(httpc, *qdrantURL); err != nil {
		log.Fatalf("qdrant collection: %v", err)
	}

	// 2. Ingest content with REAL embeddings, document_id = gh:<repo>
	pointID := 1
	totalChunks := 0
	for _, repo := range acmeRepos {
		docID := "gh:" + repo
		repoPath := filepath.Join(*reposDir, repo)
		var points []map[string]any
		err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for ci, chunk := range chunkMarkdown(string(body)) {
				vec, err := embed(httpc, *embeddingURL, chunk)
				if err != nil {
					return fmt.Errorf("embed chunk of %s: %w (is the embedder up at %s?)", repo, err, *embeddingURL)
				}
				hash := sha256hex(chunk)
				points = append(points, map[string]any{
					"id":     pointID,
					"vector": vec,
					"payload": map[string]any{
						"document_id":     docID,
						"chunk_id":        fmt.Sprintf("%s_c%d", docID, pointID),
						"chunk_hash":      hash,
						"text":            chunk,
						"page":            1,
						"offset":          ci * chunkMaxRunes,
						"freshness_score": 1.0,
						"soft_deleted":    false,
						"metadata": map[string]any{
							"tenant_id":      *tenantID,
							"region":         *region,
							"source_scope":   sourceScope,
							"owner_acl_tags": []string{},
						},
					},
				})
				pointID++
			}
			return nil
		})
		if err != nil {
			log.Fatalf("walk %s: %v", repo, err)
		}
		if err := upsertPoints(httpc, *qdrantURL, points); err != nil {
			log.Fatalf("upsert %s: %v", repo, err)
		}
		totalChunks += len(points)
		log.Printf("  ingested %s (%d chunks) as %s", repo, len(points), docID)
	}
	log.Printf("Qdrant: %d repos, %d chunks", len(acmeRepos), totalChunks)

	// 3. OpenFGA tuples (members + viewer grants), matching the connector.
	storeID, err := openfgaStoreID(httpc, *openfgaURL, storeName)
	if err != nil {
		log.Fatalf("openfga store: %v (run the query-runtime once before seeding)", err)
	}
	tuples := buildTuples()
	if err := writeTuples(httpc, *openfgaURL, storeID, tuples); err != nil {
		log.Fatalf("openfga tuples: %v", err)
	}
	log.Printf("OpenFGA: %d tuples written to store %s", len(tuples), storeID)
	log.Println("done. github-demo stack ready.")
}

// buildTuples emits the Acme org's OpenFGA tuples: user:<login> member
// group:<team>, and group:<team>#member viewer document:gh:<repo>.
// Deterministic order so re-runs are byte-stable.
func buildTuples() []map[string]string {
	var tuples []map[string]string

	teams := make([]string, 0, len(acmeTeams))
	for t := range acmeTeams {
		teams = append(teams, t)
	}
	sort.Strings(teams)

	for _, team := range teams {
		tuples = append(tuples, map[string]string{
			"user": "user:" + acmeTeams[team], "relation": "member", "object": "group:" + team,
		})
		grants := append([]string(nil), acmeGrants[team]...)
		sort.Strings(grants)
		for _, repo := range grants {
			tuples = append(tuples, map[string]string{
				"user": "group:" + team + "#member", "relation": "viewer", "object": "document:gh:" + repo,
			})
		}
	}
	return tuples
}

// ---------- markdown chunking ----------

func chunkMarkdown(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []string
	var cur strings.Builder
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if cur.Len()+len(p)+2 > chunkMaxRunes && cur.Len() >= chunkMinRunes {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(cur.String()))
	}
	if len(chunks) == 0 {
		chunks = []string{text}
	}
	return chunks
}

// ---------- embedding (real /embed calls) ----------

func embed(httpc *http.Client, embeddingURL, text string) ([]float32, error) {
	payload, _ := json.Marshal(map[string]string{"text": text})
	url := strings.TrimRight(embeddingURL, "/") + "/embed"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed %s -> %s: %s", url, resp.Status, strings.TrimSpace(string(data)))
	}
	var parsed struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Embedding) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return parsed.Embedding, nil
}

// ---------- Qdrant ----------

func ensureQdrantCollection(httpc *http.Client, base string) error {
	url := strings.TrimRight(base, "/") + "/collections/" + collectionName
	body := map[string]any{"vectors": map[string]any{"size": vectorDim, "distance": "Cosine"}}
	if err := doJSON(httpc, http.MethodPut, url, body, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			return nil
		}
		return err
	}
	return nil
}

func upsertPoints(httpc *http.Client, base string, points []map[string]any) error {
	if len(points) == 0 {
		return nil
	}
	url := strings.TrimRight(base, "/") + "/collections/" + collectionName + "/points?wait=true"
	return doJSON(httpc, http.MethodPut, url, map[string]any{"points": points}, nil)
}

// ---------- OpenFGA ----------

func openfgaStoreID(httpc *http.Client, base, name string) (string, error) {
	url := strings.TrimRight(base, "/") + "/stores"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
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

func writeTuples(httpc *http.Client, base, storeID string, tuples []map[string]string) error {
	const batch = 50
	url := strings.TrimRight(base, "/") + "/stores/" + storeID + "/write"
	for i := 0; i < len(tuples); i += batch {
		end := i + batch
		if end > len(tuples) {
			end = len(tuples)
		}
		body := map[string]any{"writes": map[string]any{"tuple_keys": tuples[i:end]}}
		if err := doJSON(httpc, http.MethodPost, url, body, nil); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already") {
				continue
			}
			return fmt.Errorf("write tuples [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// ---------- HTTP helper ----------

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
