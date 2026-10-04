package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func TestAppInstructionReceiptPrivacyBindingAndNoReplay(t *testing.T) {
	calls := 0
	rc := model.ControlReceipt{RequestID: stopA, TaskID: taskA, RunID: runA, SessionID: ptr(runB), Kind: "instruction", State: model.ControlAcknowledged, Disposition: ptr("queued"), CreatedAt: "2026-10-04T00:00:00Z", UpdatedAt: "2026-10-04T00:00:00Z", RunState: model.RunRunning}
	s := controlled(snapshotWith(), func(_ context.Context, r server.Request) (server.Response, error) {
		calls++
		if r.Op == "request-instruction" {
			var in model.WrapUpInput
			if json.Unmarshal(r.Input, &in) != nil || !model.ValidInstructionInput(in) || in.Message != "PRIVATE_HUMAN_MESSAGE" {
				t.Fatal(r)
			}
		}
		b, _ := json.Marshal(rc)
		return server.Response{OK: true, Data: b}, nil
	})
	args := `{"runId":"` + runA + `","sessionId":"` + runB + `","requestId":"` + stopA + `","message":"PRIVATE_HUMAN_MESSAGE"}`
	reply := callControl(t, context.Background(), s, ToolSendInstruction, args)
	b, _ := json.Marshal(reply)
	if strings.Contains(string(b), "PRIVATE_HUMAN_MESSAGE") || strings.Contains(string(b), "Direct user instruction") {
		t.Fatal("private text in reply")
	}
	result := reply["result"].(map[string]any)
	if result["_meta"].(map[string]any)["receipt"] == nil || data(t, reply)["receipt"] != nil {
		t.Fatal("receipt not isolated from model result", reply)
	}
	if calls != 1 {
		t.Fatal("write replay", calls)
	}
	if errCode(callControl(t, context.Background(), s, ToolSendInstruction, strings.Replace(args, `"message":`, `"message":"first","message":`, 1))) != codeInvalidParams || calls != 1 {
		t.Fatal("duplicate argument reached owner")
	}
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		calls++
		return server.Response{}, errors.New("PRIVATE_ERROR")
	}
	if data(t, callControl(t, context.Background(), s, ToolSendInstruction, args))["status"] != "unknown" || calls != 2 {
		t.Fatal("lost reply replayed")
	}
	for _, v := range interventionTools() {
		meta := v.(map[string]any)["_meta"].(map[string]any)["ui"].(map[string]any)
		visibility := meta["visibility"].([]string)
		if len(visibility) != 1 || visibility[0] != "app" {
			t.Fatal(meta)
		}
	}
	// A valid receipt for a different session cannot confirm this write.
	rc.SessionID = ptr(runA)
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		b, _ := json.Marshal(rc)
		return server.Response{OK: true, Data: b}, nil
	}
	if data(t, callControl(t, context.Background(), s, ToolSendInstruction, args))["status"] != "unknown" {
		t.Fatal("wrong session confirmed")
	}
}
