// Package core is the Go Meerkat workflow core: prepare, schedule and verify developer/reviewer/polisher
// runs under one long-lived, fenced controller lease.
package core

import (
	"context"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

// Registry maps profile executor names to executors.
type Registry map[string]executor.Executor

// Errors returned by the core.
var (
	ErrBusy      = errors.New("core: another dispatch is active")
	ErrClosed    = errors.New("core: closed")
	ErrLeaseLost = store.ErrLeaseLost
)

var (
	errStopRequested     = errors.New("stop_requested")
	errControllerStopped = errors.New("controller_stopped")
)

// Options tune the controller loop.
type Options struct {
	Heartbeat time.Duration // lease heartbeat interval (default 2s)
	Poll      time.Duration // stop-request poll interval (default 500ms)
	Env       []string      // executor child environment (nil means os.Environ)
}

type lane struct {
	cancel context.CancelCauseFunc
	stops  []string
}

// Core owns the controller lease for one store.
type Core struct {
	st    *store.Store
	reg   Registry
	opts  Options
	token string
	host  string

	wmu      sync.Mutex // serializes fenced writes
	dispatch sync.Mutex // one Execute at a time

	mu          sync.Mutex
	lanes       map[string]*lane
	lost        bool
	closed      bool
	dispatching bool

	kick      chan struct{}
	queueKick chan struct{}
	quit      chan struct{}
	bg        sync.WaitGroup
}

// New acquires the controller lease (stale recovery follows store policy), reconciles previously active
// runs to unknown and starts the heartbeat/stop poller.
func New(st *store.Store, reg Registry, opts ...Options) (*Core, error) {
	if st == nil || len(reg) == 0 {
		return nil, invalid("store and executor registry are required")
	}
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 2 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = 500 * time.Millisecond
	}
	l, err := st.AcquireLease()
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	c := &Core{st: st, reg: reg, opts: o, token: l.Token, host: host, lanes: map[string]*lane{}, kick: make(chan struct{}, 1), queueKick: make(chan struct{}, 1), quit: make(chan struct{})}
	if err := c.update(reconcile); err != nil {
		_ = st.ReleaseLease(l.Token)
		return nil, err
	}
	if err := st.ReconcileSessionsOwned(l.Token); err != nil {
		_ = st.ReleaseLease(l.Token)
		return nil, err
	}
	if err := c.reconcileQueue(); err != nil {
		_ = st.ReleaseLease(l.Token)
		return nil, err
	}
	c.bg.Add(2)
	go c.loop()
	go c.queueLoop()
	return c, nil
}

// reconcile marks every previously active run unknown; it never kills or replays processes.
func reconcile(s *model.State) error {
	unknown := map[string]bool{}
	for i := range s.Runs {
		r := &s.Runs[i]
		if model.IsActiveRunState(r.State) {
			r.RecordedState, r.State = r.State, model.RunUnknown
			pushEvent(r, "state", "controller_restart_unverified")
			unknown[r.TaskID] = true
		}
	}
	reason := "controller_interrupted"
	for i := range s.Tasks {
		t := &s.Tasks[i]
		switch {
		case unknown[t.ID]:
			t.RecordedState, t.State, t.StateReason, t.UpdatedAt = t.State, model.TaskUnknown, &reason, now()
		case isDelegateCandidate(*t): // settled local candidate, nothing in flight
		case model.IsActiveTaskState(t.State) || t.State == model.TaskQueued:
			t.State = model.TaskReady
			if slices.ContainsFunc(s.Runs, func(r model.Run) bool { return r.TaskID == t.ID }) {
				t.State = model.TaskStopped
			}
			t.StateReason, t.UpdatedAt = &reason, now()
		}
	}
	return nil
}

// update performs one fenced write; a lost lease cancels every owned execution.
func (c *Core) update(fn func(*model.State) error) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	err := c.st.UpdateOwned(c.token, fn)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return err
}

func (c *Core) isLost() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.lost }

func (c *Core) loseLease() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lost = true
	for _, l := range c.lanes {
		l.cancel(ErrLeaseLost)
	}
}

func (c *Core) loop() {
	defer c.bg.Done()
	poll := time.NewTicker(c.opts.Poll)
	beat := time.NewTicker(c.opts.Heartbeat)
	defer poll.Stop()
	defer beat.Stop()
	for {
		select {
		case <-c.quit:
			return
		case <-beat.C:
			if c.isLost() {
				continue
			}
			c.wmu.Lock()
			err := c.st.RenewLease(c.token)
			c.wmu.Unlock()
			if errors.Is(err, store.ErrLeaseLost) {
				c.loseLease()
			}
		case <-poll.C:
			c.processStops()
		case <-c.kick:
			c.processStops()
		}
	}
}

// processStops applies pending stop requests to owned lanes and records outcomes for finished runs.
func (c *Core) processStops() {
	if c.isLost() {
		return
	}
	reqs, err := c.st.StopRequests()
	if err != nil || len(reqs) == 0 {
		return
	}
	st, err := c.st.Read()
	if err != nil {
		return
	}
	for _, rq := range reqs {
		c.mu.Lock()
		l := c.lanes[rq.RunID]
		fresh := l != nil && !slices.Contains(l.stops, rq.RequestID)
		if fresh {
			l.stops = append(l.stops, rq.RequestID)
			l.cancel(errStopRequested)
		}
		c.mu.Unlock()
		if l != nil {
			if fresh {
				_ = c.update(func(s *model.State) error {
					if r := findRun(s, rq.RunID); r != nil && model.IsActiveRunState(r.State) {
						r.State = model.RunStopping
						pushEvent(r, "state", "stop_requested")
					}
					return nil
				})
			}
			continue
		}
		i := slices.IndexFunc(st.Runs, func(r model.Run) bool { return r.ID == rq.RunID })
		switch {
		case i < 0:
			_ = c.finishStop(rq.RequestID, model.RunUnknown)
		case !model.IsActiveRunState(st.Runs[i].State):
			_ = c.finishStop(rq.RequestID, st.Runs[i].State)
		}
	}
}

func (c *Core) finishStop(requestID, outcome string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	err := c.st.FinishStopOwned(c.token, requestID, outcome)
	if errors.Is(err, store.ErrLeaseLost) {
		go c.loseLease()
	}
	return err
}

// Close cancels owned executions (recorded as stopped), waits for the dispatch and releases the lease.
func (c *Core) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for _, l := range c.lanes {
		l.cancel(errControllerStopped)
	}
	c.mu.Unlock()
	c.dispatch.Lock()
	c.dispatch.Unlock()
	close(c.quit)
	c.bg.Wait()
	if c.isLost() {
		return ErrLeaseLost
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.st.ReleaseLease(c.token)
}

// Stop records an idempotent stop request. Acceptance only means recorded; the owner poller acts on it and
// records the run's actual outcome in the receipt.
func (c *Core) Stop(runID, requestID string) (model.StopReceipt, error) {
	if !uuidRE.MatchString(runID) {
		return model.StopReceipt{}, invalid("runId must be a lowercase UUID")
	}
	rc, err := c.st.RequestStop(runID, requestID)
	if err == nil {
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
	return rc, err
}

func (c *Core) settings() (model.Settings, error) {
	s, err := c.st.GetSettings()
	if err != nil {
		return s, err
	}
	if s.MaxConcurrency < model.MinConcurrency || s.MaxConcurrency > model.MaxConcurrency {
		s.MaxConcurrency = model.DefaultMaxConcurrency
	}
	if s.MaxFixRounds < 0 || s.MaxFixRounds > model.MaxFixRoundsLimit {
		s.MaxFixRounds = model.DefaultMaxFixRounds
	}
	if s.DefaultProfiles == nil {
		s.DefaultProfiles = map[string]map[string]string{}
	}
	return s, nil
}

// ParseSettingsPatch strictly decodes a settings patch.
func ParseSettingsPatch(raw []byte) (model.SettingsPatch, error) {
	var p model.SettingsPatch
	return p, decodeStrict(raw, &p, 64<<10)
}

// Settings validates and applies a future-run settings patch. Existing runs keep their frozen profiles.
func (c *Core) Settings(p model.SettingsPatch) (model.Settings, error) {
	// Serialize the whole read/merge/write across CLI, HTTP and MCP callers.
	// Locking only the write would let disjoint patches overwrite each other.
	c.wmu.Lock()
	defer c.wmu.Unlock()
	cur, err := c.settings()
	if err != nil {
		return cur, err
	}
	if p.MaxConcurrency != nil {
		if *p.MaxConcurrency < model.MinConcurrency || *p.MaxConcurrency > model.MaxConcurrency {
			return cur, invalid("maxConcurrency must be an integer 1..4")
		}
		cur.MaxConcurrency = *p.MaxConcurrency
	}
	if p.MaxFixRounds != nil {
		if *p.MaxFixRounds < 0 || *p.MaxFixRounds > model.MaxFixRoundsLimit {
			return cur, invalid("maxFixRounds must be an integer 0..2")
		}
		cur.MaxFixRounds = *p.MaxFixRounds
	}
	if p.DefaultProfiles != nil {
		st, err := c.st.Read()
		if err != nil {
			return cur, err
		}
		merged := map[string]map[string]string{}
		for k, v := range cur.DefaultProfiles {
			merged[k] = v
		}
		for projectID, roles := range p.DefaultProfiles {
			if !slices.ContainsFunc(st.Projects, func(x model.Project) bool { return x.ID == projectID }) {
				return cur, invalid("defaultProfiles references an unknown project")
			}
			m := map[string]string{}
			for k, v := range merged[projectID] {
				m[k] = v
			}
			for role, id := range roles {
				if !model.IsRole(role) {
					return cur, invalid("defaultProfiles has an unknown role")
				}
				i := slices.IndexFunc(st.Profiles, func(x model.Profile) bool { return x.ID == id })
				if i < 0 || st.Profiles[i].ProjectID != projectID || st.Profiles[i].Role != role {
					return cur, invalid("defaultProfiles must reference a registered profile of the same project and role")
				}
				if _, ok := c.reg[execName(st.Profiles[i])]; !ok {
					return cur, invalid("defaultProfiles references a profile with an unregistered executor")
				}
				m[role] = id
			}
			merged[projectID] = m
		}
		cur.DefaultProfiles = merged
	}
	cur.UpdatedAt = now()
	if c.isLost() {
		return cur, ErrLeaseLost
	}
	err = c.st.SetSettingsOwned(c.token, cur)
	if errors.Is(err, store.ErrLeaseLost) {
		go c.loseLease()
	}
	return cur, err
}

func execName(p model.Profile) string {
	if p.Executor == "" {
		return "pi"
	}
	return p.Executor
}

// ---------- public snapshot ----------

// PublicTask is the task projection with aggregate usage.
type PublicTask struct {
	model.Task
	WorktreeExists bool                 `json:"worktreeExists"`
	Usage          model.AggregateUsage `json:"usage"`
}

// Counts are snapshot counters.
type Counts struct {
	Running int `json:"running"`
	Queued  int `json:"queued"`
	Unknown int `json:"unknown"`
}

// Snapshot is the secret-free public workflow view (schemaVersion 1).
type Snapshot struct {
	SchemaVersion int                    `json:"schemaVersion"`
	ObservedAt    string                 `json:"observedAt"`
	Controller    model.ControllerStatus `json:"controller"`
	Projects      []model.Project        `json:"projects"`
	Contexts      []model.PublicContext  `json:"contexts"`
	Tasks         []PublicTask           `json:"tasks"`
	Runs          []model.Run            `json:"runs"`
	Deliveries    []model.Delivery       `json:"deliveries"`
	Reviews       []model.Review         `json:"reviews"`
	Profiles      []model.PublicProfile  `json:"profiles"`
	Settings      model.Settings         `json:"settings"`
	Counts        Counts                 `json:"counts"`
	Usage         model.AggregateUsage   `json:"usage"`
}

var safeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)

func safeName(s string) string {
	if s == "" || (safeNameRE.MatchString(s) && !model.LooksLikeCredential(s)) {
		return s
	}
	return "[redacted]"
}

// Snapshot reads a fresh public view. A failed read is an error, never an empty state.
func (c *Core) Snapshot() (Snapshot, error) {
	st, err := c.st.Read()
	if err != nil {
		return Snapshot{}, err
	}
	set, err := c.settings()
	if err != nil {
		return Snapshot{}, err
	}
	facts, err := c.st.LeaseFacts()
	if err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	live := !c.lost && !c.closed && facts.Present && !facts.Stale
	dispatching := c.dispatching
	c.mu.Unlock()
	if hb, err := time.Parse(time.RFC3339Nano, facts.HeartbeatAt); live && (err != nil || time.Since(hb) > 3*c.opts.Heartbeat+time.Second) {
		live = false
	}
	ctl := model.ControllerStatus{State: model.ControllerUnknown}
	if facts.Present {
		hb := facts.HeartbeatAt
		ctl.HeartbeatAt = &hb
	}
	if live {
		ctl.State = model.ControllerIdle
		if dispatching {
			ctl.State = model.ControllerRunning
		}
	}
	out := Snapshot{SchemaVersion: model.SchemaVersion, ObservedAt: now(), Controller: ctl, Projects: st.Projects,
		Contexts: []model.PublicContext{}, Tasks: []PublicTask{}, Runs: []model.Run{}, Deliveries: st.Deliveries, Reviews: st.Reviews,
		Profiles: []model.PublicProfile{}, Settings: set, Usage: model.Aggregate(st.Runs)}
	for _, x := range st.Contexts {
		out.Contexts = append(out.Contexts, x.Public())
	}
	for _, r := range st.Runs {
		p := r.Public()
		p.ProfileID, p.Executor, p.Role = safeName(p.ProfileID), safeName(p.Executor), safeName(p.Role)
		if p.ModelSnapshot != nil {
			m := *p.ModelSnapshot
			m.ProfileID, m.Provider, m.Model = safeName(m.ProfileID), safeName(m.Provider), safeName(m.Model)
			p.ModelSnapshot = &m
		}
		if !live && model.IsActiveRunState(p.State) {
			p.RecordedState, p.State = p.State, model.RunUnknown
		}
		switch p.State {
		case model.RunRunning:
			out.Counts.Running++
		case model.RunUnknown:
			out.Counts.Unknown++
		}
		out.Runs = append(out.Runs, p)
	}
	for _, t := range st.Tasks {
		pt := PublicTask{Task: t, Usage: model.Aggregate(slices.DeleteFunc(slices.Clone(st.Runs), func(r model.Run) bool { return r.TaskID != t.ID }))}
		ids := map[string]string{}
		for k, v := range t.ProfileIDs {
			ids[safeName(k)] = safeName(v)
		}
		pt.ProfileIDs = ids
		if !live && model.IsActiveTaskState(t.State) && !isDelegateCandidate(t) {
			pt.RecordedState, pt.State = t.State, model.TaskUnknown
		}
		if fi, err := os.Stat(t.Worktree); err == nil && fi.IsDir() {
			pt.WorktreeExists = true
		}
		if pt.State == model.TaskQueued {
			out.Counts.Queued++
		}
		out.Tasks = append(out.Tasks, pt)
	}
	for _, p := range st.Profiles {
		pp := p.Public()
		pp.ID, pp.Role, pp.Executor, pp.Provider, pp.Model = safeName(pp.ID), safeName(pp.Role), safeName(pp.Executor), safeName(pp.Provider), safeName(pp.Model)
		out.Profiles = append(out.Profiles, pp)
	}
	return out, nil
}

// ---------- small state helpers ----------

func findRun(s *model.State, id string) *model.Run {
	for i := range s.Runs {
		if s.Runs[i].ID == id {
			return &s.Runs[i]
		}
	}
	return nil
}

func findTask(s *model.State, id string) *model.Task {
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			return &s.Tasks[i]
		}
	}
	return nil
}

func findContext(s *model.State, ref model.ContextRef) *model.Context {
	for i := range s.Contexts {
		c := &s.Contexts[i]
		if c.ID == ref.ID && c.Version == ref.Version && c.Digest == ref.Digest {
			return c
		}
	}
	return nil
}

var (
	eventTypeRE    = regexp.MustCompile(`^[a-z][a-z_]{0,19}$`)
	eventSummaryRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:/-]{0,79}$`)
)

func pushEvent(r *model.Run, typ, summary string) {
	if !eventTypeRE.MatchString(typ) || !eventSummaryRE.MatchString(summary) || model.LooksLikeCredential(summary) {
		return
	}
	ts := now()
	r.Events = append(r.Events, model.RunEvent{Type: typ, Summary: strings.TrimSpace(summary), ObservedAt: ts})
	if len(r.Events) > model.MaxRunEvents {
		r.Events = slices.Clone(r.Events[len(r.Events)-model.MaxRunEvents:])
	}
	r.UpdatedAt = ts
}
