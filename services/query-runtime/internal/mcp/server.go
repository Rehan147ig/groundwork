// Package mcp implements a Model Context Protocol (MCP) server that exposes
// the Groundwork Engine as a tool for AI agents (Claude Desktop, autonomous bots, etc.).
//
// The MCP server speaks JSON-RPC 2.0 over stdio (stdin/stdout), which is the
// standard transport for local MCP tool servers.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"
)

// Server is the MCP server that wraps the Groundwork Engine.
type Server struct {
	engine    *engine.Engine
	tenantID  string
	region    string
	verifier  runtime.IdentityVerifier
	allowDemo bool
	writer    io.Writer
	mu        sync.Mutex
}

// NewServer creates an MCP server bound to a specific tenant context.
// In production, the tenant context comes from the API key used to launch the MCP
// process, and the effective end-user is derived from a verified user_token (OIDC/JWT).
// allowDemo permits a raw user_id only when ALLOW_DEMO_IDENTITY=true.
func NewServer(eng *engine.Engine, tenantID, region string, verifier runtime.IdentityVerifier, allowDemo bool) *Server {
	return &Server{
		engine:    eng,
		tenantID:  tenantID,
		region:    region,
		verifier:  verifier,
		allowDemo: allowDemo,
		writer:    os.Stdout,
	}
}

// --- JSON-RPC 2.0 types ---

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP protocol types ---

type mcpInitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      mcpServerInfo  `json:"serverInfo"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// --- Tool input types ---

type groundworkSearchArgs struct {
	UserID    string `json:"user_id"`
	UserToken string `json:"user_token"`
	Question  string `json:"question"`
}

// Run starts the MCP server, reading JSON-RPC messages from stdin and writing
// responses to stdout. It blocks until stdin is closed or an error occurs.
func (s *Server) Run(ctx context.Context) error {
	log.Println("[mcp] Groundwork MCP server started (stdio transport)")
	scanner := bufio.NewScanner(os.Stdin)
	// MCP messages can be large; allow up to 1MB per line
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req jsonrpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "parse error")
			continue
		}
		s.handleRequest(ctx, req)
	}
	return scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req jsonrpcRequest) {
	// stdio transport: tenant/region come from the process's bootstrap context, and the
	// identity token (if any) travels inside the tool arguments (no out-of-band header).
	if resp, ok := s.dispatch(ctx, s.tenantID, s.region, "", req); ok {
		s.send(resp)
	}
}

// dispatch executes a single JSON-RPC request against the shared MCP tool registry
// and the one Engine.Execute path, returning the response and whether one should be
// sent (notifications such as "initialized" produce no response). It is
// transport-agnostic: both the stdio loop and the HTTP /mcp endpoint call it.
//
// tenantID/region are supplied by the transport from the API-key context and are NEVER
// taken from the caller's arguments. assertionToken is an out-of-band identity token
// (e.g. the X-Groundwork-User-Assertion HTTP header) that, when present, takes
// precedence over the user_token tool argument.
func (s *Server) dispatch(ctx context.Context, tenantID, region, assertionToken string, req jsonrpcRequest) (jsonrpcResponse, bool) {
	switch req.Method {
	case "initialize":
		return okResponse(req.ID, initializeResult()), true
	case "initialized":
		// Client acknowledgement — no response.
		return jsonrpcResponse{}, false
	case "tools/list":
		return okResponse(req.ID, map[string]any{"tools": groundworkTools()}), true
	case "tools/call":
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResponse(req.ID, -32602, "invalid params"), true
		}
		if params.Name != "groundwork_search" {
			return errResponse(req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name)), true
		}
		result, rpcErr := s.executeSearch(ctx, tenantID, region, assertionToken, params.Arguments)
		if rpcErr != nil {
			return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return okResponse(req.ID, result), true
	case "ping":
		return okResponse(req.ID, map[string]string{}), true
	default:
		return errResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method)), true
	}
}

func okResponse(id any, result any) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResponse(id any, code int, message string) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: message}}
}

func initializeResult() mcpInitializeResult {
	return mcpInitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
		ServerInfo: mcpServerInfo{
			Name:    "groundwork",
			Version: "1.0.0",
		},
	}
}

func groundworkTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "groundwork_search",
			Description: "Search enterprise documents with live permission enforcement. Returns only documents the specified user is authorized to view. All unauthorized documents are automatically stripped and the interaction is cryptographically logged.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"user_token": map[string]any{
						"type":        "string",
						"description": "A signed end-user identity assertion (OIDC/JWT). Groundwork verifies it and derives the effective user from the sub/email/preferred_username claims, then checks that user's live permissions against every retrieved document. Required in production.",
					},
					"user_id": map[string]any{
						"type":        "string",
						"description": "Demo/dev only: a raw user identifier, honored ONLY when ALLOW_DEMO_IDENTITY=true. In production this is ignored in favor of the verified user_token.",
					},
					"question": map[string]any{
						"type":        "string",
						"description": "The natural language question to search for in the enterprise knowledge base.",
					},
				},
				"required": []string{"question"},
			},
		},
	}
}

// handleGroundworkSearch is the stdio convenience wrapper retained for the stdio
// transport and unit tests; it delegates to the shared executeSearch path.
func (s *Server) handleGroundworkSearch(ctx context.Context, id any, rawArgs json.RawMessage) {
	result, rpcErr := s.executeSearch(ctx, s.tenantID, s.region, "", rawArgs)
	if rpcErr != nil {
		s.sendError(id, rpcErr.Code, rpcErr.Message)
		return
	}
	s.sendResult(id, result)
}

// executeSearch resolves identity, runs the query through the single Engine.Execute
// path, and formats the MCP tool result. It is shared by every transport.
//
// tenantID/region are provided by the transport from the API-key context. assertionToken,
// when non-empty, is an out-of-band identity token (e.g. the X-Groundwork-User-Assertion
// HTTP header) that takes precedence over the user_token tool argument. It returns a
// protocol error only for malformed arguments; identity/authorization failures are
// returned as a fail-closed tool result (IsError) with no documents.
func (s *Server) executeSearch(ctx context.Context, tenantID, region, assertionToken string, rawArgs json.RawMessage) (mcpToolResult, *jsonrpcError) {
	var args groundworkSearchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolResult{}, &jsonrpcError{Code: -32602, Message: "invalid arguments: question (and a verified user_token) are required"}
	}
	if args.Question == "" {
		return mcpToolResult{}, &jsonrpcError{Code: -32602, Message: "question is required"}
	}

	// An out-of-band assertion token (HTTP header) wins over the in-arguments token.
	token := assertionToken
	if token == "" {
		token = args.UserToken
	}

	// A signed token is always verified and becomes the effective user; the raw user_id
	// is honored only in demo mode. Fail closed on a missing/invalid/expired/unsigned
	// assertion. Tenant/region come from the API-key context, never from the arguments.
	identity, err := runtime.ResolveEffectiveIdentity(ctx, s.verifier, s.allowDemo, token, args.UserID)
	if err != nil {
		return mcpToolResult{
			Content: []mcpContent{{
				Type: "text",
				Text: fmt.Sprintf("FAIL CLOSED: a verified end-user identity is required. %v\n\nNo query was executed and no documents were returned.", err),
			}},
			IsError: true,
		}, nil
	}

	queryReq := runtime.QueryRequest{
		TenantID: tenantID,
		Region:   region,
		UserID:   identity.UserID,
		Question: args.Question,
	}

	// Execute through the Groundwork Engine (with full ACL, circuit breakers, audit).
	resp := s.engine.Execute(ctx, queryReq)

	isError := len(resp.Citations) == 0 && resp.Trace.FailureStage != ""
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: formatSearchResult(resp)}},
		IsError: isError,
	}, nil
}

// formatSearchResult renders the engine response as MCP tool text, identical across
// transports (enforce, shadow, and denied/fail-closed cases).
func formatSearchResult(resp runtime.QueryResponse) string {
	switch {
	case resp.Trace.ShadowMode:
		// Observe-only: return what the agent receives today, but flag the chunks
		// Groundwork would strip once enforcement is switched on.
		resultText := fmt.Sprintf(
			"SHADOW MODE (observe-only, no enforcement): returning %d document(s) the agent receives today.\nGroundwork WOULD BLOCK %d of these once enforcement is switched on.\n\n",
			len(resp.Citations),
			resp.Trace.WouldBlockByACL,
		)
		wouldBlock := map[string]bool{}
		for _, decision := range resp.Trace.AccessDecisions {
			if !decision.Allowed {
				wouldBlock[decision.ChunkID] = true
			}
		}
		for i, citation := range resp.Citations {
			status := "ALLOWED"
			if wouldBlock[citation.ChunkID] {
				status = "WOULD BE BLOCKED"
			}
			resultText += fmt.Sprintf(
				"[%d] (%s) Document: %s | Chunk: %s | Score: %.3f\n%s\n\n",
				i+1, status, citation.DocumentID, citation.ChunkID, citation.Score, citation.Text,
			)
		}
		resultText += fmt.Sprintf(
			"---\nTrace: %s | Would block by ACL: %d | Blocked by Region: %d | Decision: %s",
			resp.Trace.TraceID,
			resp.Trace.WouldBlockByACL,
			resp.Trace.BlockedByResidency,
			resp.Trace.DecisionMode,
		)
		return resultText
	case len(resp.Citations) == 0:
		return fmt.Sprintf(
			"ACCESS DENIED or NO RESULTS: %s\n\nTrace: %s | Decision: %s | Blocked by ACL: %d | Blocked by Region: %d",
			resp.Answer,
			resp.Trace.TraceID,
			resp.Trace.DecisionMode,
			resp.Trace.BlockedByACL,
			resp.Trace.BlockedByResidency,
		)
	default:
		resultText := fmt.Sprintf("Found %d permitted documents (confidence: %.2f):\n\n", len(resp.Citations), resp.Confidence)
		for i, citation := range resp.Citations {
			resultText += fmt.Sprintf(
				"[%d] Document: %s | Chunk: %s | Score: %.3f\n%s\n\n",
				i+1, citation.DocumentID, citation.ChunkID, citation.Score, citation.Text,
			)
		}
		resultText += fmt.Sprintf(
			"---\nTrace: %s | Blocked by ACL: %d | Blocked by Region: %d | Decision: %s",
			resp.Trace.TraceID,
			resp.Trace.BlockedByACL,
			resp.Trace.BlockedByResidency,
			resp.Trace.DecisionMode,
		)
		return resultText
	}
}

// --- Transport helpers ---

func (s *Server) sendResult(id any, result any) {
	s.send(jsonrpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) sendError(id any, code int, message string) {
	s.send(jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: message}})
}

func (s *Server) send(resp jsonrpcResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[mcp] failed to marshal response: %v", err)
		return
	}
	data = append(data, '\n')
	if _, err := s.writer.Write(data); err != nil {
		log.Printf("[mcp] failed to write response: %v", err)
	}
}
