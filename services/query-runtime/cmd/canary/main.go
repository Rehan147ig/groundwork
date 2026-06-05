// Command canary is a production smoke test for a deployed Groundwork runtime. It verifies the
// core guarantees against a live URL and exits non-zero if any fail, so it can run on a
// schedule (cron / CI / a scheduled agent) and alert when something regresses.
//
// Checks:
//  1. /healthz returns ok.
//  2. FAIL-CLOSED: a query as an unauthorized end-user returns ZERO documents. If it ever
//     returns documents, that's a leak — the canary fails loudly.
//  3. (optional, -authorized-user) the authorized path returns documents, proving the positive
//     path works end to end.
//
// Example:
//
//	go run ./cmd/canary -runtime=https://gw.example.com -apikey=$KEY -jwt-secret=$SECRET \
//	    -authorized-user=alice@corp.test
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	runtime := flag.String("runtime", "http://localhost:8080", "query-runtime base URL")
	apiKey := flag.String("apikey", os.Getenv("GROUNDWORK_API_KEY"), "Groundwork API key")
	jwtSecret := flag.String("jwt-secret", os.Getenv("GROUNDWORK_JWT_HS_SECRET"), "HS256 secret to mint user JWTs")
	question := flag.String("question", "quarterly finance policy", "query text")
	unauthorizedUser := flag.String("unauthorized-user", "canary-nobody@nowhere.test", "a user expected to have NO access")
	authorizedUser := flag.String("authorized-user", "", "optional: a user expected to have access")
	flag.Parse()

	if *jwtSecret == "" || *apiKey == "" {
		fmt.Fprintln(os.Stderr, "FATAL: -jwt-secret and -apikey are required")
		os.Exit(2)
	}
	httpc := &http.Client{Timeout: 15 * time.Second}
	failed := false
	check := func(name string, ok bool, detail string) {
		if ok {
			fmt.Printf("PASS  %s\n", name)
		} else {
			fmt.Printf("FAIL  %s — %s\n", name, detail)
			failed = true
		}
	}

	// 1) health
	if status, _, err := get(httpc, strings.TrimRight(*runtime, "/")+"/healthz"); err != nil {
		check("health", false, err.Error())
	} else {
		check("health", status == http.StatusOK, fmt.Sprintf("status %d", status))
	}

	// 2) fail-closed: unauthorized user must get zero documents
	status, citations, err := query(httpc, *runtime, *apiKey, mintJWT(*jwtSecret, *unauthorizedUser), *question)
	switch {
	case err != nil:
		check("fail-closed (unauthorized → 0 docs)", false, err.Error())
	case status != http.StatusOK:
		// A non-200 (e.g. identity_unresolved 403) is also a closed door — acceptable.
		check("fail-closed (unauthorized → 0 docs)", citations == 0, fmt.Sprintf("status %d, citations %d", status, citations))
	default:
		check("fail-closed (unauthorized → 0 docs)", citations == 0,
			fmt.Sprintf("LEAK: unauthorized user received %d documents", citations))
	}

	// 3) optional authorized path
	if *authorizedUser != "" {
		status, citations, err := query(httpc, *runtime, *apiKey, mintJWT(*jwtSecret, *authorizedUser), *question)
		switch {
		case err != nil:
			check("authorized → documents", false, err.Error())
		case status != http.StatusOK:
			check("authorized → documents", false, fmt.Sprintf("status %d", status))
		default:
			check("authorized → documents", citations > 0, "authorized user received 0 documents")
		}
	}

	if failed {
		fmt.Println("CANARY: FAILED")
		os.Exit(1)
	}
	fmt.Println("CANARY: OK")
}

func mintJWT(secret, sub string) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := tok.SignedString([]byte(secret))
	return signed
}

func get(httpc *http.Client, url string) (int, []byte, error) {
	resp, err := httpc.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

func query(httpc *http.Client, runtimeURL, apiKey, token, question string) (int, int, error) {
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
