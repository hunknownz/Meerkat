package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const ToolRequestWrapUp = "request_wrap_up"
const ToolGetControlReceipt = "get_control_receipt"

func runControlTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	schema := func(props map[string]any, required ...string) any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	tool := func(name, description string, input any, readonly bool) any {
		return map[string]any{"name": name, "description": description, "inputSchema": input, "annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": !readonly, "idempotentHint": true, "openWorldHint": false}, "execution": map[string]any{"taskSupport": "forbidden"}, "_meta": map[string]any{"ui": map[string]any{"visibility": []string{"model"}}}}
	}
	return []any{
		tool(ToolRequestWrapUp, "Persist one bounded wrap-up instruction for the exact owned Run and Session after actual user authorization. Requires stable requestId, authorizationRef and apply:true. The reference records evidence, not human authentication. Scope and budget stay frozen. Accepted is saved; acknowledged means queued/handled by the executor, not completed or stopped. On a missing reply query get_control_receipt with the same UUID; never retry automatically.", schema(map[string]any{"runId": uuid, "sessionId": uuid, "requestId": uuid, "authorizationRef": map[string]any{"type": "string", "minLength": 1, "maxLength": 512}, "apply": map[string]any{"type": "boolean", "const": true}}, "runId", "sessionId", "requestId", "authorizationRef", "apply"), false),
		tool(ToolGetControlReceipt, "Read the durable wrap-up or retained stop receipt by requestId without resending. Protocol state and current Run result are separate. Unknown never proves process exit. A missing receipt grants no permission to repeat a write.", schema(map[string]any{"requestId": uuid}, "requestId"), true),
	}
}

func (s *Server) runControlTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	var in model.WrapUpInput
	var req server.Request
	id := ""
	if name == ToolRequestWrapUp {
		if e := decodeArgs(raw, &in, "runId", "sessionId", "requestId", "authorizationRef", "apply"); e != nil {
			return nil, e
		}
		if !model.ValidWrapUpInput(in) {
			return nil, invalidParams("explicit wrap-up authorization required")
		}
		id = in.RequestID
		b, _ := json.Marshal(in)
		req = server.Request{Op: "request-wrap-up", Input: b}
	} else if name == ToolGetControlReceipt {
		var v struct {
			RequestID string `json:"requestId"`
		}
		if e := decodeArgs(raw, &v, "requestId"); e != nil {
			return nil, e
		}
		if !model.ValidControlID(v.RequestID) {
			return nil, invalidParams("request UUID required")
		}
		id = v.RequestID
		req = server.Request{Op: "control-receipt", RequestID: id}
	} else {
		return nil, invalidParams("unknown control tool")
	}
	resp, e := s.command(ctx, req)
	if e != nil || !resp.OK {
		if name == ToolRequestWrapUp && (e != nil || resp.Code == server.CodeInternal) {
			status := "unknown"
			if errors.Is(e, server.ErrNoDaemon) || errors.Is(e, server.ErrUnsafeSocket) {
				status = "not_sent"
			}
			r := structured("Wrap-up outcome unconfirmed. Query get_control_receipt using the same requestId before any further write. Do not retry automatically.", map[string]any{"schemaVersion": 1, "status": status, "requestId": id})
			r["isError"] = true
			return r, nil
		}
		if e != nil {
			return toolError("Control receipt query unavailable."), nil
		}
		return commandFailure(resp.Code, "", ""), nil
	}
	var v model.ControlReceipt
	if json.Unmarshal(resp.Data, &v) != nil || v.RequestID != id || !model.ValidControlReceipt(v) || name == ToolRequestWrapUp && (v.Kind != "wrap_up" || v.RunID != in.RunID || v.SessionID == nil || *v.SessionID != in.SessionID) {
		return toolError("Control receipt unreadable. Query get_control_receipt before another write."), nil
	}
	return structured("Control receipt: "+v.State+"; Run state: "+v.RunState+". Protocol acknowledgement does not prove delivery or stop.", map[string]any{"schemaVersion": 1, "receipt": v}), nil
}
