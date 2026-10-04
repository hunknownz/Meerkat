package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/server"
	"github.com/hunknownz/Meerkat/internal/store"
)

func TestDoctorMissingStateAndProbeFlags(t *testing.T) {
	d := filepath.Join(privDir(t), "missing")
	if c, o, e := run(t, "doctor", "--data-dir", d); c != 0 || !strings.Contains(o, `"dataDirExists": false`) || e != "" {
		t.Fatalf("%d %s %s", c, o, e)
	}
	if _, e := os.Stat(d); !os.IsNotExist(e) {
		t.Fatal("doctor created state")
	}
	if c, _, _ := run(t, "doctor", "--data-dir", d, "--probe-executor"); c != ExitUsage {
		t.Fatal("probe without explicit profile")
	}
}

func TestDoctorOldDatabaseIsReadOnly(t *testing.T) {
	d := privDir(t)
	s, e := store.Open(d)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(d, "meerkat.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("PRAGMA user_version=1"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	before, _ := os.ReadFile(path)
	c, o, eout := run(t, "doctor", "--data-dir", d)
	if c != 0 || eout != "" || !strings.Contains(o, `"sessions": null`) || !strings.Contains(o, `"executionReadiness": "not_verified"`) {
		t.Fatalf("%d %s %s", c, o, eout)
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("doctor migrated/wrote old state")
	}
	if c, _, _ := run(t, "doctor", "--data-dir", d, "--profile", filepath.Join(d, "absent.json")); c != ExitUsage {
		t.Fatal("missing profile passed")
	}
}

func TestDoctorDoesNotTrustSocketPresenceOrLeakHealthErrors(t *testing.T) {
	for _, kind := range []string{"version_mismatch", "invalid", "stalled"} {
		t.Run(kind, func(t *testing.T) {
			d := privDir(t)
			s, e := store.Open(d)
			if e != nil {
				t.Fatal(e)
			}
			s.Close()
			ln, e := server.ListenUnix(d)
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, e := ln.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				var req server.Request
				json.NewDecoder(c).Decode(&req)
				switch kind {
				case "version_mismatch":
					json.NewEncoder(c).Encode(map[string]any{"ok": true, "data": map[string]string{"version": "0.0.1"}})
				case "invalid":
					c.Write([]byte("SECRET_SENTINEL\n"))
				case "stalled":
					c.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
					var b [1]byte
					c.Read(b[:])
					time.Sleep(2200 * time.Millisecond)
				}
			}()
			start := time.Now()
			c, o, eout := run(t, "doctor", "--data-dir", d)
			if c != ExitUsage || eout != "" || strings.Contains(o, "SECRET_SENTINEL") || time.Since(start) > 4*time.Second {
				t.Fatalf("%d %s %s", c, o, eout)
			}
			<-done
		})
	}
}

func TestDoctorUnsafeFilesNotRepaired(t *testing.T) {
	d := privDir(t)
	s, e := store.Open(d)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	path := filepath.Join(d, "meerkat.db")
	os.Chmod(path, 0o644)
	if c, o, _ := run(t, "doctor", "--data-dir", d); c != ExitUsage || !strings.Contains(o, `"storeOpen": false`) {
		t.Fatalf("%d %s", c, o)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 {
		t.Fatal("doctor changed permissions")
	}
}

func TestDoctorLifecycleUnknownAndUnavailableRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		d     store.Diagnostics
		alive bool
		want  string
	}{
		{store.Diagnostics{Runs: map[string]int{"unknown": 1}}, true, "blocked"},
		{store.Diagnostics{Requests: map[string]int{"unknown": 1}}, true, "blocked"},
		{store.Diagnostics{Controls: map[string]int{"unknown": 1}}, true, "blocked"},
		{store.Diagnostics{Sessions: map[string]int{"running": 1}}, false, "blocked"},
		{store.Diagnostics{Requests: map[string]int{"sent": 1}}, true, "warning"},
		{store.Diagnostics{}, false, "ok"},
	} {
		checks := []executor.DiagnosticCheck{}
		doctorLifecycle(tc.d, tc.alive, &checks)
		if checks[0].Status != tc.want {
			t.Fatalf("%+v", checks)
		}
		if tc.d.Sessions == nil && checks[1].ID != "execution.evidence" {
			t.Fatal("missing evidence was concealed")
		}
	}
}
