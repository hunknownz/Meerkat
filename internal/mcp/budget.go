package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const ToolProposeBudget = "propose_budget"
const ToolApplyBudget = "apply_budget_decision"
const ToolGetBudgetDecision = "get_budget_decision"

func budgetTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	digest := map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 512}
	number := func(max int64) any { return map[string]any{"type": "integer", "minimum": 0, "maximum": max} }
	schema := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	props := map[string]any{"taskId": uuid, "addTokens": number(model.MaxTaskTokens), "addWallSeconds": number(model.MaxTaskWallSeconds), "reason": text}
	proposalProps := map[string]any{}
	for k, v := range props {
		proposalProps[k] = v
	}
	proposalProps["schemaVersion"] = map[string]any{"type": "integer", "const": 1}
	proposalProps["revision"] = number(511)
	proposalProps["currentTokens"] = number(model.MaxTaskTokens)
	proposalProps["currentWallSeconds"] = number(model.MaxTaskWallSeconds)
	proposalProps["contractDigest"] = digest
	proposalProps["evidenceDigest"] = digest
	tool := func(name, description string, input any, readonly bool) any {
		return map[string]any{"name": name, "description": description, "inputSchema": input, "annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": !readonly, "idempotentHint": true, "openWorldHint": false}, "execution": map[string]any{"taskSupport": "forbidden"}, "_meta": map[string]any{"ui": map[string]any{"visibility": []string{"model"}}}}
	}
	return []any{
		tool(ToolProposeBudget, "Prepare an additional token/time proposal for a verified paused or budget-stopped task. Read-only; this grants nothing and never resumes a task. Review the exact amounts and obtain actual user authorization before applying.", schema(props, "taskId", "addTokens", "addWallSeconds", "reason"), true),
		tool(ToolApplyBudget, "Append an explicitly user-authorized budget decision. Requires the exact proposal, a stable requestId, authorizationRef and apply:true. The reference records authorization; it does not authenticate human intent. Never infer permission from a proposal or retry a lost reply automatically. Query get_budget_decision by requestId; task resume is separate.", schema(map[string]any{"proposal": schema(proposalProps, "taskId", "addTokens", "addWallSeconds", "reason", "schemaVersion", "revision", "currentTokens", "currentWallSeconds", "contractDigest", "evidenceDigest"), "requestId": uuid, "authorizationRef": text, "apply": map[string]any{"type": "boolean", "const": true}}, "proposal", "requestId", "authorizationRef", "apply"), false),
		tool(ToolGetBudgetDecision, "Read the durable public receipt for a previously applied decision by requestId. Use this to resolve a lost write reply. Not found never grants allowance or resumes work.", schema(map[string]any{"requestId": uuid}, "requestId"), true),
	}
}
func (s *Server) budgetTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	var req server.Request
	id := ""
	switch name {
	case ToolProposeBudget:
		var in model.BudgetIncrease
		if e := decodeArgs(raw, &in, "taskId", "addTokens", "addWallSeconds", "reason"); e != nil {
			return nil, e
		}
		if !model.ValidBudgetIncrease(in) {
			return nil, invalidParams("invalid additional budget")
		}
		b, _ := json.Marshal(in)
		req = server.Request{Op: "propose-budget", Input: b}
	case ToolApplyBudget:
		var in model.BudgetDecisionInput
		if e := decodeArgs(raw, &in, "proposal", "requestId", "authorizationRef", "apply"); e != nil {
			return nil, e
		}
		if !model.ValidBudgetDecision(in) {
			return nil, invalidParams("explicit apply, exact proposal and authorization reference required")
		}
		id = in.RequestID
		b, _ := json.Marshal(in)
		req = server.Request{Op: "apply-budget-decision", Input: b}
	case ToolGetBudgetDecision:
		var in struct {
			RequestID string `json:"requestId"`
		}
		if e := decodeArgs(raw, &in, "requestId"); e != nil {
			return nil, e
		}
		if !uuidPattern.MatchString(in.RequestID) {
			return nil, invalidParams("lowercase requestId UUID required")
		}
		id = in.RequestID
		req = server.Request{Op: "budget-decision", RequestID: id}
	default:
		return nil, invalidParams("unknown budget tool")
	}
	resp, e := s.command(ctx, req)
	if e != nil || !resp.OK {
		if name == ToolApplyBudget && (e != nil || resp.Code == server.CodeInternal) {
			status := "unknown"
			if errors.Is(e, server.ErrNoDaemon) || errors.Is(e, server.ErrUnsafeSocket) {
				status = "not_sent"
			}
			r := structured("Budget decision outcome is unconfirmed. Query get_budget_decision with the same requestId before another write; do not retry automatically.", map[string]any{"schemaVersion": 1, "status": status, "requestId": id})
			r["isError"] = true
			return r, nil
		}
		if e != nil {
			return toolError("Budget query unavailable."), nil
		}
		return commandFailure(resp.Code, "", ""), nil
	}
	if name == ToolProposeBudget {
		var p model.BudgetProposal
		var wanted model.BudgetIncrease
		json.Unmarshal(req.Input, &wanted)
		if json.Unmarshal(resp.Data, &p) != nil || !model.ValidBudgetProposal(p) || p.BudgetIncrease != wanted {
			return toolError("Unreadable budget proposal."), nil
		}
		return structured("Proposal only; no additional allowance was granted.", map[string]any{"schemaVersion": 1, "proposal": p}), nil
	}
	var d model.BudgetDecisionSummary
	if json.Unmarshal(resp.Data, &d) != nil || d.RequestID != id || d.Revision < 1 || d.Revision > 512 || d.AddTokens < 0 || d.AddTokens > model.MaxTaskTokens || d.AddWallSeconds < 0 || d.AddWallSeconds > model.MaxTaskWallSeconds || d.AddTokens+d.AddWallSeconds == 0 || !model.ValidBudgetText(d.Reason) {
		return toolError("Budget receipt is unreadable. Query get_budget_decision before another write."), nil
	}
	if name == ToolApplyBudget {
		var in model.BudgetDecisionInput
		json.Unmarshal(req.Input, &in)
		if d.Revision != in.Proposal.Revision+1 || d.AddTokens != in.Proposal.AddTokens || d.AddWallSeconds != in.Proposal.AddWallSeconds || d.Reason != in.Proposal.Reason {
			return toolError("Budget receipt does not match the proposal. Query get_budget_decision before another write."), nil
		}
	}
	if _, e := time.Parse(time.RFC3339Nano, d.CreatedAt); e != nil {
		return toolError("Budget receipt is unreadable."), nil
	}
	return structured("Additional allowance receipt; applying this decision does not resume execution.", map[string]any{"schemaVersion": 1, "receipt": d}), nil
}
