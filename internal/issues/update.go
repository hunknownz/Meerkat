package issues

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

// ClaimTTL bounds how long a posting claim blocks other callers (longer than any bounded gh call).
const ClaimTTL = 10 * time.Minute

// Updater prepares local delivery updates and applies them only on explicit request.
type Updater struct {
	Store  *store.Store
	Client *Client
}

// Result describes a prepare/apply outcome (no body text).
type Result struct {
	TaskID     string  `json:"taskId"`
	DeliveryID string  `json:"deliveryId"`
	IssueURL   *string `json:"issueUrl"`
	BodyFile   string  `json:"bodyFile"`
	BodyHash   string  `json:"bodyHash"`
	State      string  `json:"state"`
	Created    bool    `json:"created"`
	LocalOnly  bool    `json:"localOnly"`
	Applied    bool    `json:"applied"`
	Duplicate  bool    `json:"duplicate"`
	Retryable  bool    `json:"retryable"`
	Error      string  `json:"error,omitempty"`
	CommentURL string  `json:"commentUrl,omitempty"`
	Attempts   int     `json:"attempts"`
}

func view(r store.IssueReceipt, created bool) Result {
	return Result{TaskID: r.TaskID, DeliveryID: r.DeliveryID, IssueURL: r.IssueURL, BodyFile: r.BodyPath, BodyHash: r.BodyHash,
		State: r.State, Created: created, LocalOnly: r.IssueURL == nil, CommentURL: r.CommentURL, Attempts: r.Attempts}
}

func pickDelivery(st *model.State, taskID string) *model.Delivery {
	for _, want := range []string{"delivered", "final_candidate", "first"} {
		for i := len(st.Deliveries) - 1; i >= 0; i-- {
			if d := st.Deliveries[i]; d.TaskID == taskID && d.State == want {
				return &st.Deliveries[i]
			}
		}
	}
	return nil
}

// SafeText neutralizes free text for a public comment (no control chars, HTML comments, mentions or backticks).
func SafeText(s string, max int) string {
	s = oneLine(s, max*2)
	s = strings.NewReplacer("<!--", "", "-->", "", "`", "'").Replace(s)
	var b strings.Builder
	for i, r := range s {
		b.WriteRune(r)
		if r == '@' && i+1 < len(s) {
			b.WriteString("\u200b")
		}
	}
	return truncRunes(b.String(), max)
}

func fmtInt(p *int64) string {
	if p == nil {
		return "unknown"
	}
	return fmt.Sprint(*p)
}

// BuildUpdate renders the deterministic, secret-free Markdown for one delivery.
func BuildUpdate(st *model.State, t model.Task, d model.Delivery) string {
	var l []string
	add := func(s ...string) { l = append(l, s...) }
	add(store.DeliveryMarker(d.ID), "## Meerkat delivery update: "+SafeText(t.Title, 200), "",
		"**Goal:** "+SafeText(t.Goal, 1000), "",
		fmt.Sprintf("**Context:** frozen version %d (`%s`); context text is kept locally and not posted.", d.ContextRef.Version, SafeText(d.ContextRef.Digest, 80)), "",
		fmt.Sprintf("**Candidate:** `%s` (delivery `%s`, state `%s`; task state `%s`)", SafeText(d.CandidateSha, 64), d.ID, SafeText(d.State, 30), SafeText(t.State, 30)), "",
		"### Roles and models")
	var runs []model.Run
	for _, r := range st.Runs {
		if r.TaskID == t.ID {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		add("- No runs recorded.")
	}
	for _, r := range runs {
		prov, mod := "unknown", "unknown"
		if r.ModelSnapshot != nil {
			prov, mod = r.ModelSnapshot.Provider, r.ModelSnapshot.Model
		}
		add(fmt.Sprintf("- %s: %s/%s - %s", SafeText(r.Role, 20), SafeText(prov, 40), SafeText(mod, 80), SafeText(r.State, 20)))
	}
	add("", "### Reviews")
	n := 0
	for _, rv := range st.Reviews {
		if rv.TaskID == t.ID {
			n++
			add(fmt.Sprintf("- Review on `%s`: **%s**", SafeText(rv.CandidateSha, 12), SafeText(rv.Verdict, 30)))
		}
	}
	if n == 0 {
		add("- No review recorded.")
	}
	add("", "### Checks")
	var checks []map[string]any
	_ = json.Unmarshal(d.Checks, &checks)
	if len(checks) == 0 {
		add("- None recorded.")
	}
	for i, c := range checks {
		if i >= 40 {
			break
		}
		s := func(k string) string { v, _ := c[k].(string); return v }
		if s("status") == "reported" {
			add(fmt.Sprintf("- reported by agent (not independently verified): `%s` -> %s", SafeText(s("command"), 200), SafeText(s("result"), 200)))
		} else {
			add(fmt.Sprintf("- %s: %s (verified by controller)", SafeText(s("name"), 60), SafeText(s("status"), 20)))
		}
	}
	add("", "### Known gaps")
	if len(d.KnownGaps) == 0 {
		add("- None reported.")
	}
	for i, g := range d.KnownGaps {
		if i < 20 {
			add("- " + SafeText(g, 300))
		}
	}
	agg := model.Aggregate(runs)
	cost := "unknown"
	if agg.EstimatedCostUsd != nil {
		cost = fmt.Sprintf("$%.4f", *agg.EstimatedCostUsd)
	}
	add("", "### Usage", "- Usage completeness: "+agg.Completeness,
		fmt.Sprintf("- Tokens: total %s (known subtotal %d; input %s, output %s)", fmtInt(agg.Tokens.Total), agg.KnownSubtotal, fmtInt(agg.Tokens.Input), fmtInt(agg.Tokens.Output)),
		"- Estimated cost: "+cost, "", "### Release and human status",
		"- Local commit only: not pushed, merged, deployed or released by Meerkat.",
		"- Independent QA, human review and acceptance: pending (not implied by this update).", "")
	text := strings.Join(l, "\n")
	if len(text) > store.MaxIssueBodyBytes-64 {
		text = text[:store.MaxIssueBodyBytes-64] + "\n\n(truncated)\n"
	}
	return text
}

// PrepareUpdate creates (or returns) the local pending update for the task's latest delivery. Never sends anything.
func (u *Updater) PrepareUpdate(taskID string) (Result, error) {
	st, err := u.Store.Read()
	if err != nil {
		return Result{}, err
	}
	var task *model.Task
	for i := range st.Tasks {
		if st.Tasks[i].ID == taskID {
			task = &st.Tasks[i]
		}
	}
	if task == nil {
		return Result{}, fail("task_not_found")
	}
	d := pickDelivery(st, taskID)
	if d == nil {
		return Result{}, fail("no_delivery")
	}
	if r, err := u.Store.LoadIssueReceipt(d.ID); err == nil {
		if r.TaskID != taskID {
			return Result{}, fail("corrupt_receipt")
		}
		if r.State != store.IssuePosted {
			if _, err := u.Store.ReadIssueBody(r); err != nil {
				return Result{}, fail("corrupt_receipt")
			}
		}
		return view(r, false), nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return Result{}, err
	}
	body := BuildUpdate(st, *task, *d)
	path, hash, err := u.Store.WriteIssueBody(d.ID, []byte(body))
	if err != nil {
		return Result{}, err
	}
	var issueURL *string
	if task.IssueRef != nil && task.IssueRef.URL != "" {
		v := task.IssueRef.URL
		issueURL = &v
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	r, created, err := u.Store.SaveIssueReceipt(store.IssueReceipt{DeliveryID: d.ID, TaskID: taskID, IssueURL: issueURL, BodyHash: hash,
		BodyPath: path, State: store.IssuePending, PreparedAt: ts, UpdatedAt: ts, Origin: model.OriginNative})
	if err != nil {
		return Result{}, err
	}
	return view(r, created), nil
}

func token() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hasMarker(body, marker string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

func commentURL(out, issueURL string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	v := strings.TrimSpace(lines[len(lines)-1])
	if strings.HasPrefix(strings.ToLower(v), strings.ToLower(issueURL)+"#issuecomment-") && len(v) <= 600 && !strings.ContainsAny(v, " \t") {
		return v
	}
	return ""
}

// ApplyUpdate explicitly posts the prepared update once. It claims the receipt, reconciles the exact marker through
// paginated comments, records posting intent before the POST, and treats an uncertain POST as unknown.
func (u *Updater) ApplyUpdate(ctx context.Context, taskID string) (Result, error) {
	prep, err := u.PrepareUpdate(taskID)
	if err != nil {
		return prep, err
	}
	if prep.State == store.IssuePosted {
		prep.Duplicate = true
		return prep, nil
	}
	if prep.IssueURL == nil {
		prep.LocalOnly = true
		return prep, nil
	}
	if u.Client == nil || u.Client.Runner == nil {
		return prep, fail("no_runner")
	}
	id, err := ParseURL(*prep.IssueURL, u.Client.AllowedHosts)
	if err != nil {
		return prep, err
	}
	r, err := u.Store.LoadIssueReceipt(prep.DeliveryID)
	if err != nil {
		return prep, err
	}
	if !r.ClaimStale(ClaimTTL) {
		return view(r, false), fail("busy")
	}
	if r.State == store.IssuePosting { // stale intent from a crashed caller: outcome unknown
		r.State = store.IssueUnknown
	}
	tok := token()
	r.ClaimToken, r.ClaimedAt = tok, time.Now().UTC().Format(time.RFC3339Nano)
	if r, err = u.Store.CompareAndSetIssueReceipt(r); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return prep, fail("busy")
		}
		return prep, err
	}
	save := func(mut func(*store.IssueReceipt)) error {
		mut(&r)
		var e error
		r, e = u.Store.CompareAndSetIssueReceipt(r)
		return e
	}
	release := func(res Result, cat string) (Result, error) {
		_ = save(func(x *store.IssueReceipt) { x.ClaimToken, x.ClaimedAt = "", "" })
		res = view(r, false)
		if cat != "" {
			res.Retryable, res.Error = true, cat
		}
		return res, nil
	}
	if _, err := u.Store.ReadIssueBody(r); err != nil {
		_, _ = release(Result{}, "")
		return prep, fail("body_changed")
	}
	// 1) Reconcile by exact marker.
	comments, complete, err := u.Client.ListComments(ctx, id)
	if err != nil {
		cat := Category(err)
		_ = save(func(x *store.IssueReceipt) { x.LastError = cat })
		return release(Result{}, cat)
	}
	for _, c := range comments {
		if hasMarker(c.Body, r.Marker) {
			cu := commentURL(c.HTMLURL, id.URL)
			if err := save(func(x *store.IssueReceipt) {
				x.State, x.FoundExisting, x.LastError = store.IssuePosted, true, ""
				if x.PostedAt == "" {
					x.PostedAt = time.Now().UTC().Format(time.RFC3339Nano)
				}
				if cu != "" {
					x.CommentURL = cu
				}
			}); err != nil {
				return prep, err
			}
			res, _ := release(Result{}, "")
			res.Duplicate = true
			return res, nil
		}
	}
	if r.State == store.IssueUnknown && !complete { // cannot confirm absence: never blindly repost
		_ = save(func(x *store.IssueReceipt) { x.LastError = "unconfirmed" })
		return release(Result{}, "unconfirmed")
	}
	// 2) Record intent, then POST once.
	if err := save(func(x *store.IssueReceipt) {
		x.State, x.Attempts, x.LastError = store.IssuePosting, x.Attempts+1, ""
		x.LastAttemptAt = time.Now().UTC().Format(time.RFC3339Nano)
	}); err != nil {
		return prep, err
	}
	out, err := u.Client.run(ctx, []string{"issue", "comment", id.URL, "--body-file", r.BodyPath})
	if err != nil {
		cat := Category(err)
		_ = save(func(x *store.IssueReceipt) {
			x.State, x.LastError = store.IssueUnknown, cat
			if cat == "gh_not_found" { // binary missing: definitely not sent
				x.State = store.IssuePending
			}
		})
		return release(Result{}, cat)
	}
	cu := commentURL(string(out), id.URL)
	if err := save(func(x *store.IssueReceipt) {
		x.State, x.PostedAt = store.IssuePosted, time.Now().UTC().Format(time.RFC3339Nano)
		if cu != "" {
			x.CommentURL = cu
		}
	}); err != nil {
		return prep, err
	}
	res, _ := release(Result{}, "")
	res.Applied = true
	return res, nil
}
