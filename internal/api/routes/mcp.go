package routes

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/appsprout-dev/mnemonic/internal/mcp"
)

// HandleMCP returns an HTTP handler for the MCP JSON-RPC protocol.
//
// Session lifecycle follows the MCP streamable HTTP transport spec:
//   - First request (initialize): no Mcp-Session-Id header needed.
//     Server creates a session and returns the ID in the response header.
//   - Subsequent requests: client includes Mcp-Session-Id from the
//     initialize response. Server routes to the existing session.
//   - DELETE with Mcp-Session-Id: explicitly ends the session.
//   - Idle sessions are reaped by the session manager after timeout.
func HandleMCP(sm *mcp.SessionManager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			handleMCPDelete(sm, log, w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Read and parse the JSON-RPC request
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			writeJSONRPCError(w, nil, -32700, "Failed to read request body")
			return
		}

		var req mcp.JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSONRPCError(w, nil, -32700, "Parse error")
			return
		}

		// Resolve session: use client header if present, otherwise create new
		clientSessionID := r.Header.Get("Mcp-Session-Id")
		srv, sessionKey := sm.GetOrCreate(clientSessionID)

		resp := srv.HandleSingleRequest(r.Context(), &req)

		// Always return the session ID so the client can include it in subsequent requests
		w.Header().Set("Mcp-Session-Id", sessionKey)

		// Notifications return nil — respond with 202 Accepted
		if resp == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Warn("failed to encode MCP HTTP response", "error", err)
		}
	}
}

// handleMCPDelete explicitly ends an MCP session.
func handleMCPDelete(sm *mcp.SessionManager, log *slog.Logger, w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "Mcp-Session-Id header is required", http.StatusBadRequest)
		return
	}

	sm.EndSession(r.Context(), sessionID)
	log.Info("MCP session explicitly ended via DELETE", "session_id", sessionID)
	w.WriteHeader(http.StatusNoContent)
}

// writeJSONRPCError writes a JSON-RPC error response.
func writeJSONRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // JSON-RPC errors are still 200
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}
