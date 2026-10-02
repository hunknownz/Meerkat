package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

// Import limits.
const (
	MaxLegacyStateBytes = 32 << 20
	MaxLegacySmallBytes = 64 << 10
	MaxLegacyBodyBytes  = 256 << 10
	MaxLegacyFiles      = 4096
)

var (
	ErrLiveController = errors.New("store: live controller")
	ErrUnsafeSource   = errors.New("store: unsafe legacy source")
	ErrCorruptSource  = errors.New("store: corrupt legacy source")
)

var hashHexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ImportConflict names one divergent record (no payload content).
type ImportConflict struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ImportReport is the sanitized result of ImportLegacy: counts and hashes only, no context or transcript.
type ImportReport struct {
	ImportID     string           `json:"importId"`
	SourceHash   string           `json:"sourceHash"`
	Repeat       bool             `json:"repeat"`
	Imported     map[string]int   `json:"imported"`
	Duplicates   map[string]int   `json:"duplicates"`
	Conflicts    []ImportConflict `json:"conflicts"`
	UnknownUsage int              `json:"unknownUsage"`
	UnknownRuns  int              `json:"unknownRuns"`
	UnknownTasks int              `json:"unknownTasks"`
	SettingsKept bool             `json:"settingsKept"`
	CreatedAt    string           `json:"createdAt"`
}

// HistoryEntry is a non-executable record of legacy work (no frozen contract; never scheduled).
type HistoryEntry struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"`
	TaskID      string        `json:"taskId,omitempty"`
	RunID       string        `json:"runId,omitempty"`
	ProjectID   string        `json:"projectId,omitempty"`
	Title       string        `json:"title,omitempty"`
	Role        string        `json:"role,omitempty"`
	Executor    string        `json:"executor,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	Model       string        `json:"model,omitempty"`
	ChangeID    *string       `json:"changeId"`
	State       string        `json:"state,omitempty"`
	StartedAt   string        `json:"startedAt,omitempty"`
	EndedAt     *string       `json:"endedAt"`
	WallSeconds *float64      `json:"wallSeconds"`
	Usage       *model.Usage  `json:"usage"`
	Source      string        `json:"source"`
	SourceHash  string        `json:"sourceHash,omitempty"`
	Executable  bool          `json:"executable"`
	Extra       *HistoryExtra `json:"extra,omitempty"`
	CreatedAt   string        `json:"createdAt"`
}

// HistoryExtra carries bounded public facts for legacy dashboard records.
type HistoryExtra struct {
	Collection string `json:"collection,omitempty"`
	Stage      string `json:"stage,omitempty"`
}

// ---------- legacy typed state (exact 0.2.x FIELDS) ----------

type legacyProfile struct {
	ID           string              `json:"id"`
	ProjectID    string              `json:"projectId"`
	Role         string              `json:"role"`
	Provider     string              `json:"provider"`
	Model        string              `json:"model"`
	AuthEnv      string              `json:"authEnv,omitempty"`
	Instructions []string            `json:"instructions"`
	Limits       model.ProfileLimits `json:"limits"`
	PiCommand    []string            `json:"piCommand"`
	ConfigFile   string              `json:"configFile,omitempty"`
	ConfigDigest string              `json:"configDigest,omitempty"`
	CreatedAt    string              `json:"createdAt"`
}

type legacyTask struct {
	ID           string            `json:"id"`
	ProjectID    string            `json:"projectId"`
	Repository   string            `json:"repository"`
	Worktree     string            `json:"worktree"`
	Branch       *string           `json:"branch"`
	Title        string            `json:"title"`
	Goal         string            `json:"goal"`
	Scope        []string          `json:"scope"`
	Acceptance   []string          `json:"acceptance"`
	Dependencies []string          `json:"dependencies"`
	ContextRef   *model.ContextRef `json:"contextRef"`
	ProfileIDs   map[string]string `json:"profileIds"`
	State        string            `json:"state"`
	StateReason  *string           `json:"stateReason"`
	ResumeRole   *string           `json:"resumeRole"`
	CandidateSha *string           `json:"candidateSha"`
	BaselineSha  *string           `json:"baselineSha"`
	CreatedAt    string            `json:"createdAt"`
	UpdatedAt    string            `json:"updatedAt"`
	IssueRef     *model.IssueRef   `json:"issueRef"`
	Budget       *model.Budget     `json:"budget"`
}

type legacyRun struct {
	ID            string               `json:"id"`
	AgentID       string               `json:"agentId"`
	TaskID        string               `json:"taskId"`
	Role          string               `json:"role"`
	ProfileID     string               `json:"profileId"`
	ModelSnapshot *model.ModelSnapshot `json:"modelSnapshot"`
	ContextRef    *model.ContextRef    `json:"contextRef"`
	State         string               `json:"state"`
	PID           *int                 `json:"pid"`
	StartedAt     string               `json:"startedAt"`
	EndedAt       *string              `json:"endedAt"`
	UpdatedAt     string               `json:"updatedAt"`
	Events        []model.RunEvent     `json:"events"`
	Summary       json.RawMessage      `json:"summary"`
	Usage         json.RawMessage      `json:"usage"`
}

type legacyState struct {
	SchemaVersion int              `json:"schemaVersion"`
	Projects      []model.Project  `json:"projects"`
	Contexts      []model.Context  `json:"contexts"`
	Tasks         []legacyTask     `json:"tasks"`
	Runs          []legacyRun      `json:"runs"`
	Deliveries    []model.Delivery `json:"deliveries"`
	Reviews       []model.Review   `json:"reviews"`
	Profiles      []legacyProfile  `json:"profiles"`
}

type legacyIssueReceipt struct {
	SchemaVersion int     `json:"schemaVersion"`
	DeliveryID    string  `json:"deliveryId"`
	TaskID        string  `json:"taskId"`
	IssueURL      *string `json:"issueUrl"`
	BodyHash      string  `json:"bodyHash"`
	State         string  `json:"state"`
	Attempts      int     `json:"attempts"`
	PreparedAt    string  `json:"preparedAt"`
	UpdatedAt     string  `json:"updatedAt"`
	PostedAt      string  `json:"postedAt,omitempty"`
	CommentURL    string  `json:"commentUrl,omitempty"`
	LastError     string  `json:"lastError,omitempty"`
	LastAttemptAt string  `json:"lastAttemptAt,omitempty"`
	FoundExisting bool    `json:"foundExisting,omitempty"`
}

// ---------- safe bounded source reading ----------

type sourceFile struct {
	rel  string
	data []byte
}

type sourceSet struct {
	files []sourceFile
}

func ownedNoSymlink(fi os.FileInfo) bool {
	if fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// readSafe reads a regular, non-symlinked, owned file bounded by max. Missing returns (nil, nil).
func readSafe(path string, max int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !fi.Mode().IsRegular() || !ownedNoSymlink(fi) {
		return nil, fmt.Errorf("%w: not a private regular file", ErrUnsafeSource)
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%w: file exceeds size limit", ErrUnsafeSource)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open failed", ErrUnsafeSource)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, fmt.Errorf("%w: read failed", ErrUnsafeSource)
	}
	return b, nil
}

// safeDir checks that dir is a real (non-symlink) owned directory. Missing returns false, nil.
func safeDir(dir string) (bool, error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !fi.IsDir() || !ownedNoSymlink(fi) {
		return false, fmt.Errorf("%w: unsafe directory", ErrUnsafeSource)
	}
	return true, nil
}

func (ss *sourceSet) add(rel string, data []byte) error {
	if len(ss.files) >= MaxLegacyFiles {
		return fmt.Errorf("%w: too many files", ErrUnsafeSource)
	}
	ss.files = append(ss.files, sourceFile{rel, data})
	return nil
}

// listJSON returns sorted file names matching re in dir (safe dir required).
func listDir(dir string, re *regexp.Regexp) ([]string, error) {
	ok, err := safeDir(dir)
	if err != nil || !ok {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: list failed", ErrUnsafeSource)
	}
	var out []string
	for _, e := range ents {
		if re.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	if len(out) > MaxLegacyFiles {
		return nil, fmt.Errorf("%w: too many files", ErrUnsafeSource)
	}
	sort.Strings(out)
	return out, nil
}

func (ss *sourceSet) hash() string {
	h := sha256.New()
	sorted := append([]sourceFile{}, ss.files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rel < sorted[j].rel })
	for _, f := range sorted {
		fh := sha256.Sum256(f.data)
		fmt.Fprintf(h, "%s\x00%x\n", f.rel, fh)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func decodeStrict(b []byte, v any) error {
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

func corrupt(what string) error { return fmt.Errorf("%w: %s", ErrCorruptSource, what) }

func liveOwner(path string) (bool, error) {
	b, err := readSafe(path, 4096)
	if err != nil {
		return true, err
	}
	if b == nil {
		return false, nil
	}
	var o struct {
		PID  int    `json:"pid"`
		Host string `json:"host"`
	}
	if json.Unmarshal(b, &o) != nil {
		return true, nil // unreadable owner: refuse conservatively
	}
	host, _ := os.Hostname()
	return o.Host != host || pidAlive(o.PID), nil
}

func jsCanonical(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	s := strings.TrimSuffix(buf.String(), "\n")
	return strings.NewReplacer(`\u2028`, "\u2028", `\u2029`, "\u2029").Replace(s)
}

func contextDigest(c model.Context) string {
	src := make([]map[string]any, 0, len(c.Sources))
	for _, s := range c.Sources {
		m := map[string]any{}
		if s.URL != "" {
			m["url"] = s.URL
		}
		if s.Title != "" {
			m["title"] = s.Title
		}
		if s.Hash != "" {
			m["hash"] = s.Hash
		}
		src = append(src, m)
	}
	sum := sha256.Sum256([]byte(jsCanonical(map[string]any{"text": c.Text, "sources": src})))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ---------- import plan ----------

type importPlan struct {
	state    *model.State
	history  []HistoryEntry
	settings *model.Settings
	stops    []model.StopReceipt
	receipts []IssueReceipt
	bodies   map[string][]byte // deliveryID -> body
	report   ImportReport
}

func checkProfile(p legacyProfile) error {
	if p.AuthEnv != "" && !model.ValidEnvName(p.AuthEnv) {
		return model.Invalidf("profile authEnv must be an environment variable name")
	}
	if err := model.CheckStrings("profile", append(append([]string{p.Provider, p.Model, p.ConfigFile, p.ConfigDigest}, p.Instructions...), p.PiCommand...)...); err != nil {
		return err
	}
	for _, a := range p.PiCommand {
		if i := strings.IndexByte(a, '='); i > 0 && model.SecretKeyRE.MatchString(strings.TrimLeft(a[:i], "-")) {
			return model.Invalidf("profile piCommand embeds a secret-like assignment")
		}
	}
	return nil
}

func freeForm(where string, raws ...json.RawMessage) error {
	for _, r := range raws {
		if err := model.CheckRawFreeForm(r, where); err != nil {
			return err
		}
	}
	return nil
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func wallOf(start string, end *string) *float64 {
	a, ok1 := parseTime(start)
	if end == nil {
		return nil
	}
	b, ok2 := parseTime(*end)
	if !ok1 || !ok2 || b.Before(a) {
		return nil
	}
	v := b.Sub(a).Seconds()
	return &v
}

func buildState(ls *legacyState, p *importPlan, srcHash string) error {
	if ls.SchemaVersion != 1 {
		return corrupt("unsupported state schemaVersion")
	}
	st := p.state
	projects := map[string]bool{}
	for _, pr := range ls.Projects {
		if pr.ID == "" || projects[pr.ID] {
			return corrupt("invalid or duplicate project")
		}
		if err := model.CheckStrings("project", append([]string{pr.Name}, pr.Repositories...)...); err != nil {
			return err
		}
		projects[pr.ID] = true
		st.Projects = append(st.Projects, pr)
	}
	type ck struct {
		id string
		v  int
	}
	ctx := map[ck]model.Context{}
	for _, c := range ls.Contexts {
		k := ck{c.ID, c.Version}
		if c.ID == "" || c.Version < 1 || !projects[c.ProjectID] {
			return corrupt("invalid context relationship")
		}
		if _, dup := ctx[k]; dup {
			return corrupt("duplicate context version")
		}
		if contextDigest(c) != c.Digest {
			return corrupt("context digest mismatch")
		}
		if err := model.CheckStrings("context", c.Text); err != nil {
			return err
		}
		ctx[k] = c
		st.Contexts = append(st.Contexts, c)
	}
	profiles := map[string]legacyProfile{}
	for _, lp := range ls.Profiles {
		if lp.ID == "" || !projects[lp.ProjectID] || !model.IsRole(lp.Role) {
			return corrupt("invalid profile relationship")
		}
		if _, dup := profiles[lp.ID]; dup {
			return corrupt("duplicate profile")
		}
		if err := checkProfile(lp); err != nil {
			return err
		}
		profiles[lp.ID] = lp
		st.Profiles = append(st.Profiles, model.Profile{ID: lp.ID, ProjectID: lp.ProjectID, Role: lp.Role, Executor: "pi", Provider: lp.Provider,
			Model: lp.Model, AuthEnv: lp.AuthEnv, Instructions: lp.Instructions, Limits: lp.Limits, PiCommand: lp.PiCommand,
			ConfigFile: lp.ConfigFile, ConfigDigest: lp.ConfigDigest, CreatedAt: lp.CreatedAt})
	}
	tasks := map[string]*legacyTask{}
	historyTask := map[string]bool{}
	for i := range ls.Tasks {
		t := &ls.Tasks[i]
		if t.ID == "" || tasks[t.ID] != nil || !projects[t.ProjectID] {
			return corrupt("invalid task relationship")
		}
		if err := model.CheckStrings("task", append(append([]string{t.Title, t.Goal, t.Repository, t.Worktree}, t.Scope...), t.Acceptance...)...); err != nil {
			return err
		}
		tasks[t.ID] = t
		frozen := t.ContextRef != nil && t.ContextRef.ID != "" && len(t.ProfileIDs) > 0
		if !frozen {
			historyTask[t.ID] = true
			continue
		}
		c, ok := ctx[ck{t.ContextRef.ID, t.ContextRef.Version}]
		if !ok || c.Digest != t.ContextRef.Digest || c.ProjectID != t.ProjectID {
			return corrupt("task context reference mismatch")
		}
		for role, pid := range t.ProfileIDs {
			pr, ok := profiles[pid]
			if !model.IsRole(role) || !ok || pr.Role != role || pr.ProjectID != t.ProjectID {
				return corrupt("task profile reference mismatch")
			}
		}
	}
	for _, t := range ls.Tasks {
		for _, d := range t.Dependencies {
			if tasks[d] == nil || (!historyTask[t.ID] && historyTask[d]) {
				return corrupt("task dependency reference mismatch")
			}
		}
		if historyTask[t.ID] {
			p.history = append(p.history, HistoryEntry{ID: "task:" + t.ID, Kind: "legacy_task", TaskID: t.ID, ProjectID: t.ProjectID,
				Title: t.Title, State: t.State, StartedAt: t.CreatedAt, Source: model.UsageSourceLegacyState, SourceHash: srcHash, CreatedAt: now()})
			continue
		}
		nt := model.Task{ID: t.ID, ProjectID: t.ProjectID, Repository: t.Repository, Worktree: t.Worktree, Branch: t.Branch, Title: t.Title,
			Goal: t.Goal, Scope: t.Scope, Acceptance: t.Acceptance, Dependencies: append([]string{}, t.Dependencies...), ContextRef: *t.ContextRef,
			ProfileIDs: t.ProfileIDs, State: t.State, StateReason: t.StateReason, ResumeRole: t.ResumeRole, CandidateSha: t.CandidateSha,
			BaselineSha: t.BaselineSha, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, IssueRef: t.IssueRef, Budget: t.Budget, Origin: model.OriginLegacyImport}
		if !contains(model.TaskStates, t.State) {
			return corrupt("task state is invalid")
		}
		if model.IsActiveTaskState(t.State) || t.State == model.TaskQueued {
			nt.RecordedState, nt.State = t.State, model.TaskUnknown
			p.report.UnknownTasks++
		}
		st.Tasks = append(st.Tasks, nt)
	}
	runs := map[string]legacyRun{}
	for _, r := range ls.Runs {
		t := tasks[r.TaskID]
		if r.ID == "" || t == nil || !model.IsRole(r.Role) || !contains(model.RunStates, r.State) {
			return corrupt("invalid run relationship")
		}
		if _, dup := runs[r.ID]; dup {
			return corrupt("duplicate run")
		}
		if err := freeForm("run", r.Summary, r.Usage); err != nil {
			return err
		}
		for _, e := range r.Events {
			if err := model.CheckStrings("run event", e.Summary); err != nil {
				return err
			}
		}
		if r.ContextRef != nil {
			c, ok := ctx[ck{r.ContextRef.ID, r.ContextRef.Version}]
			if !ok || c.Digest != r.ContextRef.Digest {
				return corrupt("run context reference mismatch")
			}
		}
		if r.ProfileID != "" {
			if _, ok := profiles[r.ProfileID]; !ok {
				return corrupt("run profile reference mismatch")
			}
		}
		runs[r.ID] = r
		usage := model.ParseLegacyUsage(r.Usage, model.UsageSourceLegacyState)
		if usage.HasUnknown() {
			p.report.UnknownUsage++
		}
		if historyTask[r.TaskID] {
			h := HistoryEntry{ID: "run:" + r.ID, Kind: "legacy_run", TaskID: r.TaskID, RunID: r.ID, Role: r.Role, Executor: "pi", State: r.State,
				StartedAt: r.StartedAt, EndedAt: r.EndedAt, WallSeconds: wallOf(r.StartedAt, r.EndedAt), Usage: usage,
				Source: model.UsageSourceLegacyState, SourceHash: srcHash, CreatedAt: now()}
			if r.ModelSnapshot != nil {
				h.Provider, h.Model = r.ModelSnapshot.Provider, r.ModelSnapshot.Model
			}
			if model.IsActiveRunState(r.State) {
				h.State = model.RunUnknown
				p.report.UnknownRuns++
			}
			p.history = append(p.history, h)
			continue
		}
		nr := model.Run{ID: r.ID, AgentID: r.AgentID, TaskID: r.TaskID, Role: r.Role, ProfileID: r.ProfileID, Executor: "pi",
			ModelSnapshot: r.ModelSnapshot, ContextRef: r.ContextRef, State: r.State, PID: r.PID, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
			UpdatedAt: r.UpdatedAt, Events: r.Events, Summary: r.Summary, Usage: usage, Origin: model.OriginLegacyImport,
			Metrics: &model.TimeMetrics{WallSeconds: wallOf(r.StartedAt, r.EndedAt)}}
		if nr.Events == nil {
			nr.Events = []model.RunEvent{}
		}
		if len(nr.Events) > model.MaxRunEvents {
			nr.Events = nr.Events[len(nr.Events)-model.MaxRunEvents:]
		}
		if model.IsActiveRunState(r.State) { // never adopt/kill/replay old processes
			nr.RecordedState, nr.State, nr.PID = r.State, model.RunUnknown, nil
			p.report.UnknownRuns++
		}
		st.Runs = append(st.Runs, nr)
	}
	seenD := map[string]bool{}
	for _, d := range ls.Deliveries {
		if d.ID == "" || seenD[d.ID] || tasks[d.TaskID] == nil || historyTask[d.TaskID] || !contains(model.DeliveryStates, d.State) {
			return corrupt("invalid delivery relationship")
		}
		seenD[d.ID] = true
		if c, ok := ctx[ck{d.ContextRef.ID, d.ContextRef.Version}]; !ok || c.Digest != d.ContextRef.Digest {
			return corrupt("delivery context reference mismatch")
		}
		for _, rid := range d.RunIDs {
			if r, ok := runs[rid]; !ok || r.TaskID != d.TaskID {
				return corrupt("delivery run reference mismatch")
			}
		}
		if err := freeForm("delivery", d.Checks); err != nil {
			return err
		}
		if err := model.CheckStrings("delivery", d.KnownGaps...); err != nil {
			return err
		}
		st.Deliveries = append(st.Deliveries, d)
	}
	seenR := map[string]bool{}
	for _, rv := range ls.Reviews {
		r, ok := runs[rv.RunID]
		t := tasks[rv.TaskID]
		if rv.ID == "" || seenR[rv.ID] || t == nil || historyTask[rv.TaskID] || !ok || r.TaskID != rv.TaskID {
			return corrupt("invalid review relationship")
		}
		seenR[rv.ID] = true
		match := false
		for k, c := range ctx {
			if k.id == t.ContextRef.ID && c.Digest == rv.ContextDigest {
				match = true
			}
		}
		if !match {
			return corrupt("review context digest mismatch")
		}
		if err := freeForm("review", rv.Findings, rv.Checks); err != nil {
			return err
		}
		st.Reviews = append(st.Reviews, rv)
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var (
	stopFileRE    = regexp.MustCompile(`^stop-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
	receiptFileRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.json$`)
	runFileRE     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}\.json$`)
	dashFiles     = []string{"workspaces", "repositories", "agents", "tasks"}
)

func parseStop(b []byte, processed bool) (model.StopReceipt, error) {
	var r struct {
		Type        string  `json:"type"`
		RequestID   string  `json:"requestId"`
		RunID       string  `json:"runId"`
		CreatedAt   string  `json:"createdAt"`
		ProcessedAt *string `json:"processedAt"`
		Outcome     *string `json:"outcome"`
	}
	if decodeStrict(b, &r) != nil || !uuidRE.MatchString(r.RequestID) || !uuidRE.MatchString(r.RunID) || r.CreatedAt == "" {
		return model.StopReceipt{}, corrupt("stop request is malformed")
	}
	out := model.StopReceipt{RequestID: strings.ToLower(r.RequestID), RunID: r.RunID, State: model.StopProcessed, CreatedAt: r.CreatedAt,
		ProcessedAt: r.ProcessedAt, Outcome: r.Outcome, Accepted: true}
	if !processed || out.Outcome == nil { // never replay old stops: outcome unknown
		u := model.RunUnknown
		out.Outcome = &u
	}
	if out.ProcessedAt == nil {
		t := now()
		out.ProcessedAt = &t
	}
	return out, nil
}

func normalizeLegacyReceipt(b, body []byte) (IssueReceipt, error) {
	var r legacyIssueReceipt
	if decodeStrict(b, &r) != nil || r.SchemaVersion != 1 || !uuidRE.MatchString(r.DeliveryID) || !uuidRE.MatchString(r.TaskID) {
		return IssueReceipt{}, corrupt("issue receipt is malformed")
	}
	if !contains([]string{IssuePending, IssuePosting, IssueUnknown, IssuePosted}, r.State) || !strings.HasPrefix(r.BodyHash, "sha256:") {
		return IssueReceipt{}, corrupt("issue receipt is malformed")
	}
	if body == nil && r.State != IssuePosted {
		return IssueReceipt{}, corrupt("pending issue body is missing")
	}
	if body != nil && hashBody(body) != r.BodyHash {
		return IssueReceipt{}, corrupt("issue body hash mismatch")
	}
	state := r.State
	if state == IssuePosting { // outcome of an interrupted POST is unknown; reconcile before retry
		state = IssueUnknown
	}
	if err := model.CheckStrings("issue receipt", r.CommentURL, r.LastError); err != nil {
		return IssueReceipt{}, err
	}
	return IssueReceipt{SchemaVersion: 1, DeliveryID: r.DeliveryID, TaskID: r.TaskID, IssueURL: r.IssueURL, BodyHash: r.BodyHash,
		State: state, Attempts: r.Attempts, PreparedAt: r.PreparedAt, UpdatedAt: r.UpdatedAt, PostedAt: r.PostedAt, CommentURL: r.CommentURL,
		LastError: r.LastError, FoundExisting: r.FoundExisting, Marker: DeliveryMarker(r.DeliveryID), Origin: model.OriginLegacyImport}, nil
}

func parseRunReceipt(b []byte, rel, srcHash string) (HistoryEntry, string, error) {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil || top == nil {
		return HistoryEntry{}, "", corrupt("run receipt is malformed")
	}
	var anyv any
	_ = json.Unmarshal(b, &anyv)
	if err := model.CheckFreeForm(anyv, "run receipt"); err != nil {
		return HistoryEntry{}, "", err
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(top[k], &s)
		return s
	}
	h := HistoryEntry{Kind: "standalone_run", TaskID: str("taskId"), RunID: str("runId"), ProjectID: str("projectId"), Role: str("role"),
		Executor: "pi", StartedAt: str("startedAt"), State: str("outcome"), Source: model.UsageSourceLegacyRecipt, SourceHash: srcHash, CreatedAt: now()}
	if prov, mod, ok := strings.Cut(str("model"), "/"); ok {
		h.Provider, h.Model = prov, mod
	} else {
		h.Model = prov
	}
	if e := str("endedAt"); e != "" {
		h.EndedAt = &e
	}
	h.WallSeconds = wallOf(h.StartedAt, h.EndedAt)
	h.Usage = model.ParseFlatLegacyUsage(top, model.UsageSourceLegacyRecipt)
	if h.RunID != "" && !uuidRE.MatchString(h.RunID) {
		return HistoryEntry{}, "", corrupt("run receipt id is malformed")
	}
	sum := sha256.Sum256(b)
	h.ID = "receipt:" + hex.EncodeToString(sum[:16])
	if h.RunID != "" {
		h.ID = "receipt:" + h.RunID
	}
	return h, h.RunID, nil
}

// ImportLegacy imports a 0.2.x data directory (and explicit run roots) into this store in one transaction.
func (s *Store) ImportLegacy(from string, runRoots []string) (ImportReport, error) {
	var zero ImportReport
	if !filepath.IsAbs(from) {
		return zero, fmt.Errorf("%w: source must be absolute", ErrUnsafeSource)
	}
	from = filepath.Clean(from)
	if from == s.dir || strings.HasPrefix(s.dir, from+string(os.PathSeparator)) || strings.HasPrefix(from, s.dir+string(os.PathSeparator)) {
		return zero, fmt.Errorf("%w: source overlaps destination", ErrUnsafeSource)
	}
	if ok, err := safeDir(from); err != nil || !ok {
		return zero, fmt.Errorf("%w: source directory", ErrUnsafeSource)
	}
	wf := filepath.Join(from, "workflow")
	if _, err := safeDir(wf); err != nil {
		return zero, err
	}
	if live, err := liveOwner(filepath.Join(wf, "controller.lock", "owner.json")); err != nil || live {
		return zero, fmt.Errorf("%w: source controller", ErrLiveController)
	}
	if f, err := s.LeaseFacts(); err != nil || (f.Present && !f.Stale) {
		return zero, fmt.Errorf("%w: destination controller", ErrLiveController)
	}
	ss := &sourceSet{}
	p := &importPlan{state: model.EmptyState(), bodies: map[string][]byte{}}
	p.report.Imported, p.report.Duplicates, p.report.Conflicts = map[string]int{}, map[string]int{}, []ImportConflict{}

	stateB, err := readSafe(filepath.Join(wf, "state.json"), MaxLegacyStateBytes)
	if err != nil {
		return zero, err
	}
	if stateB != nil {
		_ = ss.add("workflow/state.json", stateB)
	}
	setB, err := readSafe(filepath.Join(wf, "settings.json"), MaxLegacySmallBytes)
	if err != nil {
		return zero, err
	}
	if setB != nil {
		_ = ss.add("workflow/settings.json", setB)
	}
	stopFiles := map[string][]byte{}
	for _, sub := range []string{"requests", "requests/processed"} {
		names, err := listDir(filepath.Join(wf, sub), stopFileRE)
		if err != nil {
			return zero, err
		}
		for _, n := range names {
			b, err := readSafe(filepath.Join(wf, sub, n), 4096)
			if err != nil {
				return zero, err
			}
			if err := ss.add("workflow/"+sub+"/"+n, b); err != nil {
				return zero, err
			}
			stopFiles[sub+"/"+n] = b
		}
	}
	isync := filepath.Join(from, "issue-sync")
	rnames, err := listDir(isync, receiptFileRE)
	if err != nil {
		return zero, err
	}
	receiptRaw := map[string][2][]byte{}
	for _, n := range rnames {
		b, err := readSafe(filepath.Join(isync, n), MaxLegacySmallBytes)
		if err != nil {
			return zero, err
		}
		body, err := readSafe(filepath.Join(isync, strings.TrimSuffix(n, ".json")+".md"), MaxLegacyBodyBytes)
		if err != nil {
			return zero, err
		}
		_ = ss.add("issue-sync/"+n, b)
		if body != nil {
			_ = ss.add("issue-sync/"+n+".md", body)
		}
		receiptRaw[n] = [2][]byte{b, body}
	}
	dash := map[string][]byte{}
	for _, n := range dashFiles {
		b, err := readSafe(filepath.Join(from, n+".json"), MaxLegacyStateBytes)
		if err != nil {
			return zero, err
		}
		if b != nil {
			_ = ss.add(n+".json", b)
			dash[n] = b
		}
	}
	type runFile struct {
		rel  string
		data []byte
	}
	var runFiles []runFile
	for i, root := range runRoots {
		if !filepath.IsAbs(root) {
			return zero, fmt.Errorf("%w: run root must be absolute", ErrUnsafeSource)
		}
		root = filepath.Clean(root)
		if ok, err := safeDir(root); err != nil || !ok {
			return zero, fmt.Errorf("%w: run root", ErrUnsafeSource)
		}
		if _, err := safeDir(filepath.Join(root, ".pi-developer")); err != nil {
			return zero, err
		}
		dir := filepath.Join(root, ".pi-developer", "runs")
		names, err := listDir(dir, runFileRE)
		if err != nil {
			return zero, err
		}
		for _, n := range names {
			b, err := readSafe(filepath.Join(dir, n), MaxLegacySmallBytes*4)
			if err != nil {
				return zero, err
			}
			rel := "root" + strconv.Itoa(i) + "/" + n
			if err := ss.add(rel, b); err != nil {
				return zero, err
			}
			runFiles = append(runFiles, runFile{rel, b})
		}
	}
	srcHash := ss.hash()
	p.report.SourceHash = "sha256:" + srcHash
	p.report.ImportID = srcHash[:32]

	// Repeat of an identical source: idempotent no-op.
	var prev []byte
	if err := s.db.QueryRow("SELECT payload FROM imports WHERE id = ?", p.report.ImportID).Scan(&prev); err == nil {
		var rep ImportReport
		if json.Unmarshal(prev, &rep) != nil {
			return zero, fmt.Errorf("store: corrupt record")
		}
		rep.Repeat = true
		return rep, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return zero, fmt.Errorf("store: read failed")
	}

	if stateB != nil {
		var raw any
		if json.Unmarshal(stateB, &raw) != nil {
			return zero, corrupt("state is not valid JSON")
		}
		var ls legacyState
		if err := decodeStrict(stateB, &ls); err != nil {
			return zero, corrupt("state does not match the 0.2.x schema")
		}
		if err := buildState(&ls, p, p.report.SourceHash); err != nil {
			return zero, err
		}
	}
	if setB != nil {
		set := model.DefaultSettings()
		if decodeStrict(setB, &set) != nil || set.MaxConcurrency < model.MinConcurrency || set.MaxConcurrency > model.MaxConcurrency ||
			set.MaxFixRounds < 0 || set.MaxFixRounds > model.MaxFixRoundsLimit {
			return zero, corrupt("settings are malformed")
		}
		var anyv any
		_ = json.Unmarshal(setB, &anyv)
		if err := model.CheckFreeForm(anyv, "settings"); err != nil {
			return zero, err
		}
		if set.DefaultProfiles == nil {
			set.DefaultProfiles = map[string]map[string]string{}
		}
		p.settings = &set
	}
	for k, b := range stopFiles {
		rc, err := parseStop(b, strings.HasPrefix(k, "requests/processed/"))
		if err != nil {
			return zero, err
		}
		p.stops = append(p.stops, rc)
	}
	sort.Slice(p.stops, func(i, j int) bool { return p.stops[i].RequestID < p.stops[j].RequestID })
	for _, n := range rnames {
		raw := receiptRaw[n]
		rc, err := normalizeLegacyReceipt(raw[0], raw[1])
		if err != nil {
			return zero, err
		}
		if rc.DeliveryID+".json" != n {
			return zero, corrupt("issue receipt name mismatch")
		}
		p.receipts = append(p.receipts, rc)
		if raw[1] != nil {
			p.bodies[rc.DeliveryID] = raw[1]
		}
	}
	for _, n := range dashFiles {
		b, ok := dash[n]
		if !ok {
			continue
		}
		var rows []map[string]any
		if json.Unmarshal(b, &rows) != nil {
			return zero, corrupt("dashboard collection is malformed")
		}
		for _, row := range rows {
			if err := model.CheckFreeForm(row, "dashboard"); err != nil {
				return zero, err
			}
			id, _ := row["id"].(string)
			if !uuidRE.MatchString(id) {
				return zero, corrupt("dashboard record id is malformed")
			}
			title, _ := row["title"].(string)
			if title == "" {
				title, _ = row["name"].(string)
			}
			stage, _ := row["stage"].(string)
			p.history = append(p.history, HistoryEntry{ID: "dashboard:" + n + ":" + id, Kind: "dashboard_" + strings.TrimSuffix(n, "s"),
				Title: trunc(title, 300), Source: "legacy_dashboard", SourceHash: p.report.SourceHash, Extra: &HistoryExtra{Collection: n, Stage: trunc(stage, 40)}, CreatedAt: now()})
		}
	}
	workflowRuns := map[string]bool{}
	for _, r := range p.state.Runs {
		workflowRuns[r.ID] = true
	}
	for _, h := range p.history {
		if h.RunID != "" {
			workflowRuns[h.RunID] = true
		}
	}
	for _, rf := range runFiles {
		h, runID, err := parseRunReceipt(rf.data, rf.rel, p.report.SourceHash)
		if err != nil {
			return zero, err
		}
		if runID != "" && workflowRuns[runID] { // already counted via workflow state
			p.report.Duplicates["standaloneLinked"]++
			continue
		}
		if h.Usage.HasUnknown() {
			p.report.UnknownUsage++
		}
		p.history = append(p.history, h)
	}
	return s.commitImport(p)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Store) bodyDir() string { return filepath.Join(s.dir, "issue-bodies") }

func (s *Store) commitImport(p *importPlan) (ImportReport, error) {
	rep := p.report
	// Stage attachments outside the SQL transaction.
	staged := map[string]string{}
	cleanup := func() {
		for _, tmp := range staged {
			os.Remove(tmp)
		}
	}
	for id, body := range p.bodies {
		tmp, err := s.stageBody(id, body)
		if err != nil {
			cleanup()
			return ImportReport{}, err
		}
		staged[id] = tmp
	}
	err := s.tx(func(tx *sql.Tx) error {
		cur, err := readState(tx)
		if err != nil {
			return err
		}
		conflict := func(kind, id string) { rep.Conflicts = append(rep.Conflicts, ImportConflict{kind, id}) }
		mergeAll(cur, p.state, &rep, conflict)
		for _, h := range p.history {
			var old []byte
			e := tx.QueryRow("SELECT payload FROM legacy_history WHERE id = ?", h.ID).Scan(&old)
			if e == nil {
				var oh HistoryEntry
				if json.Unmarshal(old, &oh) != nil || !sameHistory(oh, h) {
					conflict("history", h.ID)
				} else {
					rep.Duplicates["history"]++
				}
				continue
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return fmt.Errorf("store: read failed")
			}
			if _, err := tx.Exec("INSERT INTO legacy_history (id, created_at, payload) VALUES (?, ?, ?)", h.ID, h.CreatedAt, mustJSON(h)); err != nil {
				return fmt.Errorf("store: write failed")
			}
			rep.Imported["history"]++
		}
		for _, sr := range p.stops {
			var run string
			e := tx.QueryRow("SELECT run_id FROM stop_receipts WHERE request_id = ?", sr.RequestID).Scan(&run)
			if e == nil {
				if run != sr.RunID {
					conflict("stopReceipt", sr.RequestID)
				} else {
					rep.Duplicates["stopReceipts"]++
				}
				continue
			}
			if _, err := tx.Exec("INSERT INTO stop_receipts (request_id, run_id, state, created_at, processed_at, outcome) VALUES (?, ?, ?, ?, ?, ?)",
				sr.RequestID, sr.RunID, sr.State, sr.CreatedAt, sr.ProcessedAt, sr.Outcome); err != nil {
				return fmt.Errorf("store: write failed")
			}
			rep.Imported["stopReceipts"]++
		}
		for _, rc := range p.receipts {
			if _, ok := p.bodies[rc.DeliveryID]; ok {
				rc.BodyPath = s.bodyPath(rc.DeliveryID)
			}
			old, e := loadReceipt(tx, rc.DeliveryID)
			if e == nil {
				if old.TaskID != rc.TaskID || old.BodyHash != rc.BodyHash {
					conflict("issueReceipt", rc.DeliveryID)
				} else {
					rep.Duplicates["issueReceipts"]++ // keep the newer local state; never regress posted
				}
				continue
			}
			if !errors.Is(e, ErrNotFound) {
				return e
			}
			rc.Rev = 1
			if _, err := tx.Exec("INSERT INTO issue_receipts (id, created_at, payload) VALUES (?, ?, ?)", rc.DeliveryID, now(), mustJSON(rc)); err != nil {
				return fmt.Errorf("store: write failed")
			}
			rep.Imported["issueReceipts"]++
		}
		if len(rep.Conflicts) > 0 {
			return fmt.Errorf("%w: legacy import has %d divergent records", ErrConflict, len(rep.Conflicts))
		}
		if p.settings != nil {
			var old []byte
			e := tx.QueryRow("SELECT payload FROM settings WHERE id = 1").Scan(&old)
			if errors.Is(e, sql.ErrNoRows) {
				if _, err := tx.Exec("INSERT INTO settings (id, payload) VALUES (1, ?)", mustJSON(p.settings)); err != nil {
					return fmt.Errorf("store: write failed")
				}
				rep.Imported["settings"]++
			} else if e != nil {
				return fmt.Errorf("store: read failed")
			} else if string(old) != mustJSON(p.settings) {
				rep.SettingsKept = true
			}
		}
		if err := writeState(tx, cur); err != nil {
			return err
		}
		rep.CreatedAt = now()
		_, err = tx.Exec("INSERT INTO imports (id, created_at, payload) VALUES (?, ?, ?)", rep.ImportID, rep.CreatedAt, mustJSON(rep))
		if err != nil {
			return fmt.Errorf("store: write failed")
		}
		return nil
	})
	if err != nil {
		cleanup()
		if errors.Is(err, ErrConflict) {
			return rep, err
		}
		return ImportReport{}, err
	}
	for id, tmp := range staged {
		if err := s.publishBody(id, tmp); err != nil {
			cleanup()
			return rep, err
		}
	}
	return rep, nil
}

func sameHistory(a, b HistoryEntry) bool {
	a.CreatedAt, b.CreatedAt, a.SourceHash, b.SourceHash = "", "", "", ""
	return mustJSON(a) == mustJSON(b)
}

func canon(v any) string { return mustJSON(v) }

// mergeAll merges incoming into cur by immutable ID: identical payloads are no-ops, divergent ones conflicts.
func mergeAll(cur, in *model.State, rep *ImportReport, conflict func(string, string)) {
	merge := func(kind string, n int, id func(int) string, find func(string) (string, bool), add func(int), payload func(int) string) {
		for i := 0; i < n; i++ {
			old, ok := find(id(i))
			switch {
			case !ok:
				add(i)
				rep.Imported[kind]++
			case old == payload(i):
				rep.Duplicates[kind]++
			default:
				conflict(kind, id(i))
			}
		}
	}
	idx := func(n int, id func(int) string, pay func(int) string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i < n; i++ {
			m[id(i)] = pay(i)
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	ctxKey := func(c model.Context) string { return c.ID + "@" + strconv.Itoa(c.Version) }
	merge("projects", len(in.Projects), func(i int) string { return in.Projects[i].ID },
		idx(len(cur.Projects), func(i int) string { return cur.Projects[i].ID }, func(i int) string { return canon(cur.Projects[i]) }),
		func(i int) { cur.Projects = append(cur.Projects, in.Projects[i]) }, func(i int) string { return canon(in.Projects[i]) })
	merge("contexts", len(in.Contexts), func(i int) string { return ctxKey(in.Contexts[i]) },
		idx(len(cur.Contexts), func(i int) string { return ctxKey(cur.Contexts[i]) }, func(i int) string { return canon(cur.Contexts[i]) }),
		func(i int) { cur.Contexts = append(cur.Contexts, in.Contexts[i]) }, func(i int) string { return canon(in.Contexts[i]) })
	merge("profiles", len(in.Profiles), func(i int) string { return in.Profiles[i].ID },
		idx(len(cur.Profiles), func(i int) string { return cur.Profiles[i].ID }, func(i int) string { return canon(cur.Profiles[i]) }),
		func(i int) { cur.Profiles = append(cur.Profiles, in.Profiles[i]) }, func(i int) string { return canon(in.Profiles[i]) })
	merge("tasks", len(in.Tasks), func(i int) string { return in.Tasks[i].ID },
		idx(len(cur.Tasks), func(i int) string { return cur.Tasks[i].ID }, func(i int) string { return canon(cur.Tasks[i]) }),
		func(i int) { cur.Tasks = append(cur.Tasks, in.Tasks[i]) }, func(i int) string { return canon(in.Tasks[i]) })
	merge("runs", len(in.Runs), func(i int) string { return in.Runs[i].ID },
		idx(len(cur.Runs), func(i int) string { return cur.Runs[i].ID }, func(i int) string { return canon(cur.Runs[i]) }),
		func(i int) { cur.Runs = append(cur.Runs, in.Runs[i]) }, func(i int) string { return canon(in.Runs[i]) })
	merge("deliveries", len(in.Deliveries), func(i int) string { return in.Deliveries[i].ID },
		idx(len(cur.Deliveries), func(i int) string { return cur.Deliveries[i].ID }, func(i int) string { return canon(cur.Deliveries[i]) }),
		func(i int) { cur.Deliveries = append(cur.Deliveries, in.Deliveries[i]) }, func(i int) string { return canon(in.Deliveries[i]) })
	merge("reviews", len(in.Reviews), func(i int) string { return in.Reviews[i].ID },
		idx(len(cur.Reviews), func(i int) string { return cur.Reviews[i].ID }, func(i int) string { return canon(cur.Reviews[i]) }),
		func(i int) { cur.Reviews = append(cur.Reviews, in.Reviews[i]) }, func(i int) string { return canon(in.Reviews[i]) })
}

// ---------- history ----------

// AddHistory inserts an immutable non-executable history entry; an identical repeat is a no-op.
func (s *Store) AddHistory(h HistoryEntry) error {
	if h.ID == "" || len(h.ID) > 200 || h.Kind == "" {
		return model.Invalidf("history id and kind are required")
	}
	if err := model.CheckStrings("history", h.Title, h.Model, h.Provider, h.State); err != nil {
		return err
	}
	if err := h.Usage.Validate(); err != nil {
		return err
	}
	h.Executable = false
	if h.CreatedAt == "" {
		h.CreatedAt = now()
	}
	return s.tx(func(tx *sql.Tx) error {
		var old []byte
		e := tx.QueryRow("SELECT payload FROM legacy_history WHERE id = ?", h.ID).Scan(&old)
		if e == nil {
			var oh HistoryEntry
			if json.Unmarshal(old, &oh) != nil || !sameHistory(oh, h) {
				return fmt.Errorf("%w: history entry is immutable", ErrConflict)
			}
			return nil
		}
		if _, err := tx.Exec("INSERT INTO legacy_history (id, created_at, payload) VALUES (?, ?, ?)", h.ID, h.CreatedAt, mustJSON(h)); err != nil {
			return fmt.Errorf("store: write failed")
		}
		return nil
	})
}

// History lists imported/non-executable history entries oldest first (SourceHash omitted).
func (s *Store) History() ([]HistoryEntry, error) {
	out := []HistoryEntry{}
	if err := scanPayloads(s.db, "SELECT payload FROM legacy_history ORDER BY created_at, rowid", into(&out)); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SourceHash = ""
	}
	return out, nil
}

// Imports lists sanitized import reports.
func (s *Store) Imports() ([]ImportReport, error) {
	out := []ImportReport{}
	return out, scanPayloads(s.db, "SELECT payload FROM imports ORDER BY created_at, rowid", into(&out))
}

// ---------- metrics export ----------

// MetricsRow is one exported usage row; no context, auth env, command or transcript.
type MetricsRow struct {
	Source       string   `json:"source"`
	Origin       string   `json:"origin"`
	TaskID       string   `json:"taskId"`
	RunID        string   `json:"runId"`
	Role         string   `json:"role"`
	Executor     string   `json:"executor"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	ChangeID     *string  `json:"changeId"`
	State        string   `json:"state"`
	StartedAt    string   `json:"startedAt"`
	EndedAt      *string  `json:"endedAt"`
	WallSeconds  *float64 `json:"wallSeconds"`
	ModelSeconds *float64 `json:"modelSeconds"`
	Input        *int64   `json:"inputTokens"`
	Output       *int64   `json:"outputTokens"`
	CacheRead    *int64   `json:"cacheReadTokens"`
	CacheWrite   *int64   `json:"cacheWriteTokens"`
	Total        *int64   `json:"totalTokens"`
	Completeness string   `json:"usageCompleteness"`
	CostUsd      *float64 `json:"estimatedCostUsd"`
	UsageSource  string   `json:"usageSource"`
}

func fillUsage(r *MetricsRow, u *model.Usage) {
	r.Completeness, r.UsageSource = model.UsageUnknown, model.UsageSourceUnknown
	if u == nil {
		return
	}
	r.Input, r.Output, r.CacheRead, r.CacheWrite, r.Total = u.Tokens.Input, u.Tokens.Output, u.Tokens.CacheRead, u.Tokens.CacheWrite, u.Tokens.Total
	r.Completeness, r.CostUsd = u.UsageCompleteness, u.EstimatedCostUsd
	if u.Source != "" {
		r.UsageSource = u.Source
	}
}

// MetricsRows returns workflow runs plus non-duplicated legacy history runs.
func (s *Store) MetricsRows() ([]MetricsRow, error) {
	st, err := s.Read()
	if err != nil {
		return nil, err
	}
	hist, err := s.History()
	if err != nil {
		return nil, err
	}
	tasks := map[string]model.Task{}
	for _, t := range st.Tasks {
		tasks[t.ID] = t
	}
	seen := map[string]bool{}
	out := []MetricsRow{}
	for _, r := range st.Runs {
		seen[r.ID] = true
		row := MetricsRow{Source: "workflow", Origin: r.Origin, TaskID: r.TaskID, RunID: r.ID, Role: r.Role, Executor: r.Executor,
			State: r.State, StartedAt: r.StartedAt, EndedAt: r.EndedAt, ChangeID: tasks[r.TaskID].ChangeID}
		if r.ModelSnapshot != nil {
			row.Provider, row.Model = r.ModelSnapshot.Provider, r.ModelSnapshot.Model
		}
		if r.Metrics != nil {
			row.WallSeconds, row.ModelSeconds = r.Metrics.WallSeconds, r.Metrics.ModelSeconds
		}
		fillUsage(&row, r.Usage)
		out = append(out, row)
	}
	for _, h := range hist {
		if h.RunID == "" || seen[h.RunID] || (h.Kind != "standalone_run" && h.Kind != "legacy_run") {
			continue
		}
		seen[h.RunID] = true
		row := MetricsRow{Source: h.Kind, Origin: model.OriginLegacyImport, TaskID: h.TaskID, RunID: h.RunID, Role: h.Role, Executor: h.Executor,
			Provider: h.Provider, Model: h.Model, ChangeID: h.ChangeID, State: h.State, StartedAt: h.StartedAt, EndedAt: h.EndedAt, WallSeconds: h.WallSeconds}
		fillUsage(&row, h.Usage)
		out = append(out, row)
	}
	return out, nil
}

// ExportMetrics renders MetricsRows as "json" or "csv". Unknown values are null (JSON) or empty (CSV).
func (s *Store) ExportMetrics(format string) ([]byte, error) {
	rows, err := s.MetricsRows()
	if err != nil {
		return nil, err
	}
	switch format {
	case "json":
		return json.MarshalIndent(rows, "", "  ")
	case "csv":
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"source", "origin", "taskId", "runId", "role", "executor", "provider", "model", "changeId", "state", "startedAt", "endedAt",
			"wallSeconds", "modelSeconds", "inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "totalTokens", "usageCompleteness", "estimatedCostUsd", "usageSource"})
		ps := func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		}
		pi := func(p *int64) string {
			if p == nil {
				return ""
			}
			return strconv.FormatInt(*p, 10)
		}
		pf := func(p *float64) string {
			if p == nil {
				return ""
			}
			return strconv.FormatFloat(*p, 'f', -1, 64)
		}
		for _, r := range rows {
			_ = w.Write([]string{r.Source, r.Origin, r.TaskID, r.RunID, r.Role, r.Executor, r.Provider, r.Model, ps(r.ChangeID), r.State, r.StartedAt, ps(r.EndedAt),
				pf(r.WallSeconds), pf(r.ModelSeconds), pi(r.Input), pi(r.Output), pi(r.CacheRead), pi(r.CacheWrite), pi(r.Total), r.Completeness, pf(r.CostUsd), r.UsageSource})
		}
		w.Flush()
		return buf.Bytes(), w.Error()
	default:
		return nil, model.Invalidf("metrics format must be json or csv")
	}
}
