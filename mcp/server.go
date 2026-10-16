package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

const (
	codeParse          = -32700
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
)

type ServerTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

func FromTool(app *kit.App, def *kit.ToolDef) ServerTool {
	return ServerTool{
		Name:        def.Name(),
		Description: def.Description(),
		InputSchema: def.Schema(),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			return app.CallTool(ctx, def.Name(), args)
		},
	}
}

type ServerConfig struct {
	Name           string
	Version        string
	Tools          []ServerTool
	AllowedOrigins []string
	Logger         *slog.Logger
}

type Server struct {
	cfg  ServerConfig
	byID map[string]ServerTool
	list []ServerTool
	log  *slog.Logger
}

func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("mcp: server name is required: %w", core.ErrInvalidConfig)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	byID := make(map[string]ServerTool, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if t.Name == "" {
			return nil, fmt.Errorf("mcp: %s: a server tool has an empty name: %w", cfg.Name, core.ErrInvalidConfig)
		}
		if t.Handler == nil {
			return nil, fmt.Errorf("mcp: %s: tool %s: handler is required: %w", cfg.Name, t.Name, core.ErrInvalidConfig)
		}
		if _, dup := byID[t.Name]; dup {
			return nil, fmt.Errorf("mcp: %s: duplicate tool %q: %w", cfg.Name, t.Name, core.ErrInvalidConfig)
		}
		byID[t.Name] = t
	}
	return &Server{cfg: cfg, byID: byID, list: append([]ServerTool(nil), cfg.Tools...), log: log}, nil
}

func (s *Server) Name() string { return s.cfg.Name }

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		resp := s.handle(ctx, line)
		if resp == nil {
			continue
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			return fmt.Errorf("mcp: %s: encode response: %w", s.cfg.Name, err)
		}
		if _, err := w.Write(append(raw, '\n')); err != nil {
			return fmt.Errorf("mcp: %s: write response: %w", s.cfg.Name, err)
		}
		if err := w.Flush(); err != nil {
			return fmt.Errorf("mcp: %s: flush response: %w", s.cfg.Name, err)
		}
	}
	return sc.Err()
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "only POST is supported", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	resp := s.handle(r.Context(), body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

type srvRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type srvResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *srvError       `json:"error,omitempty"`
}

type srvError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(ctx context.Context, raw []byte) *srvResponse {
	var req srvRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return errResp(json.RawMessage("null"), codeParse, "parse error")
	}
	if req.Method == "" || strings.HasPrefix(req.Method, "notifications/") {
		return nil
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil
	}

	switch req.Method {
	case "initialize":
		return &srvResponse{JSONRPC: "2.0", ID: req.ID, Result: s.initializeResult()}
	case "ping":
		return &srvResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &srvResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.toolsList()}}
	case "tools/call":
		return s.callTool(ctx, req)
	default:
		return errResp(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      ServerInfo{Name: s.cfg.Name, Version: s.cfg.Version},
	}
}

func (s *Server) toolsList() []map[string]any {
	out := make([]map[string]any, 0, len(s.list))
	for _, t := range s.list {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		entry := map[string]any{"name": t.Name, "inputSchema": schema}
		if t.Description != "" {
			entry["description"] = t.Description
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) callTool(ctx context.Context, req srvRequest) *srvResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, codeInvalidParams, "invalid tools/call params")
		}
	}
	if params.Name == "" {
		return errResp(req.ID, codeInvalidParams, "tools/call: name is required")
	}
	tool, ok := s.byID[params.Name]
	if !ok {
		return errResp(req.ID, codeInvalidParams, "unknown tool: "+params.Name)
	}
	args := params.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	res, err := tool.Handler(ctx, args)
	if err != nil {
		s.log.Debug("mcp: server tool failed", "server", s.cfg.Name, "tool", tool.Name, "err", err)
		return &srvResponse{JSONRPC: "2.0", ID: req.ID, Result: CallResult{
			Content: []Content{{Type: "text", Text: err.Error()}},
			IsError: true,
		}}
	}
	return &srvResponse{JSONRPC: "2.0", ID: req.ID, Result: CallResult{
		Content: []Content{{Type: "text", Text: res}},
	}}
}

func errResp(id json.RawMessage, code int, msg string) *srvResponse {
	return &srvResponse{JSONRPC: "2.0", ID: id, Error: &srvError{Code: code, Message: msg}}
}
