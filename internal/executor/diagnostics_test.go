package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

func diagnosticStatus(cs []DiagnosticCheck, id string) string {
	for _, c := range cs {
		if c.ID == id {
			return c.Status
		}
	}
	return "missing"
}

func TestPiDiagnosticsStaticConfigNoExecution(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	marker := filepath.Join(dir, "executed")
	bin := filepath.Join(dir, "pi")
	os.WriteFile(bin, []byte("#!/bin/sh\ntouch '"+marker+"'\necho 0.99.1\n"), 0o700)
	config := func(api, key string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"providers": map[string]any{"example": map[string]any{"api": api, "apiKey": key, "baseUrl": "https://example.invalid/v1", "models": []map[string]any{{"id": "text", "input": []string{"text"}}}}}})
		os.WriteFile(filepath.Join(dir, "models.json"), b, 0o600)
	}
	p := model.Profile{Provider: "example", Model: "text", AuthEnv: "MY_PROVIDER_KEY", PiCommand: []string{"env", "PI_CODING_AGENT_DIR=" + dir, bin, "--thinking", "low"}}
	config("openai-completions", "${MY_PROVIDER_KEY}")
	cs := NewPi().Diagnose(context.Background(), p, false)
	if diagnosticStatus(cs, "executor.model") != "ok" || diagnosticStatus(cs, "executor.version") != "not_checked" || diagnosticStatus(cs, "executor.gate") != "not_checked" {
		t.Fatalf("%+v", cs)
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("default diagnosis launched executor")
	}
	for _, tc := range []struct{ api, key string }{{"anthropic-messages", "${MY_PROVIDER_KEY}"}, {"openai-completions", "SECRET_SENTINEL"}} {
		config(tc.api, tc.key)
		cs = NewPi().Diagnose(context.Background(), p, false)
		b, _ := json.Marshal(cs)
		if diagnosticStatus(cs, "executor.model") != "blocked" || strings.Contains(string(b), "SECRET_SENTINEL") {
			t.Fatalf("%s", b)
		}
	}
}

func TestPiVersionProbeIsolationAndBoundaries(t *testing.T) {
	t.Setenv("MEERKAT_TEST_SECRET", "SECRET_SENTINEL")
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	script := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = '--version' ] || exit 1\n[ -z \"${MEERKAT_TEST_SECRET:-}\" ] || exit 1\n[ \"$HOME\" = \"$PWD\" ] && [ \"$PI_CODING_AGENT_DIR\" = \"$PWD\" ] || exit 1\necho SECRET_SENTINEL >&2\necho 0.99.1\n"
	os.WriteFile(bin, []byte(script), 0o700)
	p := model.Profile{PiCommand: []string{bin, "--model", "do-not-pass", "--prompt", "do-not-pass"}}
	cs := NewPi().Diagnose(context.Background(), p, true)
	if diagnosticStatus(cs, "executor.version") != "ok" {
		t.Fatalf("%+v", cs)
	}
	b, _ := json.Marshal(cs)
	if strings.Contains(string(b), "SECRET_SENTINEL") || strings.Contains(string(b), "do-not-pass") {
		t.Fatal("private output leaked")
	}
	os.WriteFile(bin, []byte("#!/bin/sh\necho 0.99.2\n"), 0o700)
	if diagnosticStatus(NewPi().Diagnose(context.Background(), p, true), "executor.version") != "blocked" {
		t.Fatal("incompatible Pi passed")
	}
	os.WriteFile(bin, []byte("#!/bin/sh\necho SECRET_SENTINEL\n"), 0o700)
	if diagnosticStatus(NewPi().Diagnose(context.Background(), p, true), "executor.version") != "blocked" {
		t.Fatal("invalid version passed")
	}
	os.WriteFile(bin, []byte("#!/bin/sh\nsleep 20\n"), 0o700)
	start := time.Now()
	if diagnosticStatus(NewPi().Diagnose(context.Background(), p, true), "executor.version") != "blocked" || time.Since(start) > 5*time.Second {
		t.Fatal("probe was not bounded")
	}
	p.PiCommand = []string{"node", "script.js"}
	if diagnosticStatus(NewPi().Diagnose(context.Background(), p, true), "executor.command") != "blocked" {
		t.Fatal("interpreter version used as Pi version")
	}
}

func TestInstalledPiDiagnosticVersion(t *testing.T) {
	binary := os.Getenv("MEERKAT_TEST_PI_RPC_BINARY")
	if binary == "" {
		t.Skip("set MEERKAT_TEST_PI_RPC_BINARY for the isolated installed CLI check")
	}
	version, ok := probePiVersion(context.Background(), binary)
	if !ok || version != "0.99.1" {
		t.Fatal("installed Pi version probe failed")
	}
}
