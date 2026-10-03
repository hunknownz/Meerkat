package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/issues"
	"github.com/hunknownz/Meerkat/internal/model"
)

// SocketName is the command socket file inside the data directory.
const SocketName = "meerkat.sock"

// Limits for the socket protocol.
const (
	MaxRequestBytes  = core.MaxInput + 64<<10
	MaxResponseBytes = 64 << 20
)

// Errors for socket setup.
var (
	ErrUnsafeSocket = errors.New("server: unsafe data directory or socket")
	ErrActive       = errors.New("server: daemon already active")
	ErrNoDaemon     = errors.New("server: daemon not running")
)

// Request is one strict command (one JSON object per connection).
type Request struct {
	Op          string          `json:"op"`
	Input       json.RawMessage `json:"input,omitempty"`
	Tasks       []string        `json:"tasks,omitempty"`
	Resume      bool            `json:"resume,omitempty"`
	Acknowledge bool            `json:"acknowledge,omitempty"`
	RunID       string          `json:"runId,omitempty"`
	RequestID   string          `json:"requestId,omitempty"`
	OperationID string          `json:"operationId,omitempty"`
	WaitMillis  int             `json:"waitMillis,omitempty"`
	Apply       bool            `json:"apply,omitempty"`
}

// Response is the sanitized command reply.
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Code  string          `json:"code,omitempty"`
	Error string          `json:"error,omitempty"`
}

// SocketPath returns the socket path for dataDir.
func SocketPath(dataDir string) string { return filepath.Join(dataDir, SocketName) }

// CheckPrivateDir requires a real 0700 directory owned by the current user.
func CheckPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() || fi.Mode().Perm() != 0o700 || !ownedByMe(fi) {
		return ErrUnsafeSocket
	}
	return nil
}

func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// checkSocket validates an existing socket file (not a symlink, ours, 0600).
func checkSocket(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 || !ownedByMe(fi) || fi.Mode().Perm()&0o077 != 0 {
		return ErrUnsafeSocket
	}
	return nil
}

// Alive reports whether a daemon answers on dataDir's socket.
func Alive(dataDir string) bool {
	p := SocketPath(dataDir)
	if checkSocket(p) != nil {
		return false
	}
	c, err := net.DialTimeout("unix", p, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// ListenUnix creates the private command socket. An existing socket is removed
// only when it is ours and provably dead (connection refused); a live one is
// never unlinked.
func ListenUnix(dataDir string) (*net.UnixListener, error) {
	if err := CheckPrivateDir(dataDir); err != nil {
		return nil, err
	}
	p := SocketPath(dataDir)
	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSocket == 0 || !ownedByMe(fi) {
			return nil, ErrUnsafeSocket
		}
		c, derr := net.DialTimeout("unix", p, time.Second)
		if derr == nil {
			c.Close()
			return nil, ErrActive
		}
		if !errors.Is(derr, syscall.ECONNREFUSED) {
			return nil, ErrActive // unknown state: never unlink blindly
		}
		if err := os.Remove(p); err != nil {
			return nil, ErrUnsafeSocket
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrUnsafeSocket
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		ln.Close()
		return nil, ErrUnsafeSocket
	}
	return ln, nil
}

// ServeUnix accepts commands until ln is closed.
func (s *Service) ServeUnix(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer c.Close()
			s.handleConn(c)
		}()
	}
}

func (s *Service) handleConn(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	req, err := DecodeRequest(io.LimitReader(c, MaxRequestBytes+1))
	_ = c.SetReadDeadline(time.Time{})
	var resp Response
	if err != nil {
		resp = Response{Code: CodeInvalid, Error: "invalid request"}
	} else {
		resp = s.Do(req)
	}
	_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_ = json.NewEncoder(c).Encode(resp) // a gone client just loses the reply; nothing is replayed
}

// DecodeRequest strictly decodes one bounded request.
func DecodeRequest(r io.Reader) (Request, error) {
	var req Request
	raw, err := io.ReadAll(io.LimitReader(r, MaxRequestBytes+1))
	if err != nil || len(raw) > MaxRequestBytes {
		return req, errors.New("request too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	if dec.More() {
		return req, errors.New("trailing data")
	}
	if len(req.Tasks) > 256 || len(req.RunID) > 64 || len(req.RequestID) > 64 || len(req.OperationID) > 64 || req.WaitMillis < 0 || req.WaitMillis > 30000 {
		return req, errors.New("bounds")
	}
	return req, nil
}

func ok(v any) Response {
	b, err := json.Marshal(v)
	if err != nil {
		return Response{Code: CodeInternal, Error: "internal error"}
	}
	return Response{OK: true, Data: b}
}

func fail(err error) Response {
	code, msg := sanitize(err)
	return Response{Code: code, Error: msg}
}

func bad(msg string) Response { return Response{Code: CodeInvalid, Error: msg} }

// delegator is the optional core surface for `run` (dry validation and one developer-only run).
type delegator interface {
	DryPrepare(raw []byte) (core.DryRun, error)
	Delegate(ctx context.Context, raw []byte) (core.Result, error)
}

type dispatcher interface {
	Dispatch(model.DispatchRequest) (model.DispatchReceipt, error)
	Operation(string) (model.Operation, error)
	OperationByRequest(string) (model.Operation, error)
	WaitOperation(context.Context, string, time.Duration) (model.Operation, error)
}

type budgetController interface {
	ProposeBudget(model.BudgetIncrease) (model.BudgetProposal, error)
	ApplyBudgetDecision(model.BudgetDecisionInput) (model.BudgetDecisionSummary, error)
	BudgetDecision(string) (model.BudgetDecisionSummary, error)
}

// Do executes one command under the service-owned context.
func (s *Service) Do(req Request) Response {
	select {
	case <-s.ctx.Done():
		return Response{Code: CodeClosed, Error: "service is shutting down"}
	default:
	}
	switch req.Op {
	case "propose-budget", "apply-budget-decision", "budget-decision":
		bc, can := s.core.(budgetController)
		if !can {
			return bad("unknown op")
		}
		if req.Op == "budget-decision" {
			d, e := bc.BudgetDecision(req.RequestID)
			if e != nil {
				return fail(e)
			}
			return ok(d)
		}
		if req.Op == "propose-budget" {
			var in model.BudgetIncrease
			if e := decodeBudgetInput(req.Input, &in); e != nil {
				return bad("invalid budget input")
			}
			p, e := bc.ProposeBudget(in)
			if e != nil {
				return fail(e)
			}
			return ok(p)
		}
		var in model.BudgetDecisionInput
		if e := decodeBudgetInput(req.Input, &in); e != nil {
			return bad("invalid budget input")
		}
		d, e := bc.ApplyBudgetDecision(in)
		if e != nil {
			return fail(e)
		}
		return ok(d)
	case "dispatch", "operation", "wait-operation":
		dc, can := s.core.(dispatcher)
		if !can {
			return bad("unknown op")
		}
		if req.Op == "dispatch" {
			r, err := dc.Dispatch(model.DispatchRequest{RequestID: req.RequestID, TaskIDs: req.Tasks, Resume: req.Resume, Acknowledge: req.Acknowledge})
			if err != nil {
				return fail(err)
			}
			return ok(r)
		}
		if req.WaitMillis < 0 || req.WaitMillis > 30000 {
			return bad("waitMillis must be 0..30000")
		}
		var o model.Operation
		var err error
		if req.OperationID != "" && req.RequestID != "" {
			return bad("use operationId or requestId, not both")
		}
		if req.Op == "operation" && req.RequestID != "" {
			o, err = dc.OperationByRequest(req.RequestID)
		} else if req.Op == "wait-operation" {
			o, err = dc.WaitOperation(s.ctx, req.OperationID, time.Duration(req.WaitMillis)*time.Millisecond)
		} else {
			o, err = dc.Operation(req.OperationID)
		}
		if err != nil {
			return fail(err)
		}
		return ok(o)
	case "health":
		return ok(map[string]string{"version": Version})
	case "snapshot":
		snap, err := s.core.Snapshot()
		if err != nil {
			return fail(err)
		}
		h, err := s.history()
		if err != nil {
			return fail(err)
		}
		return ok(struct {
			core.Snapshot
			LocalHistory *HistorySummary `json:"localHistory,omitempty"`
		}{snap, h})
	case "prepare":
		if len(req.Input) == 0 {
			return bad("input required")
		}
		t, err := s.core.Prepare(req.Input)
		if err != nil {
			return fail(err)
		}
		return ok(t)
	case "dry-prepare", "delegate": // socket-only: the HTTP API never routes to Do
		dc, can := s.core.(delegator)
		if !can {
			return bad("unknown op")
		}
		if len(req.Input) == 0 || len(req.Input) > core.MaxInput {
			return bad("input required")
		}
		if req.Op == "dry-prepare" {
			d, err := dc.DryPrepare(req.Input)
			if err != nil {
				return fail(err)
			}
			return ok(d)
		}
		r, err := dc.Delegate(s.ctx, req.Input)
		if err != nil {
			return fail(err)
		}
		return ok(r)
	case "execute":
		if len(req.Tasks) == 0 {
			return bad("tasks required")
		}
		r, err := s.core.Execute(s.ctx, req.Tasks, req.Resume, req.Acknowledge)
		if err != nil {
			return fail(err)
		}
		return ok(r)
	case "stop":
		if req.RunID == "" {
			return bad("runId required")
		}
		if req.RequestID == "" {
			req.RequestID = newUUID()
		}
		r, err := s.core.Stop(req.RunID, req.RequestID)
		if err != nil {
			return fail(err)
		}
		return ok(r)
	case "settings":
		p, err := core.ParseSettingsPatch(req.Input)
		if err != nil {
			return fail(err)
		}
		set, err := s.core.Settings(p)
		if err != nil {
			return fail(err)
		}
		return ok(set)
	case "issue-update":
		if len(req.Tasks) != 1 || s.store == nil {
			return bad("exactly one task required")
		}
		u := &issues.Updater{Store: s.store, Client: s.issues}
		r, err := u.PrepareUpdate(req.Tasks[0])
		if err == nil && req.Apply { // remote sending only on explicit apply
			r, err = u.ApplyUpdate(s.ctx, req.Tasks[0])
		}
		if err != nil {
			return fail(err)
		}
		return ok(r)
	}
	return bad("unknown op")
}

func decodeBudgetInput(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 16384 {
		return errors.New("invalid input")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if e := d.Decode(new(any)); !errors.Is(e, io.EOF) {
		return errors.New("trailing input")
	}
	return nil
}

// Call sends one request to the daemon for dataDir and waits for the reply.
func Call(ctx context.Context, dataDir string, req Request) (Response, error) {
	var resp Response
	if err := CheckPrivateDir(dataDir); err != nil {
		return resp, ErrNoDaemon
	}
	p := SocketPath(dataDir)
	if err := checkSocket(p); err != nil {
		if errors.Is(err, ErrUnsafeSocket) {
			return resp, err
		}
		return resp, ErrNoDaemon
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", p)
	if err != nil {
		return resp, ErrNoDaemon
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return resp, fmt.Errorf("server: send failed")
	}
	_ = c.(*net.UnixConn).CloseWrite()
	dec := json.NewDecoder(io.LimitReader(c, MaxResponseBytes))
	if err := dec.Decode(&resp); err != nil {
		return resp, fmt.Errorf("server: no reply")
	}
	return resp, nil
}
