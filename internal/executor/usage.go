package executor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/hunknownz/Meerkat/internal/model"
)

const maxLine = 1 << 20 // bytes of one JSON event line kept in memory

// readLines calls fn for every newline-delimited line (without newline). Lines over maxLine are dropped
// and reported through oversize; reading continues with the next line.
func readLines(r io.Reader, fn func([]byte), oversize func()) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if !skipping {
			if len(buf)+len(chunk) > maxLine+1 || (err != nil && !errors.Is(err, bufio.ErrBufferFull) && len(buf)+len(chunk) > maxLine) {
				skipping, buf = true, buf[:0]
				oversize()
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			if !skipping {
				fn(bytes.TrimSuffix(buf, []byte("\n")))
			}
			buf, skipping = buf[:0], false
		case errors.Is(err, bufio.ErrBufferFull):
		default:
			if !skipping && len(buf) > 0 {
				fn(buf)
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type piMessage struct {
	Role       string          `json:"role"`
	Usage      json.RawMessage `json:"usage"`
	StopReason any             `json:"stopReason"`
}

type piEvent struct {
	Type     string          `json:"type"`
	Message  *piMessage      `json:"message"`
	Usage    json.RawMessage `json:"usage"`
	ToolName any             `json:"toolName"`
	Tool     *struct {
		Name any `json:"name"`
	} `json:"tool"`
}

var tokenKeys = [4]string{"input", "output", "cacheRead", "cacheWrite"}

// tracker sums usage only from completed assistant messages; in-flight usage counts toward limits only.
type tracker struct {
	tokens        [4]*int64
	cost          float64
	costTrusted   bool
	messages      int64
	inFlight      int64
	settled       bool
	badLines      int
	invalidFields int
	providerError bool
}

func newTracker() *tracker { return &tracker{costTrusted: true} }

func count(raw json.RawMessage) (int64, bool) {
	var n json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || f < 0 || math.IsInf(f, 0) || f != math.Trunc(f) || f > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

func usageMap(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func sampleTotal(raw json.RawMessage) int64 {
	m := usageMap(raw)
	var t int64
	for _, k := range tokenKeys {
		if v, ok := count(m[k]); ok {
			t += v
		}
	}
	return t
}

func (t *tracker) known() (int64, bool) {
	var s int64
	any := false
	for _, p := range t.tokens {
		if p != nil {
			s += *p
			any = true
		}
	}
	return s, any
}

func (t *tracker) liveTotal() int64 { s, _ := t.known(); return s + t.inFlight }

func (t *tracker) line(b []byte) *piEvent {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var ev piEvent
	if json.Unmarshal(b, &ev) != nil || ev.Type == "" {
		t.badLines++
		return nil
	}
	switch {
	case ev.Type == "agent_settled":
		t.settled = true
	case ev.Type == "message_update":
		if ev.Message == nil || ev.Message.Role == "" || ev.Message.Role == "assistant" {
			raw := ev.Usage
			if ev.Message != nil && len(ev.Message.Usage) > 0 {
				raw = ev.Message.Usage
			}
			t.inFlight = sampleTotal(raw)
		}
	case ev.Type == "message_end" && ev.Message != nil && ev.Message.Role == "assistant":
		m := usageMap(ev.Message.Usage)
		var sample int64
		for i, k := range tokenKeys {
			v, ok := count(m[k])
			if !ok {
				t.invalidFields++
				continue
			}
			if t.tokens[i] == nil {
				t.tokens[i] = new(int64)
			}
			*t.tokens[i] += v
			sample += v
		}
		var c float64
		cok := false
		if cm := usageMap(m["cost"]); cm != nil && json.Unmarshal(cm["total"], &c) == nil && c >= 0 && !math.IsInf(c, 0) {
			cok = true
		}
		// A zero cost alongside non-zero tokens means unknown pricing, not a free bill.
		if cok && !(c == 0 && sample > 0) {
			t.cost += c
		} else {
			t.costTrusted = false
		}
		if s, _ := ev.Message.StopReason.(string); s == "error" {
			t.providerError = true
		}
		t.messages++
		t.inFlight = 0
	}
	return &ev
}

func (t *tracker) completeness(stopped bool) string {
	if _, any := t.known(); t.messages == 0 || !any {
		return model.UsageUnknown
	}
	if t.invalidFields > 0 || t.badLines > 0 || t.inFlight > 0 || t.providerError || stopped || !t.settled {
		return model.UsagePartial
	}
	return model.UsageComplete
}

// usage returns normalized usage. Unknown counters stay nil; total is set only when all four are known.
func (t *tracker) usage(stopped bool) model.Usage {
	c := t.completeness(stopped)
	u := model.Usage{UsageCompleteness: c, Source: model.UsageSourceExecutor}
	dst := []**int64{&u.Tokens.Input, &u.Tokens.Output, &u.Tokens.CacheRead, &u.Tokens.CacheWrite}
	for i, p := range t.tokens {
		if p != nil {
			v := *p
			*dst[i] = &v
		}
	}
	if u.Tokens.Input != nil && u.Tokens.Output != nil && u.Tokens.CacheRead != nil && u.Tokens.CacheWrite != nil {
		s, _ := t.known()
		u.Tokens.Total = &s
	}
	m := t.messages
	u.Tokens.AssistantMessages = &m
	if c == model.UsageComplete && t.costTrusted && t.cost > 0 {
		v := math.Round(t.cost*1e6) / 1e6
		u.EstimatedCostUsd = &v
	}
	return u
}

var toolTypes = map[string]bool{"read": true, "edit": true, "write": true, "bash": true}

// structural maps a Pi event to a safe event summary (no arguments, text or errors).
func structural(ev *piEvent) (string, string, bool) {
	switch {
	case ev.Type == "tool_execution_start":
		name, _ := ev.ToolName.(string)
		if name == "" && ev.Tool != nil {
			name, _ = ev.Tool.Name.(string)
		}
		if !toolTypes[name] {
			name = "other"
		}
		return "tool", name, true
	case ev.Type == "agent_start":
		return "lifecycle", "started", true
	case ev.Type == "agent_settled":
		return "lifecycle", "settled", true
	case ev.Type == "message_end" && ev.Message != nil && ev.Message.Role == "assistant":
		return "lifecycle", "assistant_message", true
	}
	return "", "", false
}
