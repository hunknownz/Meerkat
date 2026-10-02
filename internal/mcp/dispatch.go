package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const (
	ToolDispatchTasks = "dispatch_tasks"
	ToolGetOperation  = "get_operation"
	ToolWaitOperation = "wait_operation"
)

func dispatchTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	schema := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	tool := func(name, description string, input map[string]any, readonly bool) any {
		return map[string]any{"name": name, "description": description, "inputSchema": input,
			"annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			"execution":   map[string]any{"taskSupport": "forbidden"},
			"_meta":       map[string]any{"ui": map[string]any{"visibility": []string{"model"}}}}
	}
	lookup := schema(map[string]any{"operationId": uuid, "requestId": uuid})
	lookup["oneOf"] = []any{map[string]any{"required": []string{"operationId"}}, map[string]any{"required": []string{"requestId"}}}
	return []any{
		tool(ToolDispatchTasks, "Persist 1..50 already prepared frozen tasks and return an operationId immediately. Requires user authorization and a stable requestId. Acceptance is not delivery. On a lost reply query get_operation with requestId; never retry automatically or invent a new requestId.", schema(map[string]any{
			"requestId": uuid, "taskIds": map[string]any{"type": "array", "items": uuid, "minItems": 1, "maxItems": 50, "uniqueItems": true},
			"resume": map[string]any{"type": "boolean"}, "acknowledge": map[string]any{"type": "boolean"},
		}, "requestId", "taskIds"), false),
		tool(ToolGetOperation, "Read durable scheduling and each task's actual outcome by operationId, or recover a lost dispatch reply by requestId. Completed is not proof of delivery. Unknown never means stopped.", lookup, true),
		tool(ToolWaitOperation, "Wait at most one second for an operation, then return its current snapshot. Timeout or caller disconnect does not cancel execution. Repeat reads only as needed; use stop_run for an explicitly authorized stop.", schema(map[string]any{"operationId": uuid,
			"waitMillis": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000}}, "operationId"), true),
	}
}

func (s *Server) dispatchTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	var req server.Request
	requestID, operationID := "", ""
	switch name {
	case ToolDispatchTasks:
		var p model.DispatchRequest
		if e := decodeArgs(raw, &p, "requestId", "taskIds", "resume", "acknowledge"); e != nil {
			return nil, e
		}
		if !uuidPattern.MatchString(p.RequestID) || len(p.TaskIDs) < 1 || len(p.TaskIDs) > 50 {
			return nil, invalidParams("requestId and 1..50 taskIds required")
		}
		seen := map[string]bool{}
		for _, id := range p.TaskIDs {
			if !uuidPattern.MatchString(id) || seen[id] {
				return nil, invalidParams("taskIds must be unique lowercase UUIDs")
			}
			seen[id] = true
		}
		requestID = p.RequestID
		req = server.Request{Op: "dispatch", Tasks: p.TaskIDs, RequestID: p.RequestID, Resume: p.Resume, Acknowledge: p.Acknowledge}
	case ToolGetOperation, ToolWaitOperation:
		var p struct {
			OperationID string `json:"operationId"`
			RequestID   string `json:"requestId"`
			WaitMillis  *int   `json:"waitMillis"`
		}
		allowed := []string{"operationId", "requestId"}
		if name == ToolWaitOperation {
			allowed = []string{"operationId", "waitMillis"}
		}
		if e := decodeArgs(raw, &p, allowed...); e != nil {
			return nil, e
		}
		if (p.OperationID == "") == (p.RequestID == "") || p.OperationID != "" && !uuidPattern.MatchString(p.OperationID) || p.RequestID != "" && !uuidPattern.MatchString(p.RequestID) {
			return nil, invalidParams("one lowercase operationId or requestId required")
		}
		wait := 0
		if name == ToolWaitOperation {
			wait = 500
			if p.WaitMillis != nil {
				wait = *p.WaitMillis
			}
			if wait < 0 || wait > 1000 {
				return nil, invalidParams("waitMillis must be 0..1000")
			}
		}
		requestID, operationID = p.RequestID, p.OperationID
		req = server.Request{Op: "operation", OperationID: p.OperationID, RequestID: p.RequestID, WaitMillis: wait}
		if name == ToolWaitOperation {
			req.Op = "wait-operation"
		}
	default:
		return nil, invalidParams("unknown tool")
	}
	resp, err := s.command(ctx, req)
	if err != nil {
		if name == ToolDispatchTasks {
			status := "unknown"
			if errors.Is(err, server.ErrNoDaemon) || errors.Is(err, server.ErrUnsafeSocket) {
				status = "not_sent"
			}
			return structured("Dispatch reply unavailable. Query get_operation with requestId before another write; do not retry automatically.", map[string]any{"schemaVersion": 1, "status": status, "requestId": requestID}), nil
		}
		return toolError("Operation query unavailable; no execution was canceled."), nil
	}
	if !resp.OK {
		if name == ToolDispatchTasks && resp.Code == server.CodeInternal {
			return uncertainDispatch(requestID), nil
		}
		return commandFailure(resp.Code, "", requestID), nil
	}
	var o model.Operation
	var receipt model.DispatchReceipt
	if name == ToolDispatchTasks {
		if json.Unmarshal(resp.Data, &receipt) != nil || !receipt.Accepted || receipt.Operation.RequestID != requestID {
			return uncertainDispatch(requestID), nil
		}
		o = receipt.Operation
	} else if json.Unmarshal(resp.Data, &o) != nil {
		return toolError("Unreadable operation reply."), nil
	}
	if !validPublicOperation(o) || operationID != "" && o.ID != operationID || requestID != "" && o.RequestID != requestID {
		if name == ToolDispatchTasks {
			return uncertainDispatch(requestID), nil
		}
		return toolError("Unreadable operation reply."), nil
	}
	if name == ToolDispatchTasks {
		ids := []string{}
		for _, m := range o.Tasks {
			ids = append(ids, m.TaskID)
		}
		if !slices.Equal(ids, req.Tasks) {
			return uncertainDispatch(requestID), nil
		}
	}
	o = publicOperation(o)
	if name == ToolDispatchTasks {
		receipt.Operation = o
		return structured("Dispatch persisted; inspect operation task outcomes for delivery.", map[string]any{"schemaVersion": 1, "receipt": receipt}), nil
	}
	return structured("Operation state: "+o.State+". Inspect each task's actual outcome.", map[string]any{"schemaVersion": 1, "operation": o}), nil
}

func uncertainDispatch(requestID string) any {
	return structured("Dispatch result is unknown. Query get_operation with requestId; do not retry automatically.", map[string]any{"schemaVersion": 1, "status": "unknown", "requestId": requestID})
}

var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)
var candidatePattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func validPublicOperation(o model.Operation) bool {
	if o.SchemaVersion != 1 || !uuidPattern.MatchString(o.ID) || !uuidPattern.MatchString(o.RequestID) ||
		!slices.Contains([]string{model.OperationQueued, model.OperationRunning, model.OperationCompleted, model.OperationUnknown}, o.State) ||
		!slices.Contains([]string{"workflow", "delegate"}, o.Mode) || len(o.Tasks) < 1 || len(o.Tasks) > 50 || o.FrozenFixRounds < 0 || o.FrozenFixRounds > model.MaxFixRoundsLimit {
		return false
	}
	for _, at := range []string{o.CreatedAt, o.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return false
		}
	}
	for _, at := range []*string{o.StartedAt, o.EndedAt} {
		if at != nil {
			if _, err := time.Parse(time.RFC3339Nano, *at); err != nil {
				return false
			}
		}
	}
	if o.State == model.OperationQueued && (o.StartedAt != nil || o.EndedAt != nil) ||
		o.State == model.OperationRunning && (o.StartedAt == nil || o.EndedAt != nil) ||
		model.OperationSettled(o.State) && (o.StartedAt == nil || o.EndedAt == nil) {
		return false
	}
	seen := map[string]bool{}
	for _, m := range o.Tasks {
		if !uuidPattern.MatchString(m.TaskID) || seen[m.TaskID] || !slices.Contains([]string{model.MemberQueued, model.MemberRunning, model.MemberCompleted, model.MemberUnknown}, m.State) ||
			!slices.Contains(model.TaskStates, m.TaskState) || m.CandidateSHA != nil && !candidatePattern.MatchString(*m.CandidateSHA) || m.ResumeRole != nil && !model.IsRole(*m.ResumeRole) {
			return false
		}
		seen[m.TaskID] = true
		for _, at := range []*string{m.StartedAt, m.EndedAt} {
			if at != nil {
				if _, err := time.Parse(time.RFC3339Nano, *at); err != nil {
					return false
				}
			}
		}
		if m.State == model.MemberQueued && (m.StartedAt != nil || m.EndedAt != nil) ||
			m.State == model.MemberRunning && (m.StartedAt == nil || m.EndedAt != nil) ||
			(m.State == model.MemberCompleted || m.State == model.MemberUnknown) && m.EndedAt == nil ||
			o.State == model.OperationQueued && m.State != model.MemberQueued ||
			o.State == model.OperationCompleted && m.State != model.MemberCompleted ||
			o.State == model.OperationUnknown && (m.State == model.MemberQueued || m.State == model.MemberRunning) {
			return false
		}
	}
	return true
}

func publicOperation(o model.Operation) model.Operation {
	o = o.Public()
	reason := func(p *string) *string {
		if p == nil {
			return nil
		}
		s := *p
		if !reasonPattern.MatchString(s) || model.LooksLikeCredential(s) {
			s = "redacted"
		}
		return &s
	}
	o.Reason = reason(o.Reason)
	for i := range o.Tasks {
		o.Tasks[i].StateReason = reason(o.Tasks[i].StateReason)
	}
	return o
}
