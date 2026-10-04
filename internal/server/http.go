package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/core"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/web"
)

// HTTP limits.
const (
	MaxBodyBytes  = 8 << 10
	MaxSSEClients = 16
	ssePoll       = 300 * time.Millisecond
	sseHeartbeat  = 15 * time.Second
)

var apiPaths = map[string]bool{"/api/health": true, "/api/workflow": true, "/api/workflow/events": true,
	"/api/workflow/stop": true, "/api/workflow/settings": true}

// HTTPHandler serves the loopback browser API for the bound port. Only hosts
// 127.0.0.1:port and localhost:port are accepted (DNS-rebinding guard).
// Browsers can view, send bounded instructions, stop and change future-run settings.
func (s *Service) HTTPHandler(port int) http.Handler {
	p := strconv.Itoa(port)
	hosts := map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true}
	sse := make(chan struct{}, MaxSSEClients)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version})
	})
	mux.HandleFunc("GET /api/workflow", s.getWorkflow)
	mux.HandleFunc("GET /api/workflow/events", func(w http.ResponseWriter, r *http.Request) {
		select {
		case sse <- struct{}{}:
			defer func() { <-sse }()
			s.events(w, r)
		default:
			writeErr(w, http.StatusServiceUnavailable, "too many event streams")
		}
	})
	mux.HandleFunc("POST /api/workflow/stop", s.write(func(w http.ResponseWriter, r *http.Request, body []byte) {
		var in struct {
			RunID     string `json:"runId"`
			RequestID string `json:"requestId"`
		}
		if strictJSON(body, &in) != nil {
			writeErr(w, http.StatusBadRequest, "invalid stop request")
			return
		}
		s.stop(w, in.RunID, in.RequestID)
	}))
	mux.HandleFunc("POST /api/workflow/runs/{runId}/stop", s.write(func(w http.ResponseWriter, r *http.Request, body []byte) {
		var in struct {
			RequestID string `json:"requestId"`
		}
		if strictJSON(body, &in) != nil {
			writeErr(w, http.StatusBadRequest, "invalid stop request")
			return
		}
		s.stop(w, r.PathValue("runId"), in.RequestID)
	}))
	mux.HandleFunc("POST /api/workflow/runs/{runId}/instruction", s.write(func(w http.ResponseWriter, r *http.Request, body []byte) {
		var in struct {
			SessionID string `json:"sessionId"`
			RequestID string `json:"requestId"`
			Message   string `json:"message"`
		}
		if decodeInstructionInput(body, &in, "sessionId", "requestId", "message") != nil {
			writeErr(w, 400, "invalid instruction")
			return
		}
		cc, can := s.core.(instructionController)
		if !can {
			writeErr(w, 409, "instructions unavailable")
			return
		}
		v, e := cc.RequestInstruction(model.WrapUpInput{Kind: "instruction", RunID: r.PathValue("runId"), SessionID: in.SessionID, RequestID: in.RequestID, Message: in.Message, AuthorizationRef: "Direct user instruction in Meerkat UI", Apply: true})
		if e != nil {
			s.apiErr(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "data": v})
	}))
	mux.HandleFunc("GET /api/workflow/controls/{requestId}", func(w http.ResponseWriter, r *http.Request) {
		cc, can := s.core.(runController)
		if !can {
			writeErr(w, 409, "controls unavailable")
			return
		}
		v, e := cc.ControlReceipt(r.PathValue("requestId"))
		if e != nil {
			s.apiErr(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "data": v})
	})
	mux.HandleFunc("PUT /api/workflow/settings", s.write(func(w http.ResponseWriter, r *http.Request, body []byte) {
		patch, err := core.ParseSettingsPatch(body)
		if err != nil {
			s.apiErr(w, err)
			return
		}
		set, err := s.core.Settings(patch)
		if err != nil {
			s.apiErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": set})
	}))
	assets := web.Assets()
	app, _ := fs.Sub(assets, "app")
	mount, _ := fs.Sub(assets, "mount")
	mux.Handle("GET /mount/", http.StripPrefix("/mount/", http.FileServerFS(mount)))
	files := http.FileServerFS(app)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case apiPaths[r.URL.Path] || strings.HasSuffix(r.URL.Path, "/stop"):
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		case strings.HasPrefix(r.URL.Path, "/api/"):
			writeErr(w, http.StatusNotFound, "not found")
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		default:
			files.ServeHTTP(w, r)
		}
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if !hosts[r.Host] {
			writeErr(w, http.StatusMisdirectedRequest, "host not allowed")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			writeErr(w, http.StatusForbidden, "origin not allowed")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) envelope() ([]byte, error) {
	snap, err := s.core.Snapshot()
	if err != nil {
		return nil, err
	}
	// legacyActive stays empty: no safe live legacy source is polled, never fake agents.
	return json.Marshal(map[string]any{"ok": true, "data": snap, "legacyActive": []any{}, "sessionToken": s.token})
}

func (s *Service) getWorkflow(w http.ResponseWriter, r *http.Request) {
	b, err := s.envelope()
	if err != nil {
		s.apiErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// stateHash hashes the snapshot excluding observedAt and lease heartbeat.
func (s *Service) stateHash() ([32]byte, error) {
	snap, err := s.core.Snapshot()
	if err != nil {
		return [32]byte{}, err
	}
	snap.ObservedAt = ""
	snap.Controller.HeartbeatAt = nil
	h := sha256.New()
	if err := json.NewEncoder(h).Encode(snap); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// events streams lightweight invalidations: "state" (committed state changed)
// and "snapshot" (what the frontend transport listens for); clients then GET
// the full snapshot. Comment heartbeats keep the stream alive.
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	fl, okf := w.(http.Flusher)
	if !okf {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	var last [32]byte
	send := func(rev int) bool {
		msg := "event: state\ndata: {\"rev\":" + strconv.Itoa(rev) + "}\n\nevent: snapshot\ndata: {\"rev\":" + strconv.Itoa(rev) + "}\n\n"
		if _, err := io.WriteString(w, msg); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	rev := 0
	if hs, err := s.stateHash(); err == nil {
		last = hs
	}
	if !send(rev) {
		return
	}
	poll := time.NewTicker(ssePoll)
	beat := time.NewTicker(sseHeartbeat)
	defer poll.Stop()
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-beat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-poll.C:
			hs, err := s.stateHash()
			if err != nil || hs == last {
				continue
			}
			last = hs
			rev++
			if !send(rev) {
				return
			}
		}
	}
}

// write guards browser writes: same-origin, session token and bounded JSON.
func (s *Service) write(next func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			writeErr(w, http.StatusForbidden, "same-origin request required")
			return
		}
		if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
			writeErr(w, http.StatusForbidden, "same-origin request required")
			return
		}
		tok := r.Header.Get("X-Meerkat-Token")
		if len(tok) != len(s.token) || subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			writeErr(w, http.StatusForbidden, "invalid session token")
			return
		}
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			writeErr(w, http.StatusUnsupportedMediaType, "application/json required")
			return
		}
		limit := int64(MaxBodyBytes)
		if strings.HasSuffix(r.URL.Path, "/instruction") {
			limit = 32 * 1024 // Up to 4000 codepoints plus JSON escaping and IDs.
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		next(w, r, body)
	}
}

func (s *Service) stop(w http.ResponseWriter, runID, requestID string) {
	if runID == "" || requestID == "" {
		writeErr(w, http.StatusBadRequest, "runId and requestId required")
		return
	}
	rc, err := s.core.Stop(runID, requestID)
	if err != nil {
		s.apiErr(w, err)
		return
	}
	// 202: accepted means recorded; the actual outcome arrives in later snapshots.
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "data": rc})
}

func strictJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

func (s *Service) apiErr(w http.ResponseWriter, err error) {
	code, msg := sanitize(err)
	st := http.StatusInternalServerError
	switch code {
	case CodeInvalid:
		st = http.StatusBadRequest
	case CodeBusy:
		st = http.StatusConflict
	case CodeNotFound:
		st = http.StatusNotFound
	case CodeClosed:
		st = http.StatusServiceUnavailable
	}
	writeErr(w, st, msg)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
