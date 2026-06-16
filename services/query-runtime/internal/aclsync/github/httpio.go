package github

import (
	"io"
	"net/http"
)

// readAllAndClose reads the full response body and closes it. Factored out
// so http_client.go stays focused on the GitHub REST shape.
func readAllAndClose(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}
