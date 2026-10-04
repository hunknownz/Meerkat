package pibudget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

type authorityFixture struct{}

func (authorityFixture) Reserve(context.Context, budget.Request) (budget.Grant, error) {
	return budget.Grant{}, budget.ErrDenied
}
func (authorityFixture) Begin(context.Context, budget.Begin) error { return budget.ErrConflict }
func (authorityFixture) Settle(context.Context, budget.Settlement) error {
	return errors.New("private backend error must not escape")
}

func TestPrivateBudgetSocketAuthenticationReadinessAndSafeErrors(t *testing.T) {
	s, err := Start("fixture", "text-model", authorityFixture{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var cfg Config
	if json.Unmarshal([]byte(strings.TrimPrefix(s.Env(), EnvName+"=")), &cfg) != nil {
		t.Fatal("invalid private config")
	}
	for _, path := range []string{s.dir, cfg.Socket, s.Extension()} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm()&0o077 != 0 {
			t.Fatal("private bridge exposed", err)
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	post := func(path, token string, value any) (int, string) {
		t.Helper()
		b, _ := json.Marshal(value)
		r, _ := http.NewRequest("POST", "http://private"+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	if status, _ := post("/reserve", "foreign", struct{}{}); status != http.StatusForbidden {
		t.Fatal("foreign accepted")
	}
	if status, _ := post("/reserve", cfg.Token, struct{}{}); status != http.StatusForbidden {
		t.Fatal("request before ready accepted")
	}
	ready := map[string]any{"version": Version, "piVersion": "0.99.1", "provider": "fixture", "model": "text-model", "api": "openai-completions", "installed": true}
	if status, _ := post("/ready", cfg.Token, ready); status != http.StatusOK {
		t.Fatal("ready rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !s.WaitReady(ctx) {
		t.Fatal("missing handshake")
	}
	if status, body := post("/reserve", cfg.Token, budget.Request{}); status != http.StatusForbidden || !strings.Contains(body, "denied") {
		t.Fatal(status, body)
	}
	if denied, unknown := s.Outcome(); !denied || unknown {
		t.Fatal(denied, unknown)
	}
	if status, body := post("/settle", cfg.Token, budget.Settlement{}); status != http.StatusConflict || strings.Contains(body, "private") {
		t.Fatal("raw backend error leaked", status, body)
	}
	if _, unknown := s.Outcome(); !unknown {
		t.Fatal("lost settlement not recorded")
	}
}

func TestPrivateReportRequiresHandshakeAndDoesNotPoisonBudgetOnInvalidDraft(t *testing.T) {
	called := 0
	s, err := Start("fixture", "text-model", authorityFixture{}, ReportOption{Role: "developer", Write: func(_ context.Context, draft json.RawMessage) (any, error) {
		called++
		if string(draft) != `{"summary":"observed"}` {
			return nil, errors.New("private report error must not escape")
		}
		return map[string]string{"candidateSha": strings.Repeat("a", 40)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var cfg Config
	json.Unmarshal([]byte(strings.TrimPrefix(s.Env(), EnvName+"=")), &cfg)
	if cfg.ReportRole != "developer" {
		t.Fatal("report role missing from private config")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	post := func(path, token, body string) (int, string) {
		t.Helper()
		r, _ := http.NewRequest("POST", "http://private"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	for _, token := range []string{"foreign", cfg.Token} {
		if status, _ := post("/report", token, `{"summary":"observed"}`); status != http.StatusForbidden {
			t.Fatal("report accepted before authenticated handshake")
		}
	}
	if called != 0 {
		t.Fatal("unauthorized writer invoked")
	}
	if status, _ := post("/ready", cfg.Token, `{"version":"pi-http-v1","piVersion":"0.99.1","provider":"fixture","model":"text-model","api":"openai-completions","installed":true}`); status != http.StatusOK {
		t.Fatal("handshake rejected")
	}
	for _, body := range []string{`{}`, `{} {}`, strings.Repeat("x", 65537)} {
		if status, out := post("/report", cfg.Token, body); status != http.StatusBadRequest || strings.Contains(out, "private") {
			t.Fatal(status, out)
		}
		if denied, unknown := s.Outcome(); denied || unknown {
			t.Fatal("invalid report poisoned model settlement")
		}
		select {
		case <-s.Halt():
			t.Fatal("invalid report halted budget")
		default:
		}
	}
	if status, out := post("/report", cfg.Token, `{"summary":"observed"}`); status != http.StatusOK || !strings.Contains(out, "candidateSha") {
		t.Fatal(status, out)
	}
}
