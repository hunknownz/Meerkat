// Package pibudget is the thin private Pi request bridge. Budget policy stays in Go authority.
package pibudget

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

//go:embed bridge.mjs
var bridge []byte

//go:embed extension.ts
var extension []byte

const EnvName = "MEERKAT_PRIVATE_BUDGET"
const Version = "pi-http-v1"

type Config struct {
	Socket     string `json:"socket"`
	Token      string `json:"token"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Version    string `json:"version"`
	ReportRole string `json:"reportRole,omitempty"`
}

type Server struct {
	dir, extension, env string
	http                *http.Server
	ready               chan bool
	halt                chan struct{}
	mu                  sync.Mutex
	seenReady           bool
	readyOK             bool
	denied              bool
	unknown             bool
}

type ReportWriter func(context.Context, json.RawMessage) (any, error)
type ReportOption struct {
	Role  string
	Write ReportWriter
}

func Start(provider, model string, a budget.Authority, report ...ReportOption) (*Server, error) {
	if len(report) > 1 || len(report) == 1 && (report[0].Write == nil || report[0].Role != "developer" && report[0].Role != "reviewer" && report[0].Role != "polisher") {
		return nil, budget.ErrConflict
	}
	// macOS Unix socket paths have a small limit; use a short, owned 0700 directory.
	dir, err := os.MkdirTemp("/tmp", "mkb-")
	if err != nil {
		return nil, budget.ErrUnknown
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, "extension.ts")
	if err := os.WriteFile(path, extension, 0o600); err != nil {
		return nil, budget.ErrUnknown
	}
	if err := os.WriteFile(filepath.Join(dir, "bridge.mjs"), bridge, 0o600); err != nil {
		return nil, budget.ErrUnknown
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, budget.ErrUnknown
	}
	cfg := Config{Socket: filepath.Join(dir, "gate.sock"), Token: hex.EncodeToString(raw[:]), Provider: provider, Model: model, Version: Version}
	var writer ReportWriter
	if len(report) == 1 {
		cfg.ReportRole, writer = report[0].Role, report[0].Write
	}
	ln, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return nil, budget.ErrUnknown
	}
	if err := os.Chmod(cfg.Socket, 0o600); err != nil {
		ln.Close()
		return nil, budget.ErrUnknown
	}
	b, _ := json.Marshal(cfg)
	s := &Server{dir: dir, extension: path, env: EnvName + "=" + string(b), ready: make(chan bool, 1), halt: make(chan struct{}, 1)}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+cfg.Token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		bodyLimit := int64(8192)
		if r.URL.Path == "/report" {
			bodyLimit = 65536
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		decode := func(v any) error {
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if d.Decode(v) != nil {
				return budget.ErrConflict
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				return budget.ErrConflict
			}
			return nil
		}
		var value any = struct {
			Accepted bool `json:"accepted"`
		}{true}
		var err error
		unknownSettlement := false
		s.mu.Lock()
		readyOK := s.readyOK
		s.mu.Unlock()
		if r.URL.Path != "/ready" && !readyOK {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "denied"})
			return
		}
		switch r.URL.Path {
		case "/report":
			var draft json.RawMessage
			err = decode(&draft)
			if err == nil && writer != nil {
				value, err = writer(r.Context(), draft)
			} else {
				err = budget.ErrConflict
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "report_invalid"})
				return
			}
		case "/ready":
			var v struct {
				Version   string `json:"version"`
				PiVersion string `json:"piVersion"`
				Provider  string `json:"provider"`
				Model     string `json:"model"`
				API       string `json:"api"`
				Installed bool   `json:"installed"`
			}
			err = decode(&v)
			ok := err == nil && v.Version == Version && v.PiVersion == "0.99.1" && v.Provider == provider && v.Model == model && v.API == "openai-completions" && v.Installed
			s.mu.Lock()
			if !s.seenReady {
				s.seenReady = true
				s.readyOK = ok
				s.ready <- ok
			}
			s.mu.Unlock()
			if !ok {
				err = budget.ErrDenied
			}
		case "/reserve":
			var v budget.Request
			err = decode(&v)
			if err == nil {
				value, err = a.Reserve(r.Context(), v)
			}
		case "/begin":
			var v budget.Begin
			err = decode(&v)
			if err == nil {
				err = a.Begin(r.Context(), v)
			}
		case "/rate-limit":
			var v budget.RateLimitReport
			err = decode(&v)
			if err == nil {
				if authority, ok := a.(budget.RetryAuthority); ok {
					value, err = authority.RateLimit(r.Context(), v)
				} else {
					err = budget.ErrDenied
				}
			}
		case "/settle":
			var v budget.Settlement
			err = decode(&v)
			if err == nil {
				err = a.Settle(r.Context(), v)
				unknownSettlement = err == nil && v.State == budget.Unknown
			}
		default:
			err = budget.ErrDenied
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			s.mu.Lock()
			if r.URL.Path == "/reserve" && errors.Is(err, budget.ErrDenied) {
				s.denied = true
			} else if r.URL.Path != "/ready" {
				s.unknown = true
			}
			if s.denied || s.unknown {
				select {
				case s.halt <- struct{}{}:
				default:
				}
			}
			s.mu.Unlock()
			code := "unknown"
			status := http.StatusConflict
			if errors.Is(err, budget.ErrDenied) {
				code = "denied"
				status = http.StatusForbidden
			}
			if errors.Is(err, budget.ErrConflict) {
				code = "conflict"
			}
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"error": code})
			return
		}
		if unknownSettlement {
			s.mu.Lock()
			s.unknown = true
			select {
			case s.halt <- struct{}{}:
			default:
			}
			s.mu.Unlock()
		}
		json.NewEncoder(w).Encode(value)
	})
	s.http = &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second}
	go s.http.Serve(ln)
	cleanup = false
	return s, nil
}

func (s *Server) Extension() string     { return s.extension }
func (s *Server) Env() string           { return s.env }
func (s *Server) Halt() <-chan struct{} { return s.halt }
func (s *Server) Outcome() (bool, bool) { s.mu.Lock(); defer s.mu.Unlock(); return s.denied, s.unknown }
func (s *Server) WaitReady(ctx context.Context) bool {
	select {
	case ok := <-s.ready:
		return ok
	case <-ctx.Done():
		return false
	}
}
func (s *Server) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.http.Shutdown(ctx)
	s.http.Close()
	os.RemoveAll(s.dir)
}
