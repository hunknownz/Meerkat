package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const (
	ToolListRuns       = "list_runs"
	ToolGetRun         = "get_run"
	ToolGetSettings    = "get_settings"
	ToolStopRun        = "stop_run"
	ToolUpdateSettings = "update_settings"
	ControlTimeout     = 3 * time.Second
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var atomPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
var activityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:/-]{0,79}$`)

func controlTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	schema := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	tool := func(name, description string, input map[string]any, readonly, destructive, idempotent bool) any {
		return map[string]any{
			"name": name, "description": description, "inputSchema": input,
			"annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": false},
			"execution":   map[string]any{"taskSupport": "forbidden"},
			// Host visibility is a UI hint, not a service authorization boundary.
			"_meta": map[string]any{"ui": map[string]any{"visibility": []string{"model"}}},
		}
	}
	settings := schema(map[string]any{
		"maxConcurrency": map[string]any{"type": "integer", "minimum": model.MinConcurrency, "maximum": model.MaxConcurrency},
		"maxFixRounds":   map[string]any{"type": "integer", "minimum": 0, "maximum": model.MaxFixRoundsLimit},
	})
	settings["minProperties"] = 1
	return []any{
		tool(ToolListRuns, "List bounded run summaries, optionally filtered by task or state. Use nextCursor with the same filters; changed data requires restarting pagination. Missing usage stays unknown.", schema(map[string]any{
			"taskId": uuid, "state": map[string]any{"type": "string", "enum": model.RunStates},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"cursor": map[string]any{"type": "string", "maxLength": 512},
		}), true, false, true),
		tool(ToolGetRun, "Read one run's state, role, executor, model, recent activity and nullable usage. State unknown is not proof of process exit.", schema(map[string]any{"runId": uuid}, "runId"), true, false, true),
		tool(ToolGetSettings, "Read concurrency and fix-round defaults for future runs.", emptySchema(), true, false, true),
		tool(ToolStopRun, "Record a stop request for one run. Requires user authorization and a stable requestId. Accepted is not stopped. Explicit recovery must reuse both IDs; do not automatically retry a lost reply.", schema(map[string]any{"runId": uuid, "requestId": uuid}, "runId", "requestId"), false, true, true),
		tool(ToolUpdateSettings, "Change concurrency or fix-round defaults for future runs within user authorization. Existing frozen task budgets remain unchanged. A lost reply is unknown: query get_settings before deciding another write.", settings, false, false, false),
	}
}

// decodeArgs enforces object arguments, unique keys and advertised field types.
// No tool in this slice accepts nested objects or arbitrary executor commands.
func decodeArgs(raw json.RawMessage, dst any, allowed ...string) *rpcError {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if raw[0] != '{' || !json.Valid(raw) {
		return invalidParams("arguments must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	_, _ = d.Token()
	seen := map[string]bool{}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return invalidParams("invalid arguments")
		}
		name, ok := k.(string)
		if !ok || seen[name] {
			return invalidParams("duplicate argument")
		}
		// encoding/json accepts case-insensitive struct field names; the
		// advertised JSON Schema does not. Check exact names before decoding.
		if !slices.Contains(allowed, name) {
			return invalidParams("unknown argument")
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalidParams("invalid argument value")
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return invalidParams("invalid arguments")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalidParams("trailing arguments")
	}
	return nil
}

func (s *Server) controlTool(ctx context.Context, name string, args json.RawMessage) (any, *rpcError) {
	switch name {
	case ToolListRuns:
		var p struct {
			TaskID *string `json:"taskId"`
			State  *string `json:"state"`
			Limit  *int    `json:"limit"`
			Cursor string  `json:"cursor"`
		}
		if err := decodeArgs(args, &p, "taskId", "state", "limit", "cursor"); err != nil {
			return nil, err
		}
		if p.TaskID != nil && !uuidPattern.MatchString(*p.TaskID) || p.State != nil && !slices.Contains(model.RunStates, *p.State) || len(p.Cursor) > 512 {
			return nil, invalidParams("invalid run filter")
		}
		limit := 25
		if p.Limit != nil {
			limit = *p.Limit
		}
		if limit < 1 || limit > 100 {
			return nil, invalidParams("limit must be an integer 1..100")
		}
		snap, err := s.readControlSnapshot(ctx)
		if err != nil {
			return toolError(err.Error()), nil
		}
		items := []runView{}
		for _, r := range snap.Runs {
			if (p.TaskID == nil || r.TaskID == *p.TaskID) && (p.State == nil || r.State == *p.State) {
				items = append(items, publicRun(r))
			}
		}
		slices.SortFunc(items, func(a, b runView) int { return bytes.Compare([]byte(a.ID), []byte(b.ID)) })
		digestBytes, _ := json.Marshal([]any{p.TaskID, p.State, items})
		digest := fmt.Sprintf("%x", sha256.Sum256(digestBytes))
		offset := 0
		if p.Cursor != "" {
			var c pageCursor
			b, e := base64.RawURLEncoding.DecodeString(p.Cursor)
			if e != nil || json.Unmarshal(b, &c) != nil || c.Offset < 1 || c.Offset > len(items) {
				return nil, invalidParams("invalid cursor")
			}
			if c.Digest != digest {
				return toolError("Runs or filters changed. Restart list_runs without a cursor."), nil
			}
			offset = c.Offset
		}
		end := min(offset+limit, len(items))
		var next *string
		if end < len(items) {
			b, _ := json.Marshal(pageCursor{Digest: digest, Offset: end})
			v := base64.RawURLEncoding.EncodeToString(b)
			next = &v
		}
		return structured(fmt.Sprintf("%d run summaries returned.", end-offset), map[string]any{"schemaVersion": 1, "observedAt": snap.ObservedAt, "items": items[offset:end], "nextCursor": next}), nil
	case ToolGetRun:
		var p struct {
			RunID string `json:"runId"`
		}
		if err := decodeArgs(args, &p, "runId"); err != nil {
			return nil, err
		}
		if !uuidPattern.MatchString(p.RunID) {
			return nil, invalidParams("runId must be a lowercase UUID")
		}
		snap, err := s.readControlSnapshot(ctx)
		if err != nil {
			return toolError(err.Error()), nil
		}
		for _, r := range snap.Runs {
			if r.ID == p.RunID {
				return structured("Run state: "+safeAtom(r.State)+".", map[string]any{"schemaVersion": 1, "observedAt": snap.ObservedAt, "run": publicRun(r)}), nil
			}
		}
		return toolError("Run not found."), nil
	case ToolGetSettings:
		if err := decodeArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		snap, err := s.readControlSnapshot(ctx)
		if err != nil {
			return toolError(err.Error()), nil
		}
		if snap.Settings == nil || !validSettings(*snap.Settings) {
			return toolError("Meerkat daemon returned unreadable settings."), nil
		}
		return structured("Future-run settings.", settingsView(*snap.Settings)), nil
	case ToolStopRun:
		var p struct {
			RunID     string `json:"runId"`
			RequestID string `json:"requestId"`
		}
		if err := decodeArgs(args, &p, "runId", "requestId"); err != nil {
			return nil, err
		}
		if !uuidPattern.MatchString(p.RunID) || !uuidPattern.MatchString(p.RequestID) {
			return nil, invalidParams("runId and requestId must be lowercase UUIDs")
		}
		resp, err := s.command(ctx, server.Request{Op: "stop", RunID: p.RunID, RequestID: p.RequestID})
		if err != nil {
			return controlFailure(err, p.RunID, p.RequestID), nil
		}
		if !resp.OK {
			return commandFailure(resp.Code, p.RunID, p.RequestID), nil
		}
		var rc model.StopReceipt
		if json.Unmarshal(resp.Data, &rc) != nil || rc.RunID != p.RunID || rc.RequestID != p.RequestID || !validReceipt(rc) {
			return controlFailure(errors.New("unreadable receipt"), p.RunID, p.RequestID), nil
		}
		text := "Stop request accepted; actual outcome is pending."
		if rc.State == model.StopProcessed {
			text = "Stop request processed; actual outcome: " + *rc.Outcome + "."
		}
		return structured(text, map[string]any{"schemaVersion": 1, "receipt": rc}), nil
	case ToolUpdateSettings:
		var p struct {
			MaxConcurrency *int `json:"maxConcurrency"`
			MaxFixRounds   *int `json:"maxFixRounds"`
		}
		if err := decodeArgs(args, &p, "maxConcurrency", "maxFixRounds"); err != nil {
			return nil, err
		}
		if p.MaxConcurrency == nil && p.MaxFixRounds == nil {
			return nil, invalidParams("at least one setting is required")
		}
		if p.MaxConcurrency != nil && (*p.MaxConcurrency < model.MinConcurrency || *p.MaxConcurrency > model.MaxConcurrency) || p.MaxFixRounds != nil && (*p.MaxFixRounds < 0 || *p.MaxFixRounds > model.MaxFixRoundsLimit) {
			return nil, invalidParams("settings out of range")
		}
		patch, _ := json.Marshal(model.SettingsPatch{MaxConcurrency: p.MaxConcurrency, MaxFixRounds: p.MaxFixRounds})
		resp, err := s.command(ctx, server.Request{Op: "settings", Input: patch})
		if err != nil {
			return controlFailure(err, "", ""), nil
		}
		if !resp.OK {
			return commandFailure(resp.Code, "", ""), nil
		}
		var set model.Settings
		if json.Unmarshal(resp.Data, &set) != nil || !validSettings(set) || p.MaxConcurrency != nil && set.MaxConcurrency != *p.MaxConcurrency || p.MaxFixRounds != nil && set.MaxFixRounds != *p.MaxFixRounds {
			return controlFailure(errors.New("unreadable settings"), "", ""), nil
		}
		return structured("Future-run settings updated.", settingsView(set)), nil
	default:
		return nil, invalidParams("unknown tool")
	}
}

type pageCursor struct {
	Digest string `json:"digest"`
	Offset int    `json:"offset"`
}

type controlSnapshot struct {
	SchemaVersion int             `json:"schemaVersion"`
	ObservedAt    string          `json:"observedAt"`
	Runs          []model.Run     `json:"runs"`
	Settings      *model.Settings `json:"settings"`
}

func (s *Server) readControlSnapshot(ctx context.Context) (controlSnapshot, error) {
	var snap controlSnapshot
	if s.Snapshot == nil {
		return snap, errors.New(unavailable)
	}
	cctx, cancel := context.WithTimeout(ctx, SnapshotTimeout)
	defer cancel()
	resp, err := s.Snapshot(cctx)
	if err != nil || !resp.OK {
		return snap, errors.New(unavailable)
	}
	if json.Unmarshal(resp.Data, &snap) != nil || snap.SchemaVersion != 1 || snap.Runs == nil || len(snap.ObservedAt) > 64 {
		return controlSnapshot{}, errors.New("Meerkat daemon returned an unreadable snapshot.")
	}
	return snap, nil
}

type runView struct {
	ID             string               `json:"id"`
	TaskID         string               `json:"taskId"`
	AgentID        string               `json:"agentId,omitempty"`
	Role           string               `json:"role"`
	Executor       string               `json:"executor"`
	State          string               `json:"state"`
	RecordedState  string               `json:"recordedState,omitempty"`
	Model          *model.ModelSnapshot `json:"model"`
	StartedAt      string               `json:"startedAt"`
	EndedAt        *string              `json:"endedAt"`
	UpdatedAt      string               `json:"updatedAt"`
	RecentActivity []model.RunEvent     `json:"recentActivity"`
	Usage          *model.Usage         `json:"usage"`
}

func safeAtom(v string) string {
	if v == "" || atomPattern.MatchString(v) && !model.LooksLikeCredential(v) {
		return v
	}
	return "[redacted]"
}

func publicRun(r model.Run) runView {
	v := runView{ID: safeAtom(r.ID), TaskID: safeAtom(r.TaskID), AgentID: safeAtom(r.AgentID), Role: safeAtom(r.Role), Executor: safeAtom(r.Executor), State: safeAtom(r.State), RecordedState: safeAtom(r.RecordedState), StartedAt: safeAtom(r.StartedAt), UpdatedAt: safeAtom(r.UpdatedAt), RecentActivity: []model.RunEvent{}}
	if r.EndedAt != nil {
		value := safeAtom(*r.EndedAt)
		v.EndedAt = &value
	}
	if r.ModelSnapshot != nil {
		v.Model = &model.ModelSnapshot{ProfileID: safeAtom(r.ModelSnapshot.ProfileID), Provider: safeAtom(r.ModelSnapshot.Provider), Model: safeAtom(r.ModelSnapshot.Model)}
	}
	for _, e := range r.Events[max(0, len(r.Events)-3):] {
		if atomPattern.MatchString(e.Type) && activityPattern.MatchString(e.Summary) && !model.LooksLikeCredential(e.Summary) {
			v.RecentActivity = append(v.RecentActivity, model.RunEvent{Type: e.Type, Summary: e.Summary, ObservedAt: safeAtom(e.ObservedAt)})
		}
	}
	if r.Usage != nil {
		b, _ := json.Marshal(r.Usage)
		v.Usage = model.ParseLegacyUsage(b, safeAtom(r.Usage.Source))
	}
	return v
}

func validSettings(s model.Settings) bool {
	return s.MaxConcurrency >= model.MinConcurrency && s.MaxConcurrency <= model.MaxConcurrency && s.MaxFixRounds >= 0 && s.MaxFixRounds <= model.MaxFixRoundsLimit
}

func settingsView(s model.Settings) map[string]any {
	return map[string]any{"schemaVersion": 1, "maxConcurrency": s.MaxConcurrency, "maxFixRounds": s.MaxFixRounds, "updatedAt": safeAtom(s.UpdatedAt)}
}

func validReceipt(r model.StopReceipt) bool {
	if !r.Accepted || safeAtom(r.CreatedAt) != r.CreatedAt || r.CreatedAt == "" {
		return false
	}
	if r.State == model.StopPending {
		return r.Outcome == nil && r.ProcessedAt == nil
	}
	return r.State == model.StopProcessed && r.ProcessedAt != nil && *r.ProcessedAt != "" && safeAtom(*r.ProcessedAt) == *r.ProcessedAt && r.Outcome != nil && (model.IsTerminalRunState(*r.Outcome) || *r.Outcome == model.RunUnknown)
}

func (s *Server) command(ctx context.Context, req server.Request) (server.Response, error) {
	cctx, cancel := context.WithTimeout(ctx, ControlTimeout)
	defer cancel()
	return s.Command(cctx, req)
}

func structured(text string, data any) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "structuredContent": data}
}

// Failures after sending may have persisted the operation. Never infer a failed
// write, fabricate a receipt, expose raw diagnostics or repeat a command here.
func controlFailure(err error, runID, requestID string) map[string]any {
	status, text := "unknown", "Control outcome is unknown. Query current state before deciding another write; do not automatically retry."
	var accepted any
	if errors.Is(err, server.ErrNoDaemon) || errors.Is(err, server.ErrUnsafeSocket) {
		status, text, accepted = "not_sent", unavailable, false
	}
	if status == "unknown" && requestID != "" {
		text = "Stop request outcome is unknown. Query get_run or explicitly repeat stop_run with the same runId and requestId to recover the receipt. Do not automatically retry."
	}
	r := structured(text, map[string]any{"schemaVersion": 1, "status": status, "accepted": accepted, "runId": runID, "requestId": requestID})
	r["isError"] = true
	return r
}

func commandFailure(code, runID, requestID string) map[string]any {
	var text string
	switch code {
	case server.CodeInvalid:
		text = "Meerkat daemon rejected invalid or conflicting control parameters."
	case server.CodeBusy:
		text = "Meerkat daemon is busy; the control request was rejected."
	case server.CodeNotFound:
		text = "Requested run or object was not found."
	case server.CodeClosed:
		text = "Meerkat daemon is shutting down; the control request was rejected."
	default:
		// Internal errors do not prove that a transaction never committed.
		return controlFailure(errors.New("unconfirmed control"), runID, requestID)
	}
	return toolError(text)
}
