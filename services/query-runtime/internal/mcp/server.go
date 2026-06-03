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
	engine   *engine.Engine
	tenantID string
	region   string
	writer   io.Writer
	mu       sync.Mutex
}

// NewServer creates an MCP server bound to a specific tenant context.
// In production, the tenant context comes from the API key used to launch the MCP process.
func NewServer(eng *engine.Engine, tenantID, region string) *Server {
	return &Server{
		engine:   eng,
		tenantID: tenantID,
		region:   region,
		writer:   os.Stdout,
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
	UserID   string `json:"user_id"`
	Question string `json:"question"`
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
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized":
		// Client acknowledgement — no response needed
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(ctx, req)
	case "ping":
		s.sendResult(req.ID, map[string]string{})
	default:
		s.sendError(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req jsonrpcRequest) {
	s.sendResult(req.ID, mcpInitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: map[string]any{
			"tools": map[string]any{},
		},
		ServerInfo: mcpServerInfo{
			Name:    "groundwork",
			Version: "1.0.0",
		},
	})
}

func (s *Server) handleToolsList(req jsonrpcRequest) {
	tools := []mcpTool{
		{
			Name:        "groundwork_search",
			Description: "Search enterprise documents with live permission enforcement. Returns only documents the specified user is authorized to view. All unauthorized documents are automatically stripped and the interaction is cryptographically logged.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"user_id": map[string]any{
						"type":        "string",
						"description": "The identity of the user on whose behalf this search is being performed (e.g. 'alice@company.com' or 'financial_bot'). Groundwork will check this user's live permissions against every retrieved document.",
					},
					"question": map[string]any{
						"type":        "string",
						"description": "The natural language question to search for in the enterprise knowledge base.",
					},
				},
				"required": []string{"user_id", "question"},
			},
		},
	}
	s.sendResult(req.ID, map[string]any{"tools": tools})
}

func (s *Server) handleToolsCall(ctx context.Context, req jsonrpcRequest) {
	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "invalid params")
		return
	}

	switch params.Name {
	case "groundwork_search":
		s.handleGroundworkSearch(ctx, req.ID, params.Arguments)
	default:
		s.sendError(req.ID, -32602, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

func (s *Server) handleGroundworkSearch(ctx context.Context, id any, rawArgs json.RawMessage) {
	var args groundworkSearchArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		s.sendError(id, -32602, "invalid arguments: user_id and question are required")
		return
	}
	if args.UserID == "" || args.Question == "" {
		s.sendError(id, -32602, "user_id and question are required")
		return
	}

	// Build the query request with tenant context from the MCP server config
	queryReq := runtime.QueryRequest{
		TenantID: s.tenantID,
		Region:   s.region,
		UserID:   args.UserID,
		Question: args.Question,
	}

	// Execute through the Groundwork Engine (with full ACL, circuit breakers, audit)
	resp := s.engine.Execute(ctx, queryReq)

	// Format the response for the AI agent
	var resultText string
	if len(resp.Citations) == 0 {
		resultText = fmt.Sprintf(
			"ACCESS DENIED or NO RESULTS: %s\n\nTrace: %s | Decision: %s | Blocked by ACL: %d | Blocked by Region: %d",
			resp.Answer,
			resp.Trace.TraceID,
			resp.Trace.DecisionMode,
			resp.Trace.BlockedByACL,
			resp.Trace.BlockedByResidency,
		)
	} else {
		resultText = fmt.Sprintf("Found %d permitted documents (confidence: %.2f):\n\n", len(resp.Citations), resp.Confidence)
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
	}

	isError := len(resp.Citations) == 0 && resp.Trace.FailureStage != ""
	s.sendResult(id, mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: resultText}},
		IsError: isError,
	})
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
