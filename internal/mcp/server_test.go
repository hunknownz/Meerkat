package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/web"
)

const fakeSnap = `{"schemaVersion":1,"observedAt":"2025-01-02T03:04:05Z","controller":{},"projects":[{"id":"p"}],` +
	`"contexts":[],"tasks":[{"id":"t1","goal":"secret-task-goal"},{"id":"t2"}],"runs":[{"id":"r1","authEnv":"X"}],` +
	`"deliveries":[],"reviews":[],"profiles":[],"settings":{},"counts":{"running":1,"queued":2,"unknown":0},"usage":{},"sessionToken":"tok"}`

const fakeIcon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor"><path d="M0 0h1"/></svg>`

var assets = fstest.MapFS{
	MonitorAsset: {Data: []byte("<!doctype html><title>m</title>")},
	IconAsset:    {Data: []byte(fakeIcon)},
}

// checkIcons asserts one SVG data-URI icon that decodes to the embedded logo bytes.
func checkIcons(t *testing.T, v any, want string) {
	t.Helper()
	list, ok := v.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("icons %v", v)
	}
	ic := list[0].(map[string]any)
	src, _ := ic["src"].(string)
	const prefix = "data:image/svg+xml;base64,"
	if ic["mimeType"] != "image/svg+xml" || !strings.HasPrefix(src, prefix) {
		t.Fatalf("icon %v", ic)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(src, prefix))
	if err != nil || string(raw) != want {
		t.Fatalf("icon payload %q", raw)
	}
}

type session struct {
	t     *testing.T
	in    *io.PipeWriter
	out   *bufio.Scanner
	done  chan error
	calls *atomic.Int32
}

func start(t *testing.T, snap SnapshotFunc) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var calls atomic.Int32
	s := &Server{Assets: assets}
	if snap != nil {
		s.Snapshot = func(ctx context.Context) (server.Response, error) { calls.Add(1); return snap(ctx) }
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), inR, outW); outW.Close() }()
	sc := bufio.NewScanner(outR)
	sc.Buffer(nil, MaxOutputBytes)
	ss := &session{t: t, in: inW, out: sc, done: done, calls: &calls}
	t.Cleanup(func() { inW.Close() })
	return ss
}

func (s *session) send(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *session) recv() map[string]any {
	s.t.Helper()
	ch := make(chan map[string]any, 1)
	go func() {
		if !s.out.Scan() {
			ch <- nil
			return
		}
		var m map[string]any
		if json.Unmarshal(s.out.Bytes(), &m) != nil {
			m = map[string]any{"bad": s.out.Text()}
		}
		ch <- m
	}()
	select {
	case m := <-ch:
		if m == nil || m["jsonrpc"] != "2.0" {
			s.t.Fatalf("bad reply %v", m)
		}
		return m
	case <-time.After(5 * time.Second):
		s.t.Fatal("no reply")
	}
	return nil
}

func (s *session) rpc(line string) map[string]any { s.t.Helper(); s.send(line); return s.recv() }

func errCode(m map[string]any) int {
	e, _ := m["error"].(map[string]any)
	if e == nil {
		return 0
	}
	return int(e["code"].(float64))
}

func (s *session) init() {
	s.t.Helper()
	r := s.rpc(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`)
	if r["result"] == nil {
		s.t.Fatalf("init %v", r)
	}
	s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func okSnap(context.Context) (server.Response, error) {
	return server.Response{OK: true, Data: json.RawMessage(fakeSnap)}, nil
}

func TestLifecycleAndVersions(t *testing.T) {
	for req, want := range map[string]string{"2025-11-25": "2025-11-25", "2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26", "2024-11-05": "2024-11-05", "1999-01-01": LatestProtocol} {
		s := start(t, okSnap)
		if c := errCode(s.rpc(`{"jsonrpc":"2.0","id":0,"method":"tools/list"}`)); c != codeInvalidRequest {
			t.Fatalf("tools before init: %d", c)
		}
		if r := s.rpc(`{"jsonrpc":"2.0","id":"p","method":"ping"}`); r["id"] != "p" || r["result"] == nil {
			t.Fatalf("ping %v", r)
		}
		r := s.rpc(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + req + `","capabilities":{}}}`)
		res := r["result"].(map[string]any)
		info := res["serverInfo"].(map[string]any)
		if res["protocolVersion"] != want || info["name"] != "meerkat" || info["version"] != server.Version {
			t.Fatalf("init %v", r)
		}
		checkIcons(t, info["icons"], fakeIcon)
		caps := res["capabilities"].(map[string]any)
		if caps["tools"] == nil || caps["resources"] == nil {
			t.Fatal("caps")
		}
		// initialize answered but initialized notification not yet received
		if c := errCode(s.rpc(`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)); c != codeInvalidRequest {
			t.Fatalf("resources before initialized: %d", c)
		}
		s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		if r := s.rpc(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`); r["result"] == nil {
			t.Fatalf("tools after init %v", r)
		}
		if c := errCode(s.rpc(`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)); c != codeInvalidRequest {
			t.Fatal("double initialize")
		}
		s.in.Close()
		if err := <-s.done; err != nil {
			t.Fatalf("EOF must be clean: %v", err)
		}
	}
}

func TestErrorsAndNotifications(t *testing.T) {
	s := start(t, okSnap)
	if r := s.rpc(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); errCode(r) != codeInvalidParams {
		t.Fatalf("init no version %v", r)
	}
	s.init()
	// notifications (known, unknown, malformed params) never get a reply
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`)
	s.send(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"x"}}`)
	s.send(`{"jsonrpc":"2.0","method":"bogus"}`)
	s.send(`{"jsonrpc":"2.0","id":7,"result":{}}`) // client response: ignored
	s.send("")
	cases := []struct {
		line string
		code int
	}{
		{`{not json`, codeParse},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, codeInvalidRequest},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":{},"method":"ping"}`, codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/subscribe","params":{"uri":"ui://meerkat/monitor"}}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/write"}`, codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"ping","params":[1]}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stop","arguments":{}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute"}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"open_monitor","arguments":{"runId":"x"}}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"open_monitor","arguments":[]}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"open_monitor","extra":1}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file:///etc/passwd"}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ui://meerkat/monitor/../x"}}`, codeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{}}`, codeInvalidParams},
	}
	for _, c := range cases {
		if got := errCode(s.rpc(c.line)); got != c.code {
			t.Fatalf("%s -> %d want %d", c.line, got, c.code)
		}
	}
	// oversized line is rejected and the session continues
	if c := errCode(s.rpc(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", MaxInputBytes) + `"}}`)); c != codeInvalidRequest {
		t.Fatalf("oversize %d", c)
	}
	if r := s.rpc(`{"jsonrpc":"2.0","id":99,"method":"ping"}`); r["id"] != float64(99) || r["result"] == nil {
		t.Fatalf("after oversize %v", r)
	}
	if n := s.calls.Load(); n != 0 {
		t.Fatalf("rejected calls reached daemon %d times", n)
	}
}

func TestToolsListMetadata(t *testing.T) {
	s := start(t, okSnap)
	s.init()
	tools := s.rpc(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tools %v", tools)
	}
	byName := map[string]map[string]any{}
	for _, x := range tools {
		m := x.(map[string]any)
		byName[m["name"].(string)] = m
		ann := m["annotations"].(map[string]any)
		if ann["readOnlyHint"] != true || ann["destructiveHint"] != false || ann["idempotentHint"] != true || ann["openWorldHint"] != false {
			t.Fatalf("annotations %v", ann)
		}
		sch := m["inputSchema"].(map[string]any)
		wantProperties := 0
		if m["name"] == ToolGetSnapshot {
			wantProperties = 1
		}
		if sch["type"] != "object" || len(sch["properties"].(map[string]any)) != wantProperties || sch["additionalProperties"] != false {
			t.Fatalf("schema %v", sch)
		}
		if m["_meta"].(map[string]any)["ui"].(map[string]any)["resourceUri"] != MonitorURI {
			t.Fatal("resourceUri")
		}
	}
	om, gs := byName[ToolOpenMonitor]["_meta"].(map[string]any), byName[ToolGetSnapshot]["_meta"].(map[string]any)
	if b, _ := json.Marshal(om["ui"].(map[string]any)["visibility"]); string(b) != `["model","app"]` {
		t.Fatal(string(b))
	}
	if b, _ := json.Marshal(om["openai/ui"]); string(b) != `{"entrypoints":[{"type":"global"},{"type":"thread"}]}` {
		t.Fatal(string(b))
	}
	if b, _ := json.Marshal(gs["ui"].(map[string]any)["visibility"]); string(b) != `["app"]` || gs["openai/ui"] != nil {
		t.Fatalf("get_monitor_snapshot meta %v", gs)
	}
	if byName[ToolOpenMonitor]["title"] != "Meerkat" {
		t.Fatalf("open_monitor title %v", byName[ToolOpenMonitor]["title"])
	}
	checkIcons(t, byName[ToolOpenMonitor]["icons"], fakeIcon)
}

func TestIconsOmittedWithoutLogo(t *testing.T) {
	s := &Server{Assets: fstest.MapFS{MonitorAsset: {Data: []byte("x")}}}
	out := s.handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`))
	if strings.Contains(string(out), `"icons"`) || !strings.Contains(string(out), `"protocolVersion":"2025-11-25"`) {
		t.Fatal(string(out))
	}
	for _, tool := range s.toolList() {
		if _, ok := tool.(map[string]any)["icons"]; ok {
			t.Fatal("tool icon without logo")
		}
	}
}

func TestEmbeddedLogoIsSelectedMonochromeSVG(t *testing.T) {
	svg, err := fs.ReadFile(web.Assets(), IconAsset)
	if err != nil {
		t.Fatal(err)
	}
	icons := (&Server{Assets: web.Assets()}).icons()
	checkIcons(t, icons, string(svg))
	if !strings.Contains(string(svg), `fill="currentColor"`) || strings.Contains(string(svg), "<image") {
		t.Fatal("logo must be the selected monochrome vector")
	}
}

func TestResources(t *testing.T) {
	s := start(t, okSnap)
	s.init()
	list := s.rpc(`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)["result"].(map[string]any)["resources"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	r := list[0].(map[string]any)
	if r["uri"] != MonitorURI || r["mimeType"] != "text/html;profile=mcp-app" || r["title"] != "Meerkat Agent Monitor" {
		t.Fatal(r)
	}
	read := s.rpc(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"ui://meerkat/monitor"}}`)
	c := read["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
	if c["text"] != "<!doctype html><title>m</title>" || c["mimeType"] != MonitorMIME || c["uri"] != MonitorURI {
		t.Fatal(c)
	}
	if b, _ := json.Marshal(c["_meta"]); string(b) != `{"ui":{"csp":{"connectDomains":[],"resourceDomains":[]}}}` {
		t.Fatal(string(b))
	}
	// missing asset: internal error, no paths leaked
	s2 := &Server{Assets: fstest.MapFS{}, initialize: true, initialized: true}
	out := s2.handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ui://meerkat/monitor"}}`))
	if !strings.Contains(string(out), `"code":-32603`) || strings.Contains(string(out), MonitorAsset) {
		t.Fatal(string(out))
	}
}

func TestToolCallSnapshot(t *testing.T) {
	var deadline time.Duration
	s := start(t, func(ctx context.Context) (server.Response, error) {
		d, ok := ctx.Deadline()
		if ok {
			deadline = time.Until(d)
		}
		return okSnap(ctx)
	})
	s.init()
	for i, name := range []string{ToolOpenMonitor, ToolGetSnapshot} {
		args := []string{``, `,"arguments":{}`}[i]
		r := s.rpc(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"` + name + `"` + args + `,"_meta":{"progressToken":1}}}`)
		res := r["result"].(map[string]any)
		if res["isError"] == true {
			t.Fatal(res)
		}
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		if text != "Meerkat monitor: 2 tasks, 1 runs (1 running, 2 queued, 0 unknown)." {
			t.Fatal(text)
		}
		sc := res["structuredContent"].(map[string]any)
		counts := sc["counts"].(map[string]any)
		if len(sc) != 3 || sc["schemaVersion"] != float64(1) || sc["observedAt"] != "2025-01-02T03:04:05Z" || len(counts) != 8 || counts["tasks"] != float64(2) || counts["runs"] != float64(1) || counts["running"] != float64(1) || counts["queued"] != float64(2) || counts["unknown"] != float64(0) {
			t.Fatal(sc)
		}
		meta := res["_meta"].(map[string]any)
		snap := meta["snapshot"].(map[string]any)
		if len(snap["tasks"].([]any)) != 2 || snap["sessionToken"] != nil || snap["runs"].([]any)[0].(map[string]any)["authEnv"] != nil {
			t.Fatalf("snapshot %v", snap)
		}
		if la, ok := meta["legacyActive"].([]any); !ok || len(la) != 0 {
			t.Fatal("legacyActive")
		}
		all, _ := json.Marshal(r)
		if strings.Count(string(all), "secret-task-goal") != 1 || strings.Contains(string(all), "tok\"") {
			t.Fatal("task data outside _meta.snapshot or token leaked")
		}
	}
	if deadline <= 0 || deadline > SnapshotTimeout {
		t.Fatalf("timeout %v", deadline)
	}
	if s.calls.Load() != 2 {
		t.Fatal("calls")
	}
}

func TestConditionalSnapshotPreservesHistoryAndFreshness(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fakeSnap), &raw); err != nil {
		t.Fatal(err)
	}
	raw["controller"] = json.RawMessage(`{"state":"idle","heartbeatAt":"2025-01-02T03:04:05Z"}`)
	// A large historical field must be returned once, never repeated just for a heartbeat.
	raw["historyFixture"], _ = json.Marshal(strings.Repeat("preserved-history-", 12000))
	s := start(t, func(context.Context) (server.Response, error) {
		b, err := json.Marshal(raw)
		return server.Response{OK: err == nil, Data: b}, err
	})
	s.init()
	read := func(args string) map[string]any {
		return s.rpc(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_monitor_snapshot","arguments":` + args + `}}`)["result"].(map[string]any)
	}
	first := read(`{}`)
	meta := first["_meta"].(map[string]any)
	revision := meta["snapshotRevision"].(string)
	if !validRevision(revision) || meta["snapshot"] == nil {
		t.Fatal(meta)
	}
	full, _ := json.Marshal(first)
	raw["observedAt"] = json.RawMessage(`"2025-01-02T03:04:10Z"`)
	raw["controller"] = json.RawMessage(`{"state":"idle","heartbeatAt":"2025-01-02T03:04:09Z"}`)
	unchanged := read(`{"sinceRevision":"` + revision + `"}`)
	m := unchanged["_meta"].(map[string]any)
	small, _ := json.Marshal(unchanged)
	if m["snapshot"] != nil || m["snapshotUnchanged"] != true || m["snapshotRevision"] != revision || m["observedAt"] != "2025-01-02T03:04:10Z" {
		t.Fatal(m)
	}
	if len(small) > 1500 || len(full) < 200000 || bytes.Contains(small, []byte("preserved-history")) {
		t.Fatalf("full=%d unchanged=%d", len(full), len(small))
	}
	if m["controller"].(map[string]any)["heartbeatAt"] != "2025-01-02T03:04:09Z" {
		t.Fatal(m)
	}
	// Legacy readers still receive the complete snapshot. An incorrect revision also recovers fully.
	for _, args := range []string{`{}`, `{"sinceRevision":"` + strings.Repeat("f", 64) + `"}`} {
		if read(args)["_meta"].(map[string]any)["snapshot"] == nil {
			t.Fatal("missing recovery snapshot")
		}
	}
	// A real change must invalidate the revision, even if counts have not changed.
	raw["historyFixture"], _ = json.Marshal("changed-review-or-control")
	changed := read(`{"sinceRevision":"` + revision + `"}`)["_meta"].(map[string]any)
	if changed["snapshot"] == nil || changed["snapshotRevision"] == revision || changed["snapshotUnchanged"] == true {
		t.Fatal(changed)
	}
	next := changed["snapshotRevision"].(string)
	raw["controller"] = json.RawMessage(`{"state":"unknown","heartbeatAt":null}`)
	if read(`{"sinceRevision":"` + next + `"}`)["_meta"].(map[string]any)["snapshot"] == nil {
		t.Fatal("controller change hidden")
	}
}

func TestConditionalSnapshotRejectsInvalidArguments(t *testing.T) {
	s := start(t, okSnap)
	s.init()
	for _, args := range []string{`{"sinceRevision":null}`, `{"sinceRevision":""}`, `{"sinceRevision":"short"}`, `{"sinceRevision":"` + strings.Repeat("A", 64) + `"}`, `{"other":true}`, `[]`} {
		r := s.rpc(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_monitor_snapshot","arguments":` + args + `}}`)
		if r["error"] == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	if s.calls.Load() != 0 {
		t.Fatal("invalid input reached daemon")
	}
	if s.rpc(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"open_monitor","arguments":{"sinceRevision":"` + strings.Repeat("a", 64) + `"}}}`)["error"] == nil {
		t.Fatal("open_monitor accepted revision")
	}
}

func TestToolCallDaemonErrors(t *testing.T) {
	for _, f := range []SnapshotFunc{
		func(context.Context) (server.Response, error) {
			return server.Response{}, errors.New("dial unix /private/path/meerkat.sock: secret")
		},
		func(context.Context) (server.Response, error) {
			return server.Response{Code: "internal", Error: "/private/path boom"}, nil
		},
		func(context.Context) (server.Response, error) {
			return server.Response{OK: true, Data: json.RawMessage(`[1]`)}, nil
		},
		nil,
	} {
		s := start(t, f)
		s.init()
		r := s.rpc(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"open_monitor"}}`)
		res := r["result"].(map[string]any)
		b, _ := json.Marshal(r)
		if res["isError"] != true || res["structuredContent"] != nil || strings.Contains(string(b), "/private") || strings.Contains(string(b), "secret") {
			t.Fatal(string(b))
		}
	}
}

// TestServeRealSocketNoDaemon uses the real server.Call path: no daemon is started.
func TestServeRealSocketNoDaemon(t *testing.T) {
	d, err := os.MkdirTemp("", "mkm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_monitor_snapshot","arguments":{}}}`) // no trailing newline
	var out strings.Builder
	if err := Serve(context.Background(), in, &out, d, assets); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "meerkat serve") || !strings.Contains(lines[1], `"isError":true`) || strings.Contains(out.String(), d) {
		t.Fatal(out.String())
	}
	if _, err := os.Lstat(filepath.Join(d, server.SocketName)); !os.IsNotExist(err) {
		t.Fatal("daemon started")
	}
}

func TestContextCancelStops(t *testing.T) {
	inR, inW := io.Pipe()
	defer inW.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (&Server{}).Serve(ctx, inR, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
}
