// Package mcp exposes qdnder tools to AI agents over JSON-RPC 2.0
// (MCP streamable HTTP, single POST endpoint).
package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/piqab/qdnder/internal/store"
)

// Server implements the MCP endpoint. Resolve returns the caller's role
// ("" when unauthenticated); Enqueue queues a build.
type Server struct {
	Store   *store.Store
	Enqueue func(projectRef, ref, sha, tag, trigger string) (*store.Build, error)
	Resolve func(*http.Request) string
	Version string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func reply(w http.ResponseWriter, id json.RawMessage, result any, rerr *rpcError) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		resp["error"] = rerr
	} else {
		resp["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func toolText(v any) map[string]any {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(raw)}}}
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": msg}}, "isError": true}
}

var tools = []map[string]any{
	{
		"name":        "qdnder_builds",
		"description": "List image builds (newest first). Read-only.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project_id": map[string]string{"type": "string", "description": "filter by project id"},
			"status":     map[string]string{"type": "string", "description": "queued|running|success|failed|canceled"},
			"limit":      map[string]string{"type": "integer", "description": "max rows (default 20)"},
		}},
	},
	{
		"name":        "qdnder_build",
		"description": "Queue an image build for a project. Requires operator role.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"project": map[string]string{"type": "string", "description": "project name or id"},
			"ref":     map[string]string{"type": "string", "description": "branch or tag (default: project branch)"},
			"tag":     map[string]string{"type": "string", "description": "git tag value for {tag} templates"},
		}, "required": []string{"project"}},
	},
	{
		"name":        "qdnder_logs",
		"description": "Get the log of a build (optionally the last N lines). Read-only.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"build_id": map[string]string{"type": "string"},
			"tail":     map[string]string{"type": "integer", "description": "last N lines"},
		}, "required": []string{"build_id"}},
	},
	{
		"name":        "qdnder_cancel",
		"description": "Cancel a queued or running build. Requires operator role.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"build_id": map[string]string{"type": "string"},
		}, "required": []string{"build_id"}},
	},
}

var writeTools = map[string]bool{"qdnder_build": true, "qdnder_cancel": true}

// Handler serves POST /mcp.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := s.Resolve(r)
		if role == "" || role == "runner" {
			http.Error(w, `{"error":"authentication required"}`, 401)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" {
			reply(w, nil, nil, &rpcError{Code: -32600, Message: "invalid request"})
			return
		}
		if len(req.ID) == 0 { // notification
			w.WriteHeader(202)
			return
		}
		switch req.Method {
		case "initialize":
			reply(w, req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "qdnder", "version": s.Version},
			}, nil)
		case "tools/list":
			reply(w, req.ID, map[string]any{"tools": tools}, nil)
		case "tools/call":
			s.callTool(w, req, role)
		case "ping":
			reply(w, req.ID, map[string]any{}, nil)
		default:
			reply(w, req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	})
}

func (s *Server) callTool(w http.ResponseWriter, req rpcRequest, role string) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		reply(w, req.ID, nil, &rpcError{Code: -32602, Message: "invalid params"})
		return
	}
	if writeTools[params.Name] && role != "operator" && role != "admin" {
		reply(w, req.ID, toolError("operator role required"), nil)
		return
	}
	args := map[string]any{}
	if len(params.Arguments) > 0 {
		_ = json.Unmarshal(params.Arguments, &args)
	}
	str := func(k string) string { v, _ := args[k].(string); return v }

	switch params.Name {
	case "qdnder_builds":
		limit := 20
		if f, ok := args["limit"].(float64); ok && f > 0 {
			limit = int(f)
		}
		builds, err := s.Store.Builds(str("project_id"), str("status"), limit)
		if err != nil {
			reply(w, req.ID, toolError(err.Error()), nil)
			return
		}
		reply(w, req.ID, toolText(builds), nil)
	case "qdnder_build":
		if str("project") == "" {
			reply(w, req.ID, toolError("project is required"), nil)
			return
		}
		b, err := s.Enqueue(str("project"), str("ref"), "", str("tag"), "mcp")
		if err != nil {
			reply(w, req.ID, toolError(err.Error()), nil)
			return
		}
		reply(w, req.ID, toolText(b), nil)
	case "qdnder_logs":
		b, err := s.Store.BuildByID(str("build_id"))
		if err != nil || b == nil {
			reply(w, req.ID, toolError("build not found"), nil)
			return
		}
		log := b.Log
		if f, ok := args["tail"].(float64); ok && f > 0 {
			log = tailLines(log, int(f))
		}
		reply(w, req.ID, toolText(map[string]any{"status": b.Status, "log": log}), nil)
	case "qdnder_cancel":
		if err := s.Store.RequestCancel(str("build_id")); err != nil {
			reply(w, req.ID, toolError(err.Error()), nil)
			return
		}
		reply(w, req.ID, toolText(map[string]string{"ok": "cancellation requested"}), nil)
	default:
		reply(w, req.ID, nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", params.Name)})
	}
}

func tailLines(s string, n int) string {
	lines := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			lines++
			if lines > n {
				return s[i+1:]
			}
		}
	}
	return s
}
