// Package mcp exposes a read-only Model Context Protocol server over stdio
// (newline-delimited JSON-RPC 2.0). It never owns the controller or the
// database: snapshots come only from the running daemon's private Unix socket.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	"github.com/hunknownz/Meerkat/internal/server"
)

// Protocol and transport limits.
const (
	LatestProtocol  = "2025-06-18"
	MaxInputBytes   = 64 << 10
	MaxOutputBytes  = 64 << 20
	SnapshotTimeout = 3 * time.Second

	MonitorURI   = "ui://meerkat/monitor"
	MonitorMIME  = "text/html;profile=mcp-app"
	MonitorAsset = "mcp/meerkat-app.html"

	ToolOpenMonitor = "open_monitor"
	ToolGetSnapshot = "get_monitor_snapshot"
)

var supportedProtocols = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// SnapshotFunc fetches the public snapshot from the daemon.
type SnapshotFunc func(ctx context.Context) (server.Response, error)

// Server is one stdio MCP session.
type Server struct {
	Snapshot SnapshotFunc
	Assets   fs.FS

	initialize  bool // initialize request answered
	initialized bool // notifications/initialized received
}

// Serve runs a read-only MCP session on input/output for the daemon of dataDir.
func Serve(ctx context.Context, input io.Reader, output io.Writer, dataDir string, assets fs.FS) error {
	s := &Server{
		Assets: assets,
		Snapshot: func(ctx context.Context) (server.Response, error) {
			return server.Call(ctx, dataDir, server.Request{Op: "snapshot"})
		},
	}
	return s.Serve(ctx, input, output)
}

type line struct {
	data     []byte
	tooLarge bool
	err      error
}

// Serve processes messages until EOF or ctx cancellation. EOF is a clean exit.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	lines := make(chan line)
	done := make(chan struct{})
	defer close(done)
	go readLines(input, lines, done)
	w := bufio.NewWriter(output)
	for {
		var l line
		select {
		case <-ctx.Done():
			return nil
		case l = <-lines:
		}
		if l.err != nil {
			if errors.Is(l.err, io.EOF) {
				return nil
			}
			return errors.New("mcp: read failed")
		}
		var reply []byte
		if l.tooLarge {
			reply = encodeError(nil, codeInvalidRequest, "message too large")
		} else {
			reply = s.handle(ctx, l.data)
		}
		if reply == nil {
			continue
		}
		if _, err := w.Write(append(reply, '\n')); err != nil {
			return errors.New("mcp: write failed")
		}
		if err := w.Flush(); err != nil {
			return errors.New("mcp: write failed")
		}
	}
}

// readLines splits input into bounded lines; oversized lines are discarded.
func readLines(r io.Reader, out chan<- line, done <-chan struct{}) {
	br := bufio.NewReaderSize(r, 4096)
	send := func(l line) bool {
		select {
		case out <- l:
			return true
		case <-done:
			return false
		}
	}
	for {
		var buf []byte
		tooLarge := false
		var err error
		for {
			var chunk []byte
			chunk, err = br.ReadSlice('\n')
			if !tooLarge {
				if len(buf)+len(chunk) > MaxInputBytes+1 { // +1 for the newline
					tooLarge, buf = true, nil
				} else {
					buf = append(buf, chunk...)
				}
			}
			if err != bufio.ErrBufferFull {
				break
			}
		}
		if tooLarge {
			if !send(line{tooLarge: true}) {
				return
			}
		} else if b := bytes.TrimSpace(buf); len(b) > 0 {
			if !send(line{data: b}) {
				return
			}
		}
		if err != nil {
			send(line{err: err})
			return
		}
	}
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  *string         `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type rpcError struct {
	code int
	msg  string
}

func (e *rpcError) Error() string { return e.msg }

func invalidParams(msg string) *rpcError { return &rpcError{codeInvalidParams, msg} }

// handle returns the encoded reply, or nil when no reply is due.
func (s *Server) handle(ctx context.Context, raw []byte) []byte {
	if !json.Valid(raw) {
		return encodeError(nil, codeParse, "parse error")
	}
	if raw[0] != '{' { // batches are not supported
		return encodeError(nil, codeInvalidRequest, "invalid request")
	}
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return encodeError(nil, codeInvalidRequest, "invalid request")
	}
	hasID := len(m.ID) > 0
	if hasID && !validID(m.ID) {
		return encodeError(nil, codeInvalidRequest, "invalid request")
	}
	if m.Method == nil {
		if hasID && (len(m.Result) > 0 || len(m.Error) > 0) {
			return nil // a client response; we never send requests
		}
		return encodeError(idOrNil(m.ID), codeInvalidRequest, "invalid request")
	}
	if m.JSONRPC != "2.0" {
		if !hasID {
			return nil
		}
		return encodeError(m.ID, codeInvalidRequest, "invalid request")
	}
	if !hasID {
		s.notify(*m.Method)
		return nil
	}
	result, rerr := s.dispatch(ctx, *m.Method, m.Params)
	if rerr != nil {
		return encodeError(m.ID, rerr.code, rerr.msg)
	}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	if err != nil || len(b) > MaxOutputBytes {
		return encodeError(m.ID, codeInternal, "result unavailable")
	}
	return b
}

func validID(id json.RawMessage) bool {
	var v any
	if json.Unmarshal(id, &v) != nil {
		return false
	}
	switch v.(type) {
	case string, float64:
		return true
	}
	return false
}

func idOrNil(id json.RawMessage) json.RawMessage {
	if len(id) == 0 || !validID(id) {
		return nil
	}
	return id
}

func (s *Server) notify(method string) {
	if method == "notifications/initialized" && s.initialize {
		s.initialized = true
	}
	// all other notifications (cancelled, progress, ...) are ignored
}

func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		if s.initialize {
			return nil, &rpcError{codeInvalidRequest, "already initialized"}
		}
		return s.doInitialize(params)
	case "ping":
		if err := objectParams(params); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	case "tools/list", "tools/call", "resources/list", "resources/read", "resources/templates/list":
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found"}
	}
	if !s.initialized {
		return nil, &rpcError{codeInvalidRequest, "server not initialized"}
	}
	switch method {
	case "tools/list":
		if err := objectParams(params); err != nil {
			return nil, err
		}
		return map[string]any{"tools": toolList()}, nil
	case "tools/call":
		return s.callTool(ctx, params)
	case "resources/list":
		if err := objectParams(params); err != nil {
			return nil, err
		}
		return map[string]any{"resources": []any{monitorResource()}}, nil
	case "resources/templates/list":
		if err := objectParams(params); err != nil {
			return nil, err
		}
		return map[string]any{"resourceTemplates": []any{}}, nil
	default: // resources/read
		return s.readResource(params)
	}
}

// objectParams accepts absent, null or an object (e.g. cursor, _meta).
func objectParams(params json.RawMessage) *rpcError {
	p := bytes.TrimSpace(params)
	if len(p) == 0 || string(p) == "null" {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(p, &m) != nil {
		return invalidParams("invalid params")
	}
	return nil
}

func (s *Server) doInitialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || p.ProtocolVersion == "" {
		return nil, invalidParams("protocolVersion required")
	}
	version := LatestProtocol
	if supportedProtocols[p.ProtocolVersion] {
		version = p.ProtocolVersion
	}
	s.initialize = true
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false, "subscribe": false},
		},
		"serverInfo": map[string]any{
			"name":    "meerkat",
			"title":   "Meerkat",
			"version": server.Version,
		},
		"instructions": "Read-only Meerkat monitor. Requires a running `meerkat serve` daemon; these tools never start, stop or change runs.",
	}, nil
}

var readOnly = map[string]any{
	"readOnlyHint":    true,
	"destructiveHint": false,
	"idempotentHint":  true,
	"openWorldHint":   false,
}

func emptySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func toolList() []any {
	return []any{
		map[string]any{
			"name":        ToolOpenMonitor,
			"title":       "Open Meerkat monitor",
			"description": "Open the read-only Meerkat monitor and summarize current task and run counts.",
			"inputSchema": emptySchema(),
			"annotations": readOnly,
			"_meta": map[string]any{
				"ui":        map[string]any{"resourceUri": MonitorURI, "visibility": []string{"model", "app"}},
				"openai/ui": map[string]any{"entrypoints": []any{map[string]any{"type": "global"}, map[string]any{"type": "thread"}}},
			},
		},
		map[string]any{
			"name":        ToolGetSnapshot,
			"title":       "Get Meerkat monitor snapshot",
			"description": "Refresh the read-only public Meerkat snapshot for the monitor app.",
			"inputSchema": emptySchema(),
			"annotations": readOnly,
			"_meta": map[string]any{
				"ui": map[string]any{"resourceUri": MonitorURI, "visibility": []string{"app"}},
			},
		},
	}
}

func uiMeta() map[string]any {
	return map[string]any{"ui": map[string]any{"csp": map[string]any{
		"connectDomains":  []string{},
		"resourceDomains": []string{},
	}}}
}

func monitorResource() map[string]any {
	return map[string]any{
		"uri":         MonitorURI,
		"name":        "meerkat-monitor",
		"title":       "Meerkat monitor",
		"description": "Read-only Meerkat monitor app.",
		"mimeType":    MonitorMIME,
		"_meta":       uiMeta(),
	}
}

func (s *Server) readResource(params json.RawMessage) (any, *rpcError) {
	var p struct {
		URI  string          `json:"uri"`
		Meta json.RawMessage `json:"_meta"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if len(params) == 0 || dec.Decode(&p) != nil {
		return nil, invalidParams("invalid params")
	}
	if p.URI != MonitorURI {
		return nil, invalidParams("resource not found")
	}
	if s.Assets == nil {
		return nil, &rpcError{codeInternal, "monitor UI unavailable"}
	}
	html, err := fs.ReadFile(s.Assets, MonitorAsset)
	if err != nil {
		return nil, &rpcError{codeInternal, "monitor UI unavailable"}
	}
	return map[string]any{"contents": []any{map[string]any{
		"uri":      MonitorURI,
		"mimeType": MonitorMIME,
		"text":     string(html),
		"_meta":    uiMeta(),
	}}}, nil
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if len(params) == 0 || dec.Decode(&p) != nil {
		return nil, invalidParams("invalid params")
	}
	if p.Name != ToolOpenMonitor && p.Name != ToolGetSnapshot {
		return nil, invalidParams("unknown tool")
	}
	if a := bytes.TrimSpace(p.Arguments); len(a) > 0 && string(a) != "null" {
		var m map[string]json.RawMessage
		if json.Unmarshal(a, &m) != nil || len(m) != 0 {
			return nil, invalidParams("tool takes no arguments")
		}
	}
	return s.snapshotResult(ctx), nil
}

func toolError(text string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": true,
	}
}

const unavailable = "Meerkat daemon is unavailable. Start it with `meerkat serve` and try again."

// summary is the bounded public view placed in structuredContent.
type summary struct {
	SchemaVersion int    `json:"schemaVersion"`
	ObservedAt    string `json:"observedAt"`
	Counts        counts `json:"counts"`
}

type counts struct {
	Projects   int `json:"projects"`
	Tasks      int `json:"tasks"`
	Runs       int `json:"runs"`
	Deliveries int `json:"deliveries"`
	Reviews    int `json:"reviews"`
	Running    int `json:"running"`
	Queued     int `json:"queued"`
	Unknown    int `json:"unknown"`
}

func (s *Server) snapshotResult(ctx context.Context) map[string]any {
	if s.Snapshot == nil {
		return toolError(unavailable)
	}
	cctx, cancel := context.WithTimeout(ctx, SnapshotTimeout)
	defer cancel()
	resp, err := s.Snapshot(cctx)
	if err != nil {
		return toolError(unavailable)
	}
	if !resp.OK || len(resp.Data) == 0 {
		return toolError("Meerkat daemon could not provide a snapshot. Check `meerkat serve` and try again.")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(resp.Data, &raw) != nil {
		return toolError("Meerkat daemon returned an unreadable snapshot.")
	}
	stripPrivate(raw)
	var snap struct {
		SchemaVersion int               `json:"schemaVersion"`
		ObservedAt    string            `json:"observedAt"`
		Projects      []json.RawMessage `json:"projects"`
		Tasks         []json.RawMessage `json:"tasks"`
		Runs          []json.RawMessage `json:"runs"`
		Deliveries    []json.RawMessage `json:"deliveries"`
		Reviews       []json.RawMessage `json:"reviews"`
		Counts        struct {
			Running int `json:"running"`
			Queued  int `json:"queued"`
			Unknown int `json:"unknown"`
		} `json:"counts"`
	}
	clean, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(clean, &snap) != nil {
		return toolError("Meerkat daemon returned an unreadable snapshot.")
	}
	obs := snap.ObservedAt
	if len(obs) > 64 {
		obs = ""
	}
	sum := summary{
		SchemaVersion: snap.SchemaVersion,
		ObservedAt:    obs,
		Counts: counts{
			Projects: len(snap.Projects), Tasks: len(snap.Tasks), Runs: len(snap.Runs),
			Deliveries: len(snap.Deliveries), Reviews: len(snap.Reviews),
			Running: snap.Counts.Running, Queued: snap.Counts.Queued, Unknown: snap.Counts.Unknown,
		},
	}
	text := fmt.Sprintf("Meerkat monitor: %d tasks, %d runs (%d running, %d queued, %d unknown).",
		sum.Counts.Tasks, sum.Counts.Runs, sum.Counts.Running, sum.Counts.Queued, sum.Counts.Unknown)
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": text}},
		"structuredContent": sum,
		"_meta": map[string]any{
			"snapshot":     json.RawMessage(clean),
			"legacyActive": []any{},
		},
	}
}

// privateKeys are never forwarded, even if a daemon reply contained them.
var privateKeys = map[string]bool{"sessionToken": true, "authEnv": true}

func stripPrivate(m map[string]json.RawMessage) {
	for k, v := range m {
		if privateKeys[k] {
			delete(m, k)
			continue
		}
		m[k] = stripValue(v)
	}
}

func stripValue(v json.RawMessage) json.RawMessage {
	t := bytes.TrimSpace(v)
	if len(t) == 0 {
		return v
	}
	switch t[0] {
	case '{':
		var m map[string]json.RawMessage
		if json.Unmarshal(t, &m) != nil {
			return v
		}
		stripPrivate(m)
		if b, err := json.Marshal(m); err == nil {
			return b
		}
	case '[':
		var a []json.RawMessage
		if json.Unmarshal(t, &a) != nil {
			return v
		}
		for i := range a {
			a[i] = stripValue(a[i])
		}
		if b, err := json.Marshal(a); err == nil {
			return b
		}
	}
	return v
}

func encodeError(id json.RawMessage, code int, msg string) []byte {
	var idv any = nil
	if len(id) > 0 {
		idv = id
	}
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      idv,
		"error":   map[string]any{"code": code, "message": msg},
	})
	return b
}
