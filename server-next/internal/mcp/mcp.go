// Package mcp speaks the Model Context Protocol for the assistant address: JSON-RPC 2.0
// requests over one HTTP POST each, answered with one JSON reply (no event stream). It knows
// nothing of Portico: the server gives it the tools a caller may use, and each tool is an
// ordinary API call made as the caller's key.
package mcp

import (
	"context"
	"encoding/json"
)

// ProtocolVersion is the protocol version this server speaks.
const ProtocolVersion = "2025-06-18"

// Tool is one thing an assistant may do.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// InputSchema is a JSON Schema object describing the arguments.
	InputSchema json.RawMessage `json:"inputSchema"`
	// Run does it. A returned error is shown to the assistant as the tool's failure; what
	// it returns is given to the assistant as JSON text.
	Run func(ctx context.Context, args json.RawMessage) (any, error) `json:"-"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve answers one request body. It returns nil for a notification, which takes no answer.
// server names this server to the assistant.
func Serve(ctx context.Context, body []byte, server string, tools []Tool) json.RawMessage {
	var req request
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		return reply(response{ID: json.RawMessage("null"), Error: &rpcError{-32600, "This is not a JSON-RPC 2.0 request. Batches are not taken."}})
	}
	if len(req.ID) == 0 {
		return nil // a notification, such as notifications/initialized
	}
	out := response{ID: req.ID}
	switch req.Method {
	case "initialize":
		out.Result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": server, "version": "1"},
			"instructions": "Portico media server. You can find and read titles, see what is playing and mark titles watched. " +
				"You cannot delete anything or change settings, people or keys."}
	case "ping":
		out.Result = map[string]any{}
	case "tools/list":
		list := make([]Tool, len(tools))
		copy(list, tools)
		out.Result = map[string]any{"tools": list}
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &call) != nil {
			out.Error = &rpcError{-32602, "Name a tool and its arguments."}
			break
		}
		var tool *Tool
		for i := range tools {
			if tools[i].Name == call.Name {
				tool = &tools[i]
			}
		}
		if tool == nil {
			out.Error = &rpcError{-32602, "There is no tool called " + call.Name + "."}
			break
		}
		if len(call.Arguments) == 0 {
			call.Arguments = json.RawMessage("{}")
		}
		result, err := tool.Run(ctx, call.Arguments)
		if err != nil {
			out.Result = map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}
			break
		}
		text, _ := json.Marshal(result)
		out.Result = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(text)}}, "structuredContent": result}
	default:
		out.Error = &rpcError{-32601, "This server does not have " + req.Method + "."}
	}
	return reply(out)
}

func reply(r response) json.RawMessage {
	r.JSONRPC = "2.0"
	raw, _ := json.Marshal(r)
	return raw
}

// Schema builds a tool's input schema from its argument names and descriptions. Every
// argument is a string unless its description starts with "(boolean)" or "(integer)".
func Schema(required []string, args map[string]string) json.RawMessage {
	props := map[string]any{}
	for name, about := range args {
		kind := "string"
		switch {
		case len(about) > 9 && about[:9] == "(boolean)":
			kind, about = "boolean", about[10:]
		case len(about) > 9 && about[:9] == "(integer)":
			kind, about = "integer", about[10:]
		}
		props[name] = map[string]string{"type": kind, "description": about}
	}
	if required == nil {
		required = []string{}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required})
	return raw
}
