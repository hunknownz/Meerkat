package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

const runA = "00000000-0000-4000-8000-000000000001"
const runB = "00000000-0000-4000-8000-000000000002"
const taskA = "00000000-0000-4000-8000-000000000003"
const stopA = "00000000-0000-4000-8000-000000000004"

func controlled(snap SnapshotFunc, cmd CommandFunc) *Server {
	return &Server{Snapshot: snap, Command: cmd, Assets: assets, initialize: true, initialized: true}
}

func callControl(t *testing.T, ctx context.Context, s *Server, name, args string) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 42, "method": "tools/call", "params": json.RawMessage(params)})
	var reply map[string]any
	if err := json.Unmarshal(s.handle(ctx, request), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func result(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	r, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", reply)
	}
	return r
}

func data(t *testing.T, reply map[string]any) map[string]any {
	t.Helper()
	r := result(t, reply)
	v, ok := r["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("no structured content: %v", r)
	}
	return v
}

func snapshotWith(runs ...model.Run) SnapshotFunc {
	return func(context.Context) (server.Response, error) {
		b, _ := json.Marshal(map[string]any{"schemaVersion": 1, "observedAt": "2026-10-03T12:00:00Z", "runs": runs, "settings": model.DefaultSettings(), "sessionToken": "PRIVATE-WRITE-TOKEN"})
		return server.Response{OK: true, Data: b}, nil
	}
}

func noCommand(context.Context, server.Request) (server.Response, error) {
	return server.Response{}, errors.New("unexpected command")
}

func TestControlToolsMetadata(t *testing.T) {
	s := controlled(snapshotWith(), noCommand)
	tools := s.toolList()
	if len(tools) != 21 {
		t.Fatalf("tools: %v", tools)
	}
	want := map[string]bool{ToolListRuns: true, ToolGetRun: true, ToolGetSettings: true, ToolStopRun: false, ToolUpdateSettings: false, ToolDispatchTasks: false, ToolGetOperation: true, ToolWaitOperation: true, ToolProposeBudget: true, ToolApplyBudget: false, ToolGetBudgetDecision: true, ToolInspectRecovery: true, ToolApplyRecovery: false, ToolGetRecovery: true, ToolRequestWrapUp: false, ToolGetControlReceipt: true, ToolSendInstruction: false, ToolInterventionReceipt: true, ToolStopFromUI: false}
	for _, raw := range tools[2:] {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		readonly, ok := want[name]
		if !ok {
			t.Fatalf("unimplemented tool %s", name)
		}
		delete(want, name)
		ann := tool["annotations"].(map[string]any)
		if ann["readOnlyHint"] != readonly || ann["openWorldHint"] != false || ann["destructiveHint"] != (name == ToolStopRun || name == ToolApplyBudget || name == ToolApplyRecovery || name == ToolRequestWrapUp || name == ToolSendInstruction || name == ToolStopFromUI) || ann["idempotentHint"] != (name != ToolUpdateSettings) {
			t.Fatalf("annotations %s: %v", name, ann)
		}
		if tool["execution"].(map[string]any)["taskSupport"] != "forbidden" {
			t.Fatal("task protocol advertised before implementation")
		}
		meta := tool["_meta"].(map[string]any)["ui"].(map[string]any)
		b, _ := json.Marshal(meta)
		visibility := `{"visibility":["model"]}`
		if name == ToolSendInstruction || name == ToolInterventionReceipt || name == ToolStopFromUI {
			visibility = `{"visibility":["app"]}`
		}
		if string(b) != visibility {
			t.Fatalf("monitor app gained controls: %s", b)
		}
	}
	if len(want) != 0 {
		t.Fatal(want)
	}
	// A monitor-only session exposes no write tools or contradictory instructions.
	s.Command = nil
	if len(s.toolList()) != 2 || errCode(callControl(t, context.Background(), s, ToolStopRun, `{}`)) != codeInvalidParams {
		t.Fatal("monitor-only session accepted control")
	}
	init := &Server{Command: noCommand}
	b := init.handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`))
	if !bytes.Contains(b, []byte("run controls")) || !bytes.Contains(b, []byte("monitor app exposes bounded human instructions")) {
		t.Fatalf("instructions %s", b)
	}
}

func TestControlStrictArgumentsNeverReachDaemon(t *testing.T) {
	var calls atomic.Int32
	s := controlled(func(context.Context) (server.Response, error) { calls.Add(1); return server.Response{}, nil }, func(context.Context, server.Request) (server.Response, error) {
		calls.Add(1)
		return server.Response{}, nil
	})
	cases := []struct{ name, args string }{
		{ToolListRuns, `{"limit":0}`}, {ToolListRuns, `{"limit":101}`}, {ToolListRuns, `{"limit":1.5}`}, {ToolListRuns, `{"limit":null}`},
		{ToolListRuns, `{"taskId":"../private"}`}, {ToolListRuns, `{"taskId":""}`}, {ToolListRuns, `{"state":""}`}, {ToolListRuns, `{"state":"dead"}`}, {ToolListRuns, `{"cursor":"` + strings.Repeat("a", 513) + `"}`},
		{ToolGetRun, `{}`}, {ToolGetRun, `{"runId":123}`}, {ToolGetSettings, `{"project":"x"}`},
		{ToolGetRun, `{"RunID":"` + runA + `"}`}, {ToolListRuns, `{"Limit":1}`},
		{ToolStopRun, `{"runId":"` + runA + `"}`}, {ToolStopRun, `{"runId":"` + runA + `","requestId":"` + stopA + `","pid":123}`},
		{ToolStopRun, `{"runId":"` + runA + `","runId":"` + runB + `","requestId":"` + stopA + `"}`},
		{ToolUpdateSettings, `{}`}, {ToolUpdateSettings, `{"maxConcurrency":0}`}, {ToolUpdateSettings, `{"maxConcurrency":5}`},
		{ToolUpdateSettings, `{"maxFixRounds":-1}`}, {ToolUpdateSettings, `{"maxFixRounds":3}`}, {ToolUpdateSettings, `{"maxConcurrency":2,"maxFixRounds":null}`},
		{ToolUpdateSettings, `{"maxConcurrency":2,"maxConcurrency":3}`}, {ToolUpdateSettings, `{"defaultProfiles":{}}`},
		{ToolUpdateSettings, `{"MaxConcurrency":1}`},
		{"dispatch_tasks", `{}`}, {"send_message", `{}`},
	}
	for _, c := range cases {
		if got := errCode(callControl(t, context.Background(), s, c.name, c.args)); got != codeInvalidParams {
			t.Fatalf("%s %s: code %d", c.name, c.args, got)
		}
	}
	for _, name := range []string{ToolListRuns, ToolGetSettings, ToolStopRun, ToolUpdateSettings} {
		for _, args := range []string{`null`, `[]`, `"x"`} {
			if got := errCode(callControl(t, context.Background(), s, name, args)); got != codeInvalidParams {
				t.Fatalf("%s %s: %d", name, args, got)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached daemon: %d", calls.Load())
	}
}

func TestRunQueriesAreBoundedAndPrivate(t *testing.T) {
	n := int64(91)
	r := model.Run{ID: runA, TaskID: taskA, State: model.RunUnknown, RecordedState: model.RunRunning, Executor: "pi", Role: "developer", Host: "PRIVATE-HOST", ProcessStartedAt: "PRIVATE-PROCESS", Summary: json.RawMessage(`{"prompt":"PRIVATE-PROMPT"}`), PID: new(int), Usage: &model.Usage{Tokens: model.TokenCounts{Output: &n}, UsageCompleteness: model.UsagePartial}, Events: []model.RunEvent{{Type: "state", Summary: "run_started"}, {Type: "tool", Summary: "sk-ant-abcdefghijklmnopqrstuvwxyz123"}}}
	s := controlled(snapshotWith(r, model.Run{ID: runB, TaskID: taskA, State: model.RunStopped}), noCommand)
	reply := callControl(t, context.Background(), s, ToolGetRun, `{"runId":"`+runA+`"}`)
	b, _ := json.Marshal(reply)
	for _, secret := range []string{"PRIVATE-", "sessionToken", "authEnv", "processStartedAt", `"pid"`, "sk-ant-"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("leak %s: %s", secret, b)
		}
	}
	v := data(t, reply)["run"].(map[string]any)
	usage := v["usage"].(map[string]any)
	tokens := usage["tokens"].(map[string]any)
	if v["state"] != "unknown" || v["recordedState"] != "running" || tokens["input"] != nil || tokens["output"] != float64(91) || tokens["total"] != nil || usage["estimatedCostUsd"] != nil {
		t.Fatalf("lost unknown: %v", v)
	}
	if len(v["recentActivity"].([]any)) != 1 {
		t.Fatal("unsafe activity included")
	}
	filtered := data(t, callControl(t, context.Background(), s, ToolListRuns, `{"taskId":"`+taskA+`","state":"stopped"}`))["items"].([]any)
	if len(filtered) != 1 || filtered[0].(map[string]any)["id"] != runB {
		t.Fatal(filtered)
	}
	if result(t, callControl(t, context.Background(), s, ToolGetRun, `{"runId":"`+stopA+`"}`))["isError"] != true {
		t.Fatal("missing run became empty success")
	}
}

func TestRunPaginationRejectsChangedDataAndFilters(t *testing.T) {
	a, b := model.Run{ID: runA, TaskID: taskA, State: model.RunRunning}, model.Run{ID: runB, TaskID: taskA, State: model.RunStopped}
	s := controlled(snapshotWith(b, a), noCommand)
	page := data(t, callControl(t, context.Background(), s, ToolListRuns, `{"limit":1}`))
	if page["items"].([]any)[0].(map[string]any)["id"] != runA {
		t.Fatal("unstable ordering")
	}
	cursor := page["nextCursor"].(string)
	args := `{"limit":1,"cursor":"` + cursor + `"}`
	next := data(t, callControl(t, context.Background(), s, ToolListRuns, args))
	if next["nextCursor"] != nil || next["items"].([]any)[0].(map[string]any)["id"] != runB {
		t.Fatal(next)
	}
	if result(t, callControl(t, context.Background(), s, ToolListRuns, `{"taskId":"`+taskA+`","cursor":"`+cursor+`"}`))["isError"] != true {
		t.Fatal("cursor reused with different filters")
	}
	a.State = model.RunStopped
	s.Snapshot = snapshotWith(a, b)
	if result(t, callControl(t, context.Background(), s, ToolListRuns, args))["isError"] != true {
		t.Fatal("stale cursor silently skipped runs")
	}
}

func TestStopAcceptanceIsNotCompletion(t *testing.T) {
	n := 0
	var state = model.StopPending
	s := controlled(snapshotWith(), func(ctx context.Context, req server.Request) (server.Response, error) {
		n++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > ControlTimeout || req.Op != "stop" || req.RunID != runA || req.RequestID != stopA {
			t.Fatalf("command: %+v", req)
		}
		rc := model.StopReceipt{RequestID: stopA, RunID: runA, Accepted: true, Duplicate: n > 1, State: state, CreatedAt: "2026-10-03T12:00:00Z"}
		if state == model.StopProcessed {
			outcome, at := model.RunUnknown, "2026-10-03T12:00:01Z"
			rc.Outcome, rc.ProcessedAt = &outcome, &at
		}
		b, _ := json.Marshal(rc)
		return server.Response{OK: true, Data: b}, nil
	})
	args := `{"runId":"` + runA + `","requestId":"` + stopA + `"}`
	rc := data(t, callControl(t, context.Background(), s, ToolStopRun, args))["receipt"].(map[string]any)
	if rc["accepted"] != true || rc["state"] != "pending" || rc["outcome"] != nil {
		t.Fatal(rc)
	}
	state = model.StopProcessed
	rc = data(t, callControl(t, context.Background(), s, ToolStopRun, args))["receipt"].(map[string]any)
	if rc["duplicate"] != true || rc["state"] != "processed" || rc["outcome"] != "unknown" {
		t.Fatal(rc)
	}
}

func TestControlFailuresPreserveUnknownAndNeverReplay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response server.Response
		err      error
		status   string
	}{
		{"lost", server.Response{}, errors.New("PRIVATE-PROVIDER-ERROR"), "unknown"},
		{"no-daemon", server.Response{}, server.ErrNoDaemon, "not_sent"},
		{"unsafe", server.Response{}, server.ErrUnsafeSocket, "not_sent"},
		{"malformed", server.Response{OK: true, Data: []byte(`{"accepted":true}`)}, nil, "unknown"},
		{"internal", server.Response{Code: server.CodeInternal, Error: "PRIVATE-SECRET"}, nil, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := controlled(snapshotWith(), func(context.Context, server.Request) (server.Response, error) { calls++; return tc.response, tc.err })
			reply := callControl(t, context.Background(), s, ToolStopRun, `{"runId":"`+runA+`","requestId":"`+stopA+`"}`)
			d := data(t, reply)
			if calls != 1 || result(t, reply)["isError"] != true || d["status"] != tc.status || d["runId"] != runA || d["requestId"] != stopA {
				t.Fatalf("%v; calls %d", reply, calls)
			}
			if tc.status == "unknown" && d["accepted"] != nil {
				t.Fatal("unknown became false")
			}
			b, _ := json.Marshal(reply)
			if bytes.Contains(b, []byte("PRIVATE-")) {
				t.Fatal("raw error exposed")
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	s := controlled(snapshotWith(), func(ctx context.Context, _ server.Request) (server.Response, error) {
		calls++
		<-ctx.Done()
		return server.Response{}, ctx.Err()
	})
	if d := data(t, callControl(t, ctx, s, ToolUpdateSettings, `{"maxConcurrency":1}`)); calls != 1 || d["status"] != "unknown" {
		t.Fatal(d)
	}
	for _, name := range []string{ToolStopRun, ToolUpdateSettings} {
		s.Command = func(context.Context, server.Request) (server.Response, error) {
			return server.Response{Code: server.CodeInvalid, Error: "PRIVATE-SECRET"}, nil
		}
		args := `{"maxConcurrency":1}`
		if name == ToolStopRun {
			args = `{"runId":"` + runA + `","requestId":"` + stopA + `"}`
		}
		r := callControl(t, context.Background(), s, name, args)
		b, _ := json.Marshal(r)
		if result(t, r)["isError"] != true || bytes.Contains(b, []byte("PRIVATE-")) {
			t.Fatal(string(b))
		}
	}
}

func TestSettingsControlsUseExistingAuthority(t *testing.T) {
	s := controlled(snapshotWith(model.Run{ID: runA}), func(_ context.Context, req server.Request) (server.Response, error) {
		if req.Op != "settings" || req.RunID != "" || req.Apply {
			t.Fatalf("unexpected operation %+v", req)
		}
		var patch model.SettingsPatch
		if json.Unmarshal(req.Input, &patch) != nil || patch.MaxConcurrency == nil || *patch.MaxConcurrency != 4 || patch.MaxFixRounds == nil || *patch.MaxFixRounds != 0 || patch.DefaultProfiles != nil {
			t.Fatalf("patch %s", req.Input)
		}
		b, _ := json.Marshal(model.Settings{MaxConcurrency: 4, MaxFixRounds: 0, DefaultProfiles: map[string]map[string]string{"PRIVATE": {"developer": "PRIVATE-PROFILE"}}})
		return server.Response{OK: true, Data: b}, nil
	})
	read := data(t, callControl(t, context.Background(), s, ToolGetSettings, `{}`))
	if read["maxConcurrency"] != float64(2) || read["maxFixRounds"] != float64(2) {
		t.Fatal(read)
	}
	r := callControl(t, context.Background(), s, ToolUpdateSettings, `{"maxConcurrency":4,"maxFixRounds":0}`)
	d := data(t, r)
	b, _ := json.Marshal(r)
	if d["maxConcurrency"] != float64(4) || d["maxFixRounds"] != float64(0) || bytes.Contains(b, []byte("PRIVATE")) || bytes.Contains(b, []byte("defaultProfiles")) {
		t.Fatal(string(b))
	}
}

func TestMCPControlsThroughPrivateSocketAndSQLite(t *testing.T) {
	// A short private temporary path fits Unix socket limits on macOS. This
	// service has no paid requests and never accesses the user's active daemon.
	dir, err := os.MkdirTemp("", "mk-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	err = st.Update(func(s *model.State) error {
		s.Projects = []model.Project{{ID: "demo", Name: "Demo"}}
		c := model.Context{ID: "ctx", ProjectID: "demo", Version: 1, Digest: "sha256:example", Text: "PRIVATE-CONTEXT"}
		s.Contexts = []model.Context{c}
		s.Tasks = []model.Task{{ID: taskA, ProjectID: "demo", ContextRef: c.Ref(), Worktree: dir, State: model.TaskDelivered}}
		s.Runs = []model.Run{{ID: runA, TaskID: taskA, State: model.RunSucceeded}, {ID: runB, TaskID: taskA, State: model.RunSucceeded}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(st, server.Registry())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := server.New(c, st, nil)
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	ln, err := server.ListenUnix(dir)
	if err != nil {
		svc.Shutdown()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.ServeUnix(ln) }()
	t.Cleanup(func() {
		ln.Close()
		svc.Shutdown()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop_run","arguments":{"runId":"` + runA + `","requestId":"` + stopA + `"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"stop_run","arguments":{"runId":"` + runA + `","requestId":"` + stopA + `"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"update_settings","arguments":{"maxConcurrency":1,"maxFixRounds":0}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_settings","arguments":{}}}
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"stop_run","arguments":{"runId":"` + runB + `","requestId":"` + stopA + `"}}}
`
	var output bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(input), &output, dir, assets); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("replies %d: %s", len(lines), output.String())
	}
	replies := []map[string]any{}
	for _, line := range lines {
		var r map[string]any
		if json.Unmarshal([]byte(line), &r) != nil {
			t.Fatal(line)
		}
		replies = append(replies, r)
	}
	r1 := data(t, replies[1])["receipt"].(map[string]any)
	r2 := data(t, replies[2])["receipt"].(map[string]any)
	if r1["accepted"] != true || r1["duplicate"] != false || r2["duplicate"] != true || r1["createdAt"] != r2["createdAt"] {
		t.Fatalf("receipts %v %v", r1, r2)
	}
	if result(t, replies[5])["isError"] != true {
		t.Fatal("request ID reused across runs")
	}
	set := data(t, replies[4])
	if set["maxConcurrency"] != float64(1) || set["maxFixRounds"] != float64(0) {
		t.Fatal(set)
	}
	select {
	case <-svc.Done():
		t.Fatal("MCP EOF shut down the authority")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if r, err := server.Call(ctx, dir, server.Request{Op: "health"}); err != nil || !r.OK {
		t.Fatalf("service after MCP EOF: %+v %v", r, err)
	}
	if strings.Contains(output.String(), "PRIVATE-") || strings.Contains(output.String(), svc.Token()) {
		t.Fatal("private data leaked")
	}
	stored, err := st.RequestStop(runA, stopA)
	if err != nil || !stored.Duplicate || !stored.Accepted {
		t.Fatalf("no durable receipt %+v %v", stored, err)
	}
}
