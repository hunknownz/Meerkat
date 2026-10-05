// Package server is the single local Meerkat daemon: it owns the core lease,
// executor processes, a private Unix-socket command server and a loopback
// browser API limited to view/stop/settings.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/issues"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

// Version is the Meerkat release.
const Version = "0.4.0-beta.15"

// Core is the subset of *core.Core used by the service (injectable in tests).
type Core interface {
	Prepare(raw []byte) (model.Task, error)
	Execute(ctx context.Context, taskIDs []string, resume, acknowledge bool) (core.Result, error)
	Snapshot() (core.Snapshot, error)
	Stop(runID, requestID string) (model.StopReceipt, error)
	Settings(p model.SettingsPatch) (model.Settings, error)
	Close() error
}

// Service owns the operation context. Client disconnects never cancel or replay
// an operation; only Shutdown cancels.
type Service struct {
	core   Core
	store  *store.Store   // owner store (history summary, Issue updates); may be nil
	issues *issues.Client // Issue adapter for explicit --apply
	token  string
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// New wraps an owned core. st may be nil (tests); client nil means the real gh CLI.
func New(c Core, st *store.Store, client *issues.Client) (*Service, error) {
	if c == nil {
		return nil, errors.New("server: core required")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if client == nil {
		client = &issues.Client{Runner: issues.ExecRunner{}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{core: c, store: st, issues: client, token: hex.EncodeToString(b), ctx: ctx, cancel: cancel}, nil
}

// Registry returns the production executor registry.
func Registry() core.Registry {
	return core.Registry{"pi": executor.NewPi()}
}

// Token is the private browser session write token.
func (s *Service) Token() string { return s.token }

// Done is closed when Shutdown starts.
func (s *Service) Done() <-chan struct{} { return s.ctx.Done() }

// Shutdown cancels owned operations, closes the core (recording stops) and
// waits for in-flight handlers.
func (s *Service) Shutdown() error {
	var err error
	s.once.Do(func() {
		s.cancel()
		err = s.core.Close()
		s.wg.Wait()
	})
	return err
}

// HistorySummary is the optional local-history part of a socket snapshot, so
// imported legacy records are reported rather than silently dropped.
type HistorySummary struct {
	Entries int            `json:"entries"`
	ByKind  map[string]int `json:"byKind"`
	Imports int            `json:"imports"`
}

func (s *Service) history() (*HistorySummary, error) {
	if s.store == nil {
		return nil, nil
	}
	h, err := s.store.History()
	if err != nil {
		return nil, err
	}
	im, err := s.store.Imports()
	if err != nil {
		return nil, err
	}
	sum := &HistorySummary{Entries: len(h), ByKind: map[string]int{}, Imports: len(im)}
	for _, e := range h {
		sum.ByKind[e.Kind]++
	}
	return sum, nil
}

// Error codes returned to clients.
const (
	CodeInvalid  = "invalid"
	CodeBusy     = "busy"
	CodeNotFound = "not_found"
	CodeClosed   = "closed"
	CodeInternal = "internal"
)

// sanitize maps err to a fixed code and a secret-free message.
func sanitize(err error) (code, msg string) {
	var xe *executor.Error
	var ie *issues.Error
	switch {
	case errors.Is(err, model.ErrInvalid):
		code, msg = CodeInvalid, err.Error()
	case errors.Is(err, core.ErrBusy):
		return CodeBusy, "another dispatch is active"
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrUnknownRun):
		return CodeNotFound, "not found"
	case errors.Is(err, core.ErrClosed), errors.Is(err, context.Canceled):
		return CodeClosed, "service is shutting down"
	case errors.Is(err, store.ErrStopLimit):
		return CodeBusy, "too many pending stop requests"
	case errors.Is(err, store.ErrConflict):
		return CodeInvalid, "conflict"
	case errors.As(err, &xe):
		code, msg = CodeInternal, "executor: "+xe.Category
	case errors.As(err, &ie):
		code, msg = CodeInternal, "issue: "+ie.Category
	default:
		return CodeInternal, "internal error"
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if model.LooksLikeCredential(msg) {
		msg = "redacted error"
	}
	return code, msg
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}
