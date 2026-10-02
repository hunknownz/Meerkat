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

func dispatchFixture() model.Operation {
	return model.Operation{SchemaVersion: 1, ID: runA, RequestID: stopA, State: model.OperationQueued, Mode: "workflow",
		CreatedAt: "2026-10-03T12:00:00Z", UpdatedAt: "2026-10-03T12:00:00Z", FrozenFixRounds: 2,
		RequestDigest: "PRIVATE_REQUEST_HASH", Tasks: []model.OperationTask{{TaskID: taskA, State: model.MemberQueued, TaskState: model.TaskQueued, ContractDigest: "PRIVATE_CONTRACT_HASH"}}}
}

func TestDispatchMCPValidatesArgumentsBeforeCommand(t *testing.T) {
	calls := 0
	s := controlled(snapshotWith(), func(context.Context, server.Request) (server.Response, error) { calls++; return server.Response{}, nil })
	for _, tc := range []struct{ name, args string }{
		{ToolDispatchTasks, `{}`}, {ToolDispatchTasks, `{"requestId":"` + stopA + `","taskIds":[]}`},
		{ToolDispatchTasks, `{"requestId":"` + stopA + `","taskIds":["` + taskA + `","` + taskA + `"]}`},
		{ToolDispatchTasks, `{"requestId":"` + stopA + `","taskIds":["` + taskA + `"],"resume":null}`},
		{ToolDispatchTasks, `{"requestId":"` + stopA + `","taskIds":["` + taskA + `"],"goal":"override"}`},
		{ToolDispatchTasks, `{"RequestId":"` + stopA + `","taskIds":["` + taskA + `"]}`},
		{ToolGetOperation, `{}`}, {ToolGetOperation, `{"requestId":"` + stopA + `","operationId":"` + runA + `"}`},
		{ToolWaitOperation, `{"operationId":"` + runA + `","waitMillis":1001}`},
		{ToolWaitOperation, `{"operationId":"` + runA + `","waitMillis":1.5}`},
		{ToolWaitOperation, `{"requestId":"` + stopA + `"}`},
	} {
		if code := errCode(callControl(t, context.Background(), s, tc.name, tc.args)); code != codeInvalidParams {
			t.Fatalf("%s %s: %d", tc.name, tc.args, code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid argument reached daemon")
	}
}

func TestDispatchMCPReceiptReadWaitAndNoPrivateDigests(t *testing.T) {
	o := dispatchFixture()
	o.Reason = ptr("/private/path ZENMUX_SECRET_VALUE")
	s := controlled(snapshotWith(), func(ctx context.Context, req server.Request) (server.Response, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded command")
		}
		if req.Op == "dispatch" {
			if req.RequestID != stopA || len(req.Tasks) != 1 || req.Tasks[0] != taskA {
				t.Fatal(req)
			}
			b, _ := json.Marshal(model.DispatchReceipt{Accepted: true, Operation: o})
			return server.Response{OK: true, Data: b}, nil
		}
		if req.Op == "wait-operation" && req.WaitMillis != 1000 {
			t.Fatal(req)
		}
		b, _ := json.Marshal(o)
		return server.Response{OK: true, Data: b}, nil
	})
	for _, tc := range []struct{ name, args string }{
		{ToolDispatchTasks, `{"requestId":"` + stopA + `","taskIds":["` + taskA + `"]}`},
		{ToolGetOperation, `{"requestId":"` + stopA + `"}`},
		{ToolWaitOperation, `{"operationId":"` + runA + `","waitMillis":1000}`},
	} {
		r := callControl(t, context.Background(), s, tc.name, tc.args)
		b, _ := json.Marshal(data(t, r))
		for _, secret := range []string{"PRIVATE_REQUEST_HASH", "PRIVATE_CONTRACT_HASH", "/private/path", "ZENMUX_SECRET_VALUE"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("private data exposed: %s", secret)
			}
		}
		if !strings.Contains(string(b), `"candidateSha":null`) {
			t.Fatal("unknown candidate became known")
		}
	}
}

func TestDispatchMCPLostReplyNeverRetries(t *testing.T) {
	calls := 0
	s := controlled(snapshotWith(), func(context.Context, server.Request) (server.Response, error) {
		calls++
		return server.Response{}, errors.New("PRIVATE_ERROR")
	})
	args := `{"requestId":"` + stopA + `","taskIds":["` + taskA + `"]}`
	d := data(t, callControl(t, context.Background(), s, ToolDispatchTasks, args))
	if calls != 1 || d["status"] != "unknown" || d["requestId"] != stopA {
		t.Fatal(d, calls)
	}
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		return server.Response{}, server.ErrNoDaemon
	}
	if d := data(t, callControl(t, context.Background(), s, ToolDispatchTasks, args)); d["status"] != "not_sent" {
		t.Fatal(d)
	}
	s.Command = func(context.Context, server.Request) (server.Response, error) {
		o := dispatchFixture()
		o.RequestID = runB
		b, _ := json.Marshal(model.DispatchReceipt{Accepted: true, Operation: o})
		return server.Response{OK: true, Data: b}, nil
	}
	if d := data(t, callControl(t, context.Background(), s, ToolDispatchTasks, args)); d["status"] != "unknown" {
		t.Fatal("mismatched receipt accepted", d)
	}
}

func TestDispatchMCPRejectsReorderedReceipt(t *testing.T) {
	s := controlled(snapshotWith(), func(context.Context, server.Request) (server.Response, error) {
		o := dispatchFixture()
		m := o.Tasks[0]
		m.TaskID = runB
		o.Tasks = []model.OperationTask{m, o.Tasks[0]}
		b, _ := json.Marshal(model.DispatchReceipt{Accepted: true, Operation: o})
		return server.Response{OK: true, Data: b}, nil
	})
	args := `{"requestId":"` + stopA + `","taskIds":["` + taskA + `","` + runB + `"]}`
	if d := data(t, callControl(t, context.Background(), s, ToolDispatchTasks, args)); d["status"] != "unknown" {
		t.Fatal("receipt changed execution order", d)
	}
}

func TestOperationMCPRejectsUnsettledCompletion(t *testing.T) {
	s := controlled(snapshotWith(), func(context.Context, server.Request) (server.Response, error) {
		o := dispatchFixture()
		at := o.CreatedAt
		o.State, o.StartedAt, o.EndedAt = model.OperationCompleted, &at, &at
		b, _ := json.Marshal(o)
		return server.Response{OK: true, Data: b}, nil
	})
	r := callControl(t, context.Background(), s, ToolGetOperation, `{"operationId":"`+runA+`"}`)
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"isError":true`) {
		t.Fatal("queued member exposed as completed", string(b))
	}
}

func ptr(s string) *string { return &s }
