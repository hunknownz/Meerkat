// Package pirpc implements Pi's private JSONL subprocess control protocol.
// Scheduling, process ownership, budgets and delivery verification belong to
// the caller. New does not start a process, send a prompt or replay a command.
package pirpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxRecordBytes = 1 << 20
	maxPending     = 64
	maxEvents      = 64
)

// Failure describes command delivery, without exposing provider diagnostics.
type Failure string

const (
	NotSent   Failure = "not_sent"
	Rejected  Failure = "rejected"
	Uncertain Failure = "uncertain"
	Protocol  Failure = "protocol"
	Closed    Failure = "closed"
)

// Error has fixed text. Uncertain means the command may have taken effect and
// must not be retried automatically, including after a deadline or lost reply.
type Error struct {
	Kind  Failure
	cause error
}

func (e *Error) Error() string {
	switch e.Kind {
	case NotSent:
		return "pi rpc: command not sent"
	case Rejected:
		return "pi rpc: command rejected"
	case Uncertain:
		return "pi rpc: command outcome unknown"
	case Protocol:
		return "pi rpc: invalid protocol"
	default:
		return "pi rpc: connection closed"
	}
}

func (e *Error) Unwrap() error { return e.cause }

// Receipt acknowledges command handling, not model or task completion.
type Receipt struct {
	ID          string
	Accepted    bool
	Disposition string // started, queued or handled; empty for control commands
}

type record struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
}

type reply struct {
	data json.RawMessage
	err  error
}

type request struct {
	id, command string
	ctx         context.Context
	wire        []byte
	input       bool
	shutdown    bool
	sent        bool // guarded by Client.mu, set before touching the pipe
	reply       chan reply
}

// Client owns the supplied pipes. Their Close methods must unblock Read/Write,
// as os.Pipe and io.Pipe do. Drain Events continuously; its bounded queue applies
// backpressure instead of silently dropping events or accumulating transcripts.
type Client struct {
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	prefix        string
	mu            sync.Mutex
	next          uint64
	end           error
	inputsStopped bool
	shuttingDown  bool
	pending       map[string]*request
	outgoing      chan *request
	events        chan Event
	done          chan struct{}
	readDone      chan struct{}
	writeDone     chan struct{}
	closeOnce     sync.Once
	stopGate      chan struct{}
	stopResult    *StopReceipt // guarded by stopGate
}

func New(stdin io.WriteCloser, stdout io.ReadCloser) (*Client, error) {
	if stdin == nil || stdout == nil {
		return nil, &Error{Kind: NotSent}
	}
	var seed [12]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, &Error{Kind: NotSent}
	}
	c := &Client{stdin: stdin, stdout: stdout, prefix: hex.EncodeToString(seed[:]),
		pending: make(map[string]*request), outgoing: make(chan *request, maxPending),
		events: make(chan Event, maxEvents), done: make(chan struct{}),
		readDone: make(chan struct{}), writeDone: make(chan struct{}), stopGate: make(chan struct{}, 1)}
	c.stopGate <- struct{}{}
	go c.readLoop()
	go c.writeLoop()
	return c, nil
}

func (c *Client) Events() <-chan Event  { return c.events }
func (c *Client) Done() <-chan struct{} { return c.done }

// Err reports why the transport ended; it never includes wire contents.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.end
}

// Close closes the transport, without claiming the child process stopped.
// The process owner must separately close/reap its owned child and verify state.
func (c *Client) Close() error {
	c.finish(&Error{Kind: Closed})
	<-c.readDone
	<-c.writeDone
	return nil
}

// Shutdown closes input after the current write and drains output until the
// transport ends. The caller must continue consuming Events and independently
// reap/verify the child. A deadline forces transport closure, with an uncertain
// outcome; queued commands not yet written are rejected. It never claims a
// successful process exit or delivered task.
func (c *Client) Shutdown(ctx context.Context) error {
	if ctx.Err() != nil {
		return &Error{Kind: NotSent, cause: ctx.Err()}
	}
	c.mu.Lock()
	first := !c.shuttingDown && c.end == nil
	c.shuttingDown = true
	c.mu.Unlock()
	if first {
		r := &request{shutdown: true, ctx: ctx}
		select {
		case c.outgoing <- r:
		case <-c.done:
		case <-ctx.Done():
			c.finish(&Error{Kind: Closed})
			return &Error{Kind: Uncertain, cause: ctx.Err()}
		}
	}
	select {
	case <-c.done:
		var err *Error
		if errors.As(c.Err(), &err) && err.Kind != Closed {
			return err
		}
		return nil
	case <-ctx.Done():
		c.finish(&Error{Kind: Closed})
		return &Error{Kind: Uncertain, cause: ctx.Err()}
	}
}

func (c *Client) finish(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.end = err
		close(c.done)
		c.mu.Unlock()
		_ = c.stdin.Close()
		_ = c.stdout.Close()
	})
}

func (c *Client) call(ctx context.Context, command, message string, input bool) (json.RawMessage, Receipt, error) {
	if ctx.Err() != nil || (input && (strings.TrimSpace(message) == "" || !utf8.ValidString(message))) {
		return nil, Receipt{}, &Error{Kind: NotSent, cause: ctx.Err()}
	}
	c.mu.Lock()
	if c.end != nil || c.shuttingDown || len(c.pending) >= maxPending || (input && c.inputsStopped) {
		c.mu.Unlock()
		return nil, Receipt{}, &Error{Kind: NotSent}
	}
	c.next++
	id := c.prefix + "-" + strconv.FormatUint(c.next, 10)
	r := &request{id: id, command: command, ctx: ctx, input: input, reply: make(chan reply, 1)}
	wire := struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Message string `json:"message,omitempty"`
	}{id, command, message}
	var err error
	r.wire, err = json.Marshal(wire)
	if err != nil || len(r.wire) > maxRecordBytes {
		c.mu.Unlock()
		return nil, Receipt{}, &Error{Kind: NotSent}
	}
	r.wire = append(r.wire, '\n')
	c.pending[id] = r
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	rc := Receipt{ID: id}
	select {
	case c.outgoing <- r:
	case <-ctx.Done():
		return nil, rc, c.deliveryError(r, ctx.Err())
	case <-c.done:
		return nil, rc, c.deliveryError(r, c.Err())
	}
	select {
	case v := <-r.reply:
		if v.err != nil {
			return nil, rc, v.err
		}
		rc.Accepted = true
		return v.data, rc, nil
	case <-ctx.Done():
		return nil, rc, c.deliveryError(r, ctx.Err())
	case <-c.done:
		// Prefer a response already received before EOF over an uncertain result.
		select {
		case v := <-r.reply:
			if v.err != nil {
				return nil, rc, v.err
			}
			rc.Accepted = true
			return v.data, rc, nil
		default:
			return nil, rc, c.deliveryError(r, c.Err())
		}
	}
}

func (c *Client) deliveryError(r *request, cause error) error {
	c.mu.Lock()
	sent := r.sent
	c.mu.Unlock()
	kind := NotSent
	if sent {
		kind = Uncertain
	}
	return &Error{Kind: kind, cause: cause}
}

func (c *Client) writeLoop() {
	defer close(c.writeDone)
	for {
		select {
		case <-c.done:
			return
		case r := <-c.outgoing:
			if r.shutdown {
				_ = c.stdin.Close()
				continue
			}
			c.mu.Lock()
			if c.end != nil || c.shuttingDown || r.ctx.Err() != nil || (r.input && c.inputsStopped) {
				c.mu.Unlock()
				select {
				case r.reply <- reply{err: &Error{Kind: NotSent, cause: r.ctx.Err()}}:
				default:
				}
				continue
			}
			r.sent = true
			c.mu.Unlock()
			b := r.wire
			for len(b) > 0 {
				n, err := c.stdin.Write(b)
				if n < 0 || n > len(b) || (n == 0 && err == nil) {
					err = io.ErrShortWrite
				}
				if err != nil {
					c.finish(&Error{Kind: Closed})
					return
				}
				b = b[n:]
			}
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.readDone)
	defer close(c.events)
	br := bufio.NewReaderSize(c.stdout, 64<<10)
	var line []byte
	for {
		b, err := br.ReadSlice('\n')
		if len(line)+len(b) > maxRecordBytes+2 {
			c.finish(&Error{Kind: Protocol})
			return
		}
		line = append(line, b...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			kind := Closed
			if len(line) != 0 {
				kind = Protocol
			}
			c.finish(&Error{Kind: kind})
			return
		}
		b = bytes.TrimSuffix(line, []byte{'\n'})
		b = bytes.TrimSuffix(b, []byte{'\r'})
		if len(b) > maxRecordBytes {
			c.finish(&Error{Kind: Protocol})
			return
		}
		if len(b) > 0 && !c.readRecord(b) {
			return
		}
		line = line[:0]
	}
}

func (c *Client) readRecord(b []byte) bool {
	var v record
	if !utf8.Valid(b) || json.Unmarshal(b, &v) != nil || v.Type == "" {
		c.finish(&Error{Kind: Protocol})
		return false
	}
	if v.Type == "response" {
		if v.ID == "" || v.Success == nil {
			c.finish(&Error{Kind: Protocol})
			return false
		}
		c.mu.Lock()
		r := c.pending[v.ID]
		c.mu.Unlock()
		if r == nil {
			return true
		} // late or duplicate reply; never replay
		if v.Command != r.command {
			c.finish(&Error{Kind: Protocol})
			return false
		}
		rep := reply{data: v.Data}
		if !*v.Success {
			rep = reply{err: &Error{Kind: Rejected}}
		}
		select {
		case r.reply <- rep:
		default:
		}
		return true
	}
	ev, ok := eventSummary(v.Type, b)
	if !ok {
		return true
	}
	select {
	case c.events <- ev:
		return true
	case <-c.done:
		return false
	}
}

func (c *Client) input(ctx context.Context, command, message string) (Receipt, error) {
	data, rc, err := c.call(ctx, command, message, true)
	if err != nil {
		return rc, err
	}
	var d struct {
		Disposition string `json:"disposition"`
	}
	if json.Unmarshal(data, &d) != nil || (d.Disposition != "queued" && d.Disposition != "handled" &&
		(command != "prompt" || d.Disposition != "started")) {
		return Receipt{ID: rc.ID}, &Error{Kind: Uncertain, cause: &Error{Kind: Protocol}}
	}
	rc.Disposition = d.Disposition
	return rc, nil
}

func (c *Client) Prompt(ctx context.Context, message string) (Receipt, error) {
	return c.input(ctx, "prompt", message)
}
func (c *Client) Steer(ctx context.Context, message string) (Receipt, error) {
	return c.input(ctx, "steer", message)
}
func (c *Client) FollowUp(ctx context.Context, message string) (Receipt, error) {
	return c.input(ctx, "follow_up", message)
}
