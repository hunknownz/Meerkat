package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

func privDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "mk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func realService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(privDir(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(st, Registry())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(c, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown(); st.Close() })
	return svc, st
}

func startHTTP(t *testing.T, svc *Service) (*httptest.Server, string) {
	srv := httptest.NewUnstartedServer(nil)
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	srv.Config.Handler = svc.HTTPHandler(port)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, "127.0.0.1:" + strconv.Itoa(port)
}

func do(t *testing.T, method, url string, body string, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res, m
}

func TestWorkflowEnvelopeAndAssets(t *testing.T) {
	svc, _ := realService(t)
	srv, _ := startHTTP(t, svc)
	res, m := do(t, "GET", srv.URL+"/api/workflow", "", nil)
	if res.StatusCode != 200 || m["ok"] != true || m["sessionToken"] != svc.Token() {
		t.Fatalf("envelope %d %v", res.StatusCode, m["ok"])
	}
	if la, ok := m["legacyActive"].([]any); !ok || len(la) != 0 {
		t.Fatal("legacyActive must be an empty array")
	}
	data := m["data"].(map[string]any)
	for _, k := range []string{"schemaVersion", "observedAt", "controller", "projects", "contexts", "tasks", "runs", "deliveries", "reviews", "profiles", "settings", "counts", "usage"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("snapshot missing %s", k)
		}
	}
	if data["schemaVersion"].(float64) != 1 {
		t.Fatal("schemaVersion")
	}
	for _, p := range []string{"/", "/mount/meerkat-ui.js", "/mount/meerkat-ui.css"} {
		r, err := http.Get(srv.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("asset %s", p)
		}
		r.Body.Close()
	}
	if r, _ := do(t, "GET", srv.URL+"/api/health", "", nil); r.StatusCode != 200 {
		t.Fatal("health")
	}
}

func TestHostOriginAndWriteGuards(t *testing.T) {
	svc, _ := realService(t)
	srv, host := startHTTP(t, svc)
	origin := "http://" + host
	good := map[string]string{"Origin": origin, "X-Meerkat-Token": svc.Token(), "Content-Type": "application/json"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	if r, _ := do(t, "GET", srv.URL+"/api/workflow", "", map[string]string{"Host": "evil.example:" + strings.Split(host, ":")[1]}); r.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("rebinding host accepted: %d", r.StatusCode)
	}
	if r, _ := do(t, "GET", srv.URL+"/api/workflow", "", map[string]string{"Host": "127.0.0.1:1"}); r.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal("wrong port accepted")
	}
	if r, _ := do(t, "GET", srv.URL+"/api/workflow", "", map[string]string{"Origin": "http://evil.example"}); r.StatusCode != 403 {
		t.Fatal("foreign origin GET accepted")
	}
	body := `{"maxConcurrency":3}`
	for name, h := range map[string]map[string]string{
		"no token": with("X-Meerkat-Token", ""), "bad token": with("X-Meerkat-Token", "x"),
		"no origin": with("Origin", ""), "foreign origin": with("Origin", "http://evil.example"),
		"text": with("Content-Type", "text/plain"),
	} {
		if r, _ := do(t, "PUT", srv.URL+"/api/workflow/settings", body, h); r.StatusCode < 400 {
			t.Fatalf("%s accepted", name)
		}
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/workflow/settings", `{"maxConcurrency":3,"x":"`+strings.Repeat("a", 9000)+`"}`, good); r.StatusCode != 413 {
		t.Fatalf("oversize body: %d", r.StatusCode)
	}
	if r, _ := do(t, "PUT", srv.URL+"/api/workflow/settings", `{"maxConcurrency":9}`, good); r.StatusCode != 400 {
		t.Fatal("invalid settings accepted")
	}
	r, m := do(t, "PUT", srv.URL+"/api/workflow/settings", body, good)
	if r.StatusCode != 200 || m["data"].(map[string]any)["maxConcurrency"].(float64) != 3 {
		t.Fatalf("settings %d %v", r.StatusCode, m)
	}
	// Browser cannot execute/prepare; wrong methods rejected.
	for _, p := range []string{"/api/workflow/execute", "/api/workflow/prepare", "/api/issues/read", "/api/migrate"} {
		if r, _ := do(t, "POST", srv.URL+p, "{}", good); r.StatusCode != 404 && r.StatusCode != 405 {
			t.Fatalf("%s reachable: %d", p, r.StatusCode)
		}
	}
	if r, _ := do(t, "DELETE", srv.URL+"/api/workflow/settings", "", good); r.StatusCode != 405 {
		t.Fatalf("DELETE: %d", r.StatusCode)
	}
	// Unknown run: sanitized not-found / invalid, never 202.
	rid := newUUID()
	r, m = do(t, "POST", srv.URL+"/api/workflow/runs/"+newUUID()+"/stop", `{"requestId":"`+rid+`"}`, good)
	if r.StatusCode == 202 || m["ok"] != false {
		t.Fatalf("unknown run stop: %d", r.StatusCode)
	}
}

func TestSSEUpdates(t *testing.T) {
	svc, _ := realService(t)
	srv, host := startHTTP(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/workflow/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal("sse open")
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	next := func() string {
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: state") {
				return sc.Text()
			}
		}
		t.Fatal("stream ended")
		return ""
	}
	next() // initial
	do(t, "PUT", srv.URL+"/api/workflow/settings", `{"maxFixRounds":1}`, map[string]string{"Origin": "http://" + host, "X-Meerkat-Token": svc.Token(), "Content-Type": "application/json"})
	next() // change observed
}

// fakeCore blocks Execute until released or canceled.
type fakeCore struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	stops   int
	closed  bool
}

func (f *fakeCore) Prepare(raw []byte) (model.Task, error) { return model.Task{ID: "t1"}, nil }
func (f *fakeCore) Execute(ctx context.Context, ids []string, resume, ack bool) (core.Result, error) {
	close(f.started)
	select {
	case <-f.release:
		return core.Result{Tasks: []core.TaskResult{{ID: ids[0], State: model.TaskDelivered}}}, nil
	case <-ctx.Done():
		return core.Result{Stopped: "shutdown"}, nil
	}
}
func (f *fakeCore) Snapshot() (core.Snapshot, error) {
	return core.Snapshot{SchemaVersion: 1, ObservedAt: time.Now().String()}, nil
}
func (f *fakeCore) Stop(runID, requestID string) (model.StopReceipt, error) {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	return model.StopReceipt{RunID: runID, RequestID: requestID, Accepted: true, State: model.StopPending}, nil
}
func (f *fakeCore) Settings(p model.SettingsPatch) (model.Settings, error) {
	return model.Settings{}, nil
}
func (f *fakeCore) Close() error { f.closed = true; return nil }

func TestUnixConcurrentStopSnapshotDuringExecute(t *testing.T) {
	dir := privDir(t)
	fc := &fakeCore{started: make(chan struct{}), release: make(chan struct{})}
	svc, _ := New(fc, nil, nil)
	ln, err := ListenUnix(dir)
	if err != nil {
		t.Fatal(err)
	}
	go svc.ServeUnix(ln)
	fi, _ := os.Stat(SocketPath(dir))
	if fi.Mode().Perm() != 0o600 {
		t.Fatal("socket not 0600")
	}
	if _, err := ListenUnix(dir); err != ErrActive {
		t.Fatal("active socket must not be replaced")
	}
	ctx := context.Background()
	done := make(chan Response, 1)
	go func() {
		r, _ := Call(ctx, dir, Request{Op: "execute", Tasks: []string{"t1"}})
		done <- r
	}()
	<-fc.started
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := Call(ctx, dir, Request{Op: "stop", RunID: "r1"}); err != nil || !r.OK {
				t.Error("stop failed")
			}
			if r, err := Call(ctx, dir, Request{Op: "snapshot"}); err != nil || !r.OK {
				t.Error("snapshot failed")
			}
		}()
	}
	wg.Wait()
	close(fc.release)
	r := <-done
	var res core.Result
	if !r.OK || json.Unmarshal(r.Data, &res) != nil || res.Tasks[0].State != model.TaskDelivered {
		t.Fatalf("execute %+v", r)
	}
	if r, _ := Call(ctx, dir, Request{Op: "bogus"}); r.OK || r.Code != CodeInvalid {
		t.Fatal("unknown op")
	}
	ln.Close()
	svc.Shutdown()
	if !fc.closed {
		t.Fatal("core not closed")
	}
}

func TestExecuteSurvivesClientDisconnectAndShutdownCancels(t *testing.T) {
	dir := privDir(t)
	fc := &fakeCore{started: make(chan struct{}), release: make(chan struct{})}
	svc, _ := New(fc, nil, nil)
	ln, _ := ListenUnix(dir)
	go svc.ServeUnix(ln)
	ctx, cancel := context.WithCancel(context.Background())
	go Call(ctx, dir, Request{Op: "execute", Tasks: []string{"t1"}})
	<-fc.started
	cancel() // client disconnect: op keeps running under service ctx
	time.Sleep(50 * time.Millisecond)
	select {
	case <-svc.Done():
		t.Fatal("service canceled by client")
	default:
	}
	ln.Close()
	if err := svc.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRequestStrict(t *testing.T) {
	for _, s := range []string{`{"op":"x","extra":1}`, `{"op":"x"} {}`, `[]`, strings.Repeat(" ", MaxRequestBytes+2)} {
		if _, err := DecodeRequest(strings.NewReader(s)); err == nil {
			t.Fatalf("accepted %.20q", s)
		}
	}
	if _, err := DecodeRequest(io.LimitReader(strings.NewReader(`{"op":"snapshot","apply":true}`), 1<<10)); err != nil {
		t.Fatal(err)
	}
}

func TestSanitize(t *testing.T) {
	if c, m := sanitize(io.ErrUnexpectedEOF); c != CodeInternal || m != "internal error" {
		t.Fatal(m)
	}
	if c, _ := sanitize(core.ErrBusy); c != CodeBusy {
		t.Fatal(c)
	}
}

// delegCore adds the optional run surface to fakeCore and counts calls.
type delegCore struct {
	fakeCore
	dry, deleg int
	resumed    string
}

func (d *delegCore) DryPrepare(raw []byte) (core.DryRun, error) {
	d.mu.Lock()
	d.dry++
	d.mu.Unlock()
	return core.DryRun{Valid: true, Mode: "delegate"}, nil
}
func (d *delegCore) Delegate(ctx context.Context, raw []byte) (core.Result, error) {
	d.mu.Lock()
	d.deleg++
	d.mu.Unlock()
	return core.Result{Mode: "delegate", Tasks: []core.TaskResult{{ID: "t1", State: model.TaskFirstDelivery}}}, nil
}

func (d *delegCore) ResumeDelegate(_ context.Context, id string) (core.Result, error) {
	d.mu.Lock()
	d.resumed = id
	d.mu.Unlock()
	return core.Result{Mode: "delegate", Tasks: []core.TaskResult{{ID: id, State: model.TaskFirstDelivery}}}, nil
}

func TestDelegateResumeRequiresExplicitSocketInput(t *testing.T) {
	dc := &delegCore{}
	svc, _ := New(dc, nil, nil)
	t.Cleanup(svc.cancel)
	for _, req := range []Request{
		{Op: "resume-delegate", TaskID: "task"},
		{Op: "resume-delegate", Resume: true},
		{Op: "resume-delegate", TaskID: "task", Resume: true, Input: json.RawMessage(`{}`)},
		{Op: "resume-delegate", TaskID: "task", Resume: true, Acknowledge: true},
		{Op: "resume-delegate", TaskID: "task", Resume: true, Tasks: []string{"task"}},
	} {
		if svc.Do(req).OK || dc.resumed != "" {
			t.Fatal("ambiguous continuation accepted", req)
		}
	}
	if r := svc.Do(Request{Op: "resume-delegate", TaskID: "task", Resume: true}); !r.OK || dc.resumed != "task" {
		t.Fatal("continuation was not forwarded", r)
	}
}

func TestUnixDryPrepareAndDelegateSocketOnly(t *testing.T) {
	dir := privDir(t)
	dc := &delegCore{}
	svc, _ := New(dc, nil, nil)
	ln, err := ListenUnix(dir)
	if err != nil {
		t.Fatal(err)
	}
	go svc.ServeUnix(ln)
	defer func() { ln.Close(); svc.Shutdown() }()
	ctx := context.Background()
	if r, _ := Call(ctx, dir, Request{Op: "dry-prepare"}); r.OK || r.Code != CodeInvalid {
		t.Fatal("dry-prepare without input accepted")
	}
	r, _ := Call(ctx, dir, Request{Op: "dry-prepare", Input: json.RawMessage(`{}`)})
	var d core.DryRun
	if !r.OK || json.Unmarshal(r.Data, &d) != nil || !d.Valid {
		t.Fatalf("dry %+v", r)
	}
	r, _ = Call(ctx, dir, Request{Op: "delegate", Input: json.RawMessage(`{}`)})
	var res core.Result
	if !r.OK || json.Unmarshal(r.Data, &res) != nil || res.Mode != "delegate" {
		t.Fatalf("delegate %+v", r)
	}
	if dc.dry != 1 || dc.deleg != 1 {
		t.Fatal("calls", dc.dry, dc.deleg)
	}
	// A core without the surface rejects the ops.
	plain, _ := New(&fakeCore{}, nil, nil)
	if r := plain.Do(Request{Op: "delegate", Input: json.RawMessage(`{}`)}); r.OK {
		t.Fatal("plain core delegated")
	}
	// The browser API has no route to these ops.
	srv, host := startHTTP(t, svc)
	hdr := map[string]string{"Origin": "http://" + host, "X-Meerkat-Token": svc.Token(), "Content-Type": "application/json"}
	for _, p := range []string{"/api/workflow/delegate", "/api/workflow/dry-prepare", "/api/delegate", "/api/workflow/resume-delegate"} {
		if res, _ := do(t, "POST", srv.URL+p, `{}`, hdr); res.StatusCode < 400 {
			t.Fatalf("%s reachable over HTTP: %d", p, res.StatusCode)
		}
	}
	if dc.dry != 1 || dc.deleg != 1 {
		t.Fatal("HTTP reached the core")
	}
}
