package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/server"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e syncBuf
	code := Run(Env{Stdin: strings.NewReader(""), Stdout: &o, Stderr: &e, Ctx: context.Background()}, args)
	return code, o.String(), e.String()
}

func privDir(t *testing.T) string {
	d, err := os.MkdirTemp("", "mkc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestHelpVersionUsage(t *testing.T) {
	if c, o, _ := run(t, "version"); c != 0 || strings.TrimSpace(o) != "0.3.0" {
		t.Fatal(o)
	}
	if c, o, _ := run(t, "help"); c != 0 || !strings.Contains(o, "serve") {
		t.Fatal("help")
	}
	for _, a := range [][]string{{}, {"nope"}, {"issue"}, {"execute"}, {"stop"}, {"prepare"}, {"export", "--format", "xml"}, {"snapshot", "extra"}, {"serve", "--port", "-1"}, {"issue", "read", "--url", "x"}} {
		if c, _, e := run(t, a...); c != ExitUsage {
			t.Fatalf("%v -> %d %s", a, c, e)
		}
	}
}

func TestNoDaemonIsPreflight(t *testing.T) {
	d := privDir(t)
	if c, _, e := run(t, "snapshot", "--data-dir", d); c != ExitUsage || !strings.Contains(e, "daemon unavailable") {
		t.Fatalf("%d %s", c, e)
	}
}

func TestOfflineExportBackupRestoreDoctor(t *testing.T) {
	d := privDir(t)
	c, o, e := run(t, "export", "--data-dir", d, "--format", "json")
	if c != 0 || !json.Valid([]byte(o)) {
		t.Fatalf("export json %d %s", c, e)
	}
	if c, _, _ := run(t, "export", "--data-dir", d, "--format", "csv"); c != 0 {
		t.Fatal("csv")
	}
	bk := filepath.Join(privDir(t), "b.db")
	if c, _, e := run(t, "backup", "--data-dir", d, "--output", bk); c != 0 {
		t.Fatal(e)
	}
	if c, _, _ := run(t, "backup", "--data-dir", d, "--output", bk); c == 0 {
		t.Fatal("backup overwrote")
	}
	if c, _, _ := run(t, "restore", "--backup", bk, "--to", d); c != ExitUsage {
		t.Fatal("restore must refuse existing dir")
	}
	fresh := filepath.Join(privDir(t), "fresh")
	if c, o, e := run(t, "restore", "--backup", bk, "--to", fresh); c != 0 || !strings.Contains(o, `"merged": false`) {
		t.Fatal(e)
	}
	if c, o, _ := run(t, "doctor", "--data-dir", d); c != 0 || !strings.Contains(o, `"daemonActive": false`) {
		t.Fatal(o)
	}
	if c, _, _ := run(t, "migrate", "--data-dir", d, "--from", filepath.Join(d, "missing")); c == 0 {
		t.Fatal("migrate from missing succeeded")
	}
}

func TestServeSocketCommandsAndOwnerExclusion(t *testing.T) {
	d := privDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	var o, e syncBuf
	done := make(chan int, 1)
	go func() { done <- Run(Env{Stdout: &o, Stderr: &e, Ctx: ctx}, []string{"serve", "--data-dir", d}) }()
	deadline := time.Now().Add(5 * time.Second)
	for !server.Alive(d) {
		if time.Now().After(deadline) {
			t.Fatalf("serve not up: %s", e.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var start map[string]any
	if json.Unmarshal([]byte(o.String()), &start) != nil || start["version"] != "0.3.0" || !strings.HasPrefix(start["url"].(string), "http://127.0.0.1:") {
		t.Fatalf("startup %s", o.String())
	}
	if c, out, e := run(t, "snapshot", "--data-dir", d); c != 0 || !strings.Contains(out, `"schemaVersion": 1`) || !strings.Contains(out, "localHistory") {
		t.Fatalf("snapshot %d %s", c, e)
	}
	if c, out, _ := run(t, "settings", "--data-dir", d, "--max-concurrency", "3"); c != 0 || !strings.Contains(out, `"maxConcurrency": 3`) {
		t.Fatal(out)
	}
	if c, _, _ := run(t, "settings", "--data-dir", d, "--max-concurrency", "9"); c != ExitUsage {
		t.Fatal("invalid settings")
	}
	if c, _, _ := run(t, "stop", "--data-dir", d, "--run", "not-a-uuid"); c != ExitUsage {
		t.Fatal("bad stop")
	}
	for _, a := range [][]string{{"serve"}, {"export"}, {"backup", "--output", filepath.Join(d, "x.db")}, {"migrate", "--from", d}} {
		if c, _, _ := run(t, append(a, "--data-dir", d)...); c != ExitUsage {
			t.Fatalf("%v allowed while daemon active", a)
		}
	}
	cancel()
	select {
	case c := <-done:
		if c != 0 {
			t.Fatalf("serve exit %d %s", c, e.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	if _, err := os.Lstat(server.SocketPath(d)); !os.IsNotExist(err) {
		t.Fatal("socket left behind")
	}
}
