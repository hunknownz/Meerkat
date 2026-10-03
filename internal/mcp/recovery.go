package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const ToolInspectRecovery = "inspect_recovery"
const ToolApplyRecovery = "apply_recovery"
const ToolGetRecovery = "get_recovery_decision"

func recoveryTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	schema := func(props map[string]any, required ...string) any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	proposal := schema(map[string]any{"schemaVersion": map[string]any{"type": "integer", "const": 1}, "taskId": uuid, "runId": uuid, "completionDigest": digest, "evidenceDigest": digest}, "schemaVersion", "taskId", "runId", "completionDigest", "evidenceDigest")
	tool := func(name, description string, input any, readonly bool) any {
		return map[string]any{"name": name, "description": description, "inputSchema": input, "annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": !readonly, "idempotentHint": true, "openWorldHint": false}, "execution": map[string]any{"taskSupport": "forbidden"}, "_meta": map[string]any{"ui": map[string]any{"visibility": []string{"model"}}}}
	}
	return []any{
		tool(ToolInspectRecovery, "Read-only inspection of an interrupted task's Go-recorded completion, process absence, contract, Git, session and request accounting. A verified proposal grants nothing and starts no executor. Missing or uncertain evidence stays blocked.", schema(map[string]any{"taskId": uuid}, "taskId"), true),
		tool(ToolApplyRecovery, "Recover exactly one previously verified completed step after actual user authorization. Requires exact proposal, stable requestId, authorizationRef and apply:true. Free text records authorization; it cannot authenticate human intent. Never signals or replays an agent. Workflow remains stopped for separate resume. On a lost reply query get_recovery_decision; never retry automatically.", schema(map[string]any{"proposal": proposal, "requestId": uuid, "authorizationRef": map[string]any{"type": "string", "minLength": 1, "maxLength": 512}, "apply": map[string]any{"type": "boolean", "const": true}}, "proposal", "requestId", "authorizationRef", "apply"), false),
		tool(ToolGetRecovery, "Read the public durable recovery receipt by requestId. Use after a lost write reply before further changes. This does not run or resume a task.", schema(map[string]any{"requestId": uuid}, "requestId"), true),
	}
}
func (s *Server) recoveryTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	var req server.Request
	var applied model.RecoveryInput
	id := ""
	switch name {
	case ToolInspectRecovery:
		var v struct {
			TaskID string `json:"taskId"`
		}
		if e := decodeArgs(raw, &v, "taskId"); e != nil {
			return nil, e
		}
		if !uuidPattern.MatchString(v.TaskID) {
			return nil, invalidParams("task UUID required")
		}
		req = server.Request{Op: "inspect-recovery", TaskID: v.TaskID}
	case ToolApplyRecovery:
		if e := decodeArgs(raw, &applied, "proposal", "requestId", "authorizationRef", "apply"); e != nil {
			return nil, e
		}
		if !model.ValidRecoveryInput(applied) {
			return nil, invalidParams("explicit recovery authorization required")
		}
		id = applied.RequestID
		b, _ := json.Marshal(applied)
		req = server.Request{Op: "apply-recovery", Input: b}
	case ToolGetRecovery:
		var v struct {
			RequestID string `json:"requestId"`
		}
		if e := decodeArgs(raw, &v, "requestId"); e != nil {
			return nil, e
		}
		if !uuidPattern.MatchString(v.RequestID) {
			return nil, invalidParams("request UUID required")
		}
		id = v.RequestID
		req = server.Request{Op: "recovery-decision", RequestID: id}
	default:
		return nil, invalidParams("unknown recovery tool")
	}
	resp, e := s.command(ctx, req)
	if e != nil || !resp.OK {
		if name == ToolApplyRecovery && (e != nil || resp.Code == server.CodeInternal) {
			status := "unknown"
			if errors.Is(e, server.ErrNoDaemon) || errors.Is(e, server.ErrUnsafeSocket) {
				status = "not_sent"
			}
			r := structured("Recovery outcome unconfirmed. Query get_recovery_decision using the same requestId before any further write. Do not retry automatically.", map[string]any{"schemaVersion": 1, "status": status, "requestId": id})
			r["isError"] = true
			return r, nil
		}
		if e != nil {
			return toolError("Recovery query unavailable."), nil
		}
		return commandFailure(resp.Code, "", ""), nil
	}
	if name == ToolInspectRecovery {
		var v model.RecoveryInspection
		if json.Unmarshal(resp.Data, &v) != nil || v.SchemaVersion != 1 || v.TaskID != req.TaskID || (v.Status != "blocked" && v.Status != "verified") || (v.Status == "verified") != (v.Proposal != nil) || v.Proposal != nil && (!model.ValidRecoveryProposal(*v.Proposal) || v.Proposal.TaskID != v.TaskID || v.Proposal.RunID != v.RunID) {
			return toolError("Recovery inspection is unreadable."), nil
		}
		return structured("Read-only recovery inspection; no state was changed.", map[string]any{"schemaVersion": 1, "inspection": v}), nil
	}
	var v model.RecoveryReceipt
	if json.Unmarshal(resp.Data, &v) != nil || v.RequestID != id || !uuidPattern.MatchString(v.TaskID) || !uuidPattern.MatchString(v.RunID) {
		return toolError("Recovery receipt unreadable; query before another write."), nil
	}
	if _, e := time.Parse(time.RFC3339Nano, v.CreatedAt); e != nil {
		return toolError("Recovery receipt unreadable."), nil
	}
	if name == ToolApplyRecovery && (v.TaskID != applied.Proposal.TaskID || v.RunID != applied.Proposal.RunID) {
		return toolError("Recovery receipt does not match the proposal."), nil
	}
	return structured("Verified step recovery receipt; recovery does not execute an agent.", map[string]any{"schemaVersion": 1, "receipt": v}), nil
}
