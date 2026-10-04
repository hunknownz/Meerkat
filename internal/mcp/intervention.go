package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

const (
	ToolSendInstruction     = "send_run_instruction"
	ToolInterventionReceipt = "get_intervention_receipt"
	ToolStopFromUI          = "stop_run_from_ui"
)

// App visibility is a host routing hint. The private owner socket remains the
// service boundary; the recorded authorization reference is not authentication.
func interventionTools() []any {
	uuid := map[string]any{"type": "string", "pattern": uuidPattern.String()}
	tool := func(name, description string, props map[string]any, required []string, readonly bool) any {
		return map[string]any{"name": name, "description": description,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
			"annotations": map[string]any{"readOnlyHint": readonly, "destructiveHint": !readonly, "idempotentHint": true, "openWorldHint": false},
			"execution":   map[string]any{"taskSupport": "forbidden"},
			"_meta":       map[string]any{"ui": map[string]any{"visibility": []string{"app"}}}}
	}
	return []any{
		tool(ToolSendInstruction, "Send a bounded human instruction to the exact owned Run and Session. Keep the request UUID. Acknowledgement means queued/handled, not completed. A lost reply must be queried, never automatically replayed.", map[string]any{"runId": uuid, "sessionId": uuid, "requestId": uuid, "message": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000}}, []string{"runId", "sessionId", "requestId", "message"}, false),
		tool(ToolInterventionReceipt, "Query one durable control receipt without resending. No instruction text or private contract is returned.", map[string]any{"requestId": uuid}, []string{"requestId"}, true),
		tool(ToolStopFromUI, "Record a human stop request for one Run. Accepted is not stopped. Keep the request UUID and query after a missing reply.", map[string]any{"runId": uuid, "requestId": uuid}, []string{"runId", "requestId"}, false),
	}
}

func (s *Server) interventionTool(ctx context.Context, name string, raw json.RawMessage) (any, *rpcError) {
	var in struct {
		RunID     string `json:"runId"`
		SessionID string `json:"sessionId"`
		RequestID string `json:"requestId"`
		Message   string `json:"message"`
	}
	allowed := []string{"requestId"}
	if name == ToolSendInstruction {
		allowed = []string{"runId", "sessionId", "requestId", "message"}
	}
	if name == ToolStopFromUI {
		allowed = []string{"runId", "requestId"}
	}
	if e := decodeArgs(raw, &in, allowed...); e != nil {
		return nil, e
	}
	if !model.ValidControlID(in.RequestID) || name != ToolInterventionReceipt && !model.ValidControlID(in.RunID) {
		return nil, invalidParams("valid control UUIDs required")
	}
	req := server.Request{Op: "control-receipt", RequestID: in.RequestID}
	if name == ToolSendInstruction {
		v := model.WrapUpInput{Kind: "instruction", RunID: in.RunID, SessionID: in.SessionID, RequestID: in.RequestID, Message: in.Message, AuthorizationRef: "Direct user instruction in Meerkat UI", Apply: true}
		if !model.ValidInstructionInput(v) {
			return nil, invalidParams("bounded instruction without credentials required")
		}
		b, _ := json.Marshal(v)
		req = server.Request{Op: "request-instruction", Input: b}
	} else if name == ToolStopFromUI {
		req = server.Request{Op: "stop", RunID: in.RunID, RequestID: in.RequestID}
	}
	resp, err := s.command(ctx, req)
	if err != nil || !resp.OK {
		status := "unknown"
		if errors.Is(err, server.ErrNoDaemon) || errors.Is(err, server.ErrUnsafeSocket) {
			status = "not_sent"
		}
		if err == nil && resp.Code != server.CodeInternal {
			status = "rejected"
		}
		r := structured("Control result unconfirmed. Query the receipt with the same UUID before another write.", map[string]any{"schemaVersion": 1, "status": status, "requestId": in.RequestID})
		r["isError"] = true
		return r, nil
	}
	if name == ToolStopFromUI {
		// A read may recover a lost response, but never repeats the stop write.
		resp, err = s.command(ctx, server.Request{Op: "control-receipt", RequestID: in.RequestID})
		if err != nil || !resp.OK {
			return interventionUnknown(in.RequestID), nil
		}
	}
	var rc model.ControlReceipt
	if json.Unmarshal(resp.Data, &rc) != nil || !model.ValidControlReceipt(rc) || rc.RequestID != in.RequestID || name != ToolInterventionReceipt && rc.RunID != in.RunID || name == ToolSendInstruction && (rc.Kind != "instruction" || rc.SessionID == nil || *rc.SessionID != in.SessionID) || name == ToolStopFromUI && rc.Kind != "stop" {
		return interventionUnknown(in.RequestID), nil
	}
	r := structured("Control receipt: "+rc.State+". Protocol acknowledgement does not prove completion.", map[string]any{"schemaVersion": 1, "status": rc.State, "requestId": rc.RequestID})
	r["_meta"] = map[string]any{"receipt": rc}
	return r, nil
}

func interventionUnknown(id string) map[string]any {
	r := structured("Receipt unreadable. Query using the same UUID; do not replay the write.", map[string]any{"schemaVersion": 1, "status": "unknown", "requestId": id})
	r["isError"] = true
	return r
}
