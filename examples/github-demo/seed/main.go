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
	"regexp"
	"strings"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/github"
)

const (
	collectionName = "groundwork_chunks"
	storeName      = "groundwork_local"
	vectorDim      = 384
	chunkMaxRunes  = 600
	chunkMinRunes  = 200
)

func main() {
	var (
		qdrantURL  = flag.String("qdrant", "http://localhost:6333", "Qdrant REST API URL")
		openfgaURL = flag.String("openfga", "http://localhost:8081", "OpenFGA REST API URL")
		reposDir   = flag.String("repos", "./examples/github-demo/repos", "Path to generated github repos")
		tenantID   = flag.String("tenant", "acme-financial", "Tenant ID for the data")
		region     = flag.String("region", "us", "Data residency region")
	)
	flag.Parse()

	log.Printf("Starting GitHub offline seeder for tenant %q", *tenantID)

	// 1. Sync permissions via the Mock GitHub Connector
	log.Printf("Syncing permissions to OpenFGA...")
	mockClient := github.NewMockClient()
	connector := github.NewConnector(mockClient, "acme-financial", nil)
	
	ps, err := connector.Snapshot(context.Background(), *tenantID)
	if err != nil {
		log.Fatalf("Failed to snapshot permissions: %v", err)
	}

	tuples := aclsync.PermissionSetToTuples(ps)
	log.Printf("Generated %d tuples from GitHub mapping", len(tuples))

	fgaSink := aclsync.NewOpenFGASink(*openfgaURL, storeName)
	if err := fgaSink.WriteTuples(context.Background(), *tenantID, tuples); err != nil {
		log.Fatalf("Failed to write tuples to OpenFGA: %v", err)
	}
	log.Printf("Successfully wrote tuples to OpenFGA")

	// 2. Index Content
	log.Printf("Chunking and indexing repository content from %s...", *reposDir)

	var allChunks []map[string]any

	err = filepath.Walk(*reposDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		rel, _ := filepath.Rel(*reposDir, path)
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		repoName := parts[0]

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		docID := "gh:" + repoName
		chunks := chunkMarkdown(string(content))

		for i, text := range chunks {
			hash := sha256.Sum256([]byte(text))
			chunkHash := hex.EncodeToString(hash[:])
			chunkID := fmt.Sprintf("%s_c%d", docID, i+1)

			// Fast-path pseudo-embedding for the offline seeder (matches Python dummy embedder)
			// A real seeder would call the /embed endpoint.
			vec := make([]float32, vectorDim)
			for j := 0; j < len(text) && j < vectorDim; j++ {
				vec[j] = float32(text[j]) / 255.0
			}

			payload := map[string]any{
				"document_id": docID,
				"chunk_id":    chunkID,
				"chunk_hash":  chunkHash,
				"tenant_id":   *tenantID,
				"region":      *region,
				"text":        text,
				"source_url":  fmt.Sprintf("https://github.com/acme-financial/%s/blob/main/%s", repoName, strings.ReplaceAll(parts[1], "\\", "/")),
			}

			allChunks = append(allChunks, map[string]any{
				"id":      chunkHash, // use hash as point UUID equivalent
				"vector":  vec,
				"payload": payload,
			})
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Failed to read repos: %v", err)
	}

	log.Printf("Generated %d chunks. Pushing to Qdrant...", len(allChunks))
	pushToQdrant(*qdrantURL, allChunks)
	log.Printf("Seeding complete.")
}

func chunkMarkdown(text string) []string {
	var chunks []string
	paragraphs := regexp.MustCompile(`\n\s*\n`).Split(text, -1)
	var current strings.Builder

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if current.Len()+len(p) > chunkMaxRunes && current.Len() >= chunkMinRunes {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func pushToQdrant(url string, points []map[string]any) {
	// Simple UUID generation for Qdrant points based on hashes
	formattedPoints := make([]map[string]any, len(points))
	for i, p := range points {
		hash := p["id"].(string)
		uuid := fmt.Sprintf("%s-%s-%s-%s-%s", hash[0:8], hash[8:12], hash[12:16], hash[16:20], hash[20:32])
		formattedPoints[i] = map[string]any{
			"id":      uuid,
			"vector":  p["vector"],
			"payload": p["payload"],
		}
	}

	body := map[string]any{
		"points": formattedPoints,
	}
	b, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/collections/%s/points?wait=true", url, collectionName), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("Qdrant push failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		out, _ := io.ReadAll(resp.Body)
		log.Fatalf("Qdrant returned %d: %s", resp.StatusCode, string(out))
	}
}
