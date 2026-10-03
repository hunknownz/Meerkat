package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

const migrationV5 = `
CREATE TABLE budget_runs (run_id TEXT PRIMARY KEY REFERENCES runs(id) DEFERRABLE INITIALLY DEFERRED,
  task_id TEXT NOT NULL REFERENCES tasks(id) DEFERRABLE INITIALLY DEFERRED,
  session_id TEXT NOT NULL REFERENCES execution_sessions(id) DEFERRABLE INITIALLY DEFERRED,
  state TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX budget_runs_task ON budget_runs(task_id);
CREATE TABLE budget_requests (id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES budget_runs(run_id) DEFERRABLE INITIALLY DEFERRED,
  state TEXT NOT NULL, payload TEXT NOT NULL);
CREATE INDEX budget_requests_run ON budget_requests(run_id);
PRAGMA user_version = 5;
`

var budgetTables = []string{"budget_runs", "budget_requests"}

func validBudgetPolicy(p budget.Policy) bool {
	_, e := time.Parse(time.RFC3339Nano, p.Deadline)
	return uuidRE.MatchString(p.RunID) && uuidRE.MatchString(p.TaskID) && uuidRE.MatchString(p.SessionID) && p.ProfileID != "" &&
		digestRE.MatchString(p.ProfileDigest) && digestRE.MatchString(p.ContractDigest) && p.Provider != "" && p.Model != "" &&
		!model.LooksLikeCredential(p.Provider) && !model.LooksLikeCredential(p.Model) && len(p.Provider) <= 128 && len(p.Model) <= 256 &&
		p.Version == budget.PolicyVersion && p.TaskTokens > 0 && p.TaskTokens <= budget.MaxCount && p.RunTokens > 0 && p.RunTokens <= p.TaskTokens &&
		p.TaskRequests > 0 && p.TaskRequests <= budget.MaxRequests && p.WrapUpTokens >= 0 && p.WrapUpTokens < p.RunTokens && slices.Contains([]string{"open", "closed", "unknown"}, p.State) && e == nil
}

// RequestHeadroomOwned includes reservations and uncertain requests. It never
// substitutes a guessed token count for unknown settlement evidence.
func (s *Store) RequestHeadroomOwned(token, runID string) (int64, int64, error) {
	var remaining, threshold int64
	err := s.tx(func(tx *sql.Tx) error {
		p, err := loadBudgetPolicy(tx, runID)
		if err != nil {
			return err
		}
		st, err := verifyBudgetOwner(tx, token, p)
		if err != nil {
			return err
		}
		ps, rs, err := budgetRows(tx, p.TaskID)
		if err != nil {
			return err
		}
		taskLeft, runLeft, _, err := budgetRemaining(st, p, ps, rs)
		if errors.Is(err, budget.ErrDenied) {
			// Confirmed overrun is exhausted, not uncertain. Settlement remains
			// valid evidence and CloseRequestBudgetOwned reports the overrun.
			remaining, threshold = 0, p.WrapUpTokens
			return nil
		}
		if err != nil {
			return err
		}
		remaining, threshold = min(taskLeft, runLeft), p.WrapUpTokens
		return nil
	})
	return remaining, threshold, err
}

func loadBudgetPolicy(q querier, runID string) (budget.Policy, error) {
	var p budget.Policy
	var tid, sid, state string
	var b []byte
	err := q.QueryRow("SELECT task_id,session_id,state,payload FROM budget_runs WHERE run_id=?", runID).Scan(&tid, &sid, &state, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil || json.Unmarshal(b, &p) != nil || !validBudgetPolicy(p) || p.RunID != runID || p.TaskID != tid || p.SessionID != sid || p.State != state {
		return p, fmt.Errorf("store: budget policy corrupt")
	}
	return p, nil
}

func validBudgetRequest(r budget.Request) bool {
	return uuidRE.MatchString(r.ID) && digestRE.MatchString(r.Digest) && r.API == "openai-completions" &&
		r.Provider != "" && r.Model != "" && len(r.Provider) <= 128 && len(r.Model) <= 256 &&
		!model.LooksLikeCredential(r.Provider) && !model.LooksLikeCredential(r.Model) &&
		r.InputEstimate > 0 && r.InputEstimate <= budget.MaxCount && r.MaxOutput > 0 && r.MaxOutput <= budget.MaxCount
}

func validSettlement(v budget.Settlement) bool {
	if !uuidRE.MatchString(v.ID) || !slices.Contains([]string{budget.Settled, budget.Unknown, budget.Canceled}, v.State) {
		return false
	}
	for _, n := range []*int64{v.Tokens.Input, v.Tokens.Output, v.Tokens.CacheRead, v.Tokens.CacheWrite, v.Tokens.Total} {
		if n != nil && (*n < 0 || *n > budget.MaxCount) {
			return false
		}
	}
	if v.Tokens.AssistantMessages != nil {
		return false
	}
	// A cancellation is allowed only before a send permit was issued.
	if v.State == budget.Canceled {
		if v.Terminal {
			return false
		}
		for _, n := range []*int64{v.Tokens.Input, v.Tokens.Output, v.Tokens.CacheRead, v.Tokens.CacheWrite, v.Tokens.Total} {
			if n != nil && *n != 0 {
				return false
			}
		}
	}
	if v.Tokens.Total != nil && knownSubtotal(v.Tokens) > *v.Tokens.Total {
		return false
	}
	return v.State != budget.Settled || v.Terminal && v.Tokens.Total != nil
}

func knownSubtotal(t model.TokenCounts) int64 {
	var n int64
	for _, v := range []*int64{t.Input, t.Output, t.CacheRead, t.CacheWrite} {
		if v != nil {
			n += *v
		}
	}
	return n
}

func requestOverrun(r budget.Record) bool {
	if r.Settlement == nil || r.State == budget.Canceled {
		return false
	}
	t := r.Settlement.Tokens
	return knownSubtotal(t) > r.Grant.ReservedTokens || t.Total != nil && *t.Total > r.Grant.ReservedTokens || t.Output != nil && *t.Output > r.Grant.MaxOutput
}

func validBudgetRecord(r budget.Record) bool {
	if !validBudgetRequest(r.Request) || !uuidRE.MatchString(r.RunID) || r.Grant.ID != r.Request.ID ||
		r.Grant.MaxOutput < 1 || r.Grant.MaxOutput > r.Request.MaxOutput || r.Grant.MaxOutput > budget.MaxOutput ||
		r.Grant.ReservedTokens != r.Request.InputEstimate+r.Grant.MaxOutput ||
		!slices.Contains([]string{budget.Reserved, budget.Sent, budget.Settled, budget.Unknown, budget.Canceled}, r.State) {
		return false
	}
	for _, at := range []string{r.CreatedAt, r.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return false
		}
	}
	if r.SentDigest != nil && !digestRE.MatchString(*r.SentDigest) {
		return false
	}
	if r.State == budget.Reserved && (r.SentDigest != nil || r.Settlement != nil) ||
		r.State == budget.Sent && (r.SentDigest == nil || r.Settlement != nil) ||
		r.State == budget.Settled && (r.SentDigest == nil || r.Settlement == nil) {
		return false
	}
	if r.Settlement != nil && (!validSettlement(*r.Settlement) || r.Settlement.ID != r.Request.ID || r.Settlement.State != r.State) {
		return false
	}
	if r.State == budget.Canceled && (r.SentDigest != nil || r.Settlement == nil) {
		return false
	}
	return r.Overrun == requestOverrun(r)
}

func loadBudgetRecord(q querier, id string) (budget.Record, error) {
	var r budget.Record
	var runID, state string
	var b []byte
	err := q.QueryRow("SELECT run_id,state,payload FROM budget_requests WHERE id=?", id).Scan(&runID, &state, &b)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil || json.Unmarshal(b, &r) != nil || !validBudgetRecord(r) || r.Request.ID != id || r.RunID != runID || r.State != state {
		return r, fmt.Errorf("store: budget record corrupt")
	}
	return r, nil
}

func putBudgetRecord(tx *sql.Tx, r budget.Record) error {
	if !validBudgetRecord(r) {
		return model.Invalidf("invalid budget record")
	}
	_, err := tx.Exec(`INSERT INTO budget_requests(id,run_id,state,payload) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET state=excluded.state,payload=excluded.payload`, r.Request.ID, r.RunID, r.State, mustJSON(r))
	if err != nil {
		return fmt.Errorf("store: budget write failed")
	}
	return nil
}

func budgetRows(q querier, taskID string) ([]budget.Policy, []budget.Record, error) {
	query, args := "SELECT run_id FROM budget_runs ORDER BY rowid", []any{}
	if taskID != "" {
		query, args = "SELECT run_id FROM budget_runs WHERE task_id=? ORDER BY rowid", []any{taskID}
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("store: budgets unreadable")
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return nil, nil, ErrBadBackup
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, ErrBadBackup
	}
	ps, rs := []budget.Policy{}, []budget.Record{}
	for _, id := range ids {
		p, err := loadBudgetPolicy(q, id)
		if err != nil {
			return nil, nil, err
		}
		ps = append(ps, p)
		rows, err := q.Query("SELECT id FROM budget_requests WHERE run_id=? ORDER BY rowid", id)
		if err != nil {
			return nil, nil, ErrBadBackup
		}
		rids := []string{}
		for rows.Next() {
			var rid string
			if rows.Scan(&rid) != nil {
				rows.Close()
				return nil, nil, ErrBadBackup
			}
			rids = append(rids, rid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, ErrBadBackup
		}
		for _, rid := range rids {
			r, err := loadBudgetRecord(q, rid)
			if err != nil {
				return nil, nil, err
			}
			rs = append(rs, r)
		}
	}
	return ps, rs, nil
}

func verifyBudgetOwner(q *sql.Tx, token string, p budget.Policy) (*model.State, error) {
	if err := checkToken(q, token); err != nil {
		return nil, err
	}
	ss, err := loadSession(q, p.SessionID)
	if err != nil || ss.TaskID != p.TaskID || ss.State != model.SessionRunning || ss.ActiveRunID == nil || *ss.ActiveRunID != p.RunID {
		return nil, budget.ErrDenied
	}
	st, err := readState(q)
	if err != nil {
		return nil, err
	}
	var task *model.Task
	var run *model.Run
	var profile *model.Profile
	for i := range st.Tasks {
		if st.Tasks[i].ID == p.TaskID {
			task = &st.Tasks[i]
		}
	}
	for i := range st.Runs {
		if st.Runs[i].ID == p.RunID {
			run = &st.Runs[i]
		}
	}
	for i := range st.Profiles {
		if st.Profiles[i].ID == p.ProfileID {
			profile = &st.Profiles[i]
		}
	}
	if task == nil || run == nil || profile == nil || run.TaskID != p.TaskID || run.ProfileID != p.ProfileID || !model.IsActiveRunState(run.State) ||
		model.FrozenTaskDigest(st, *task) != p.ContractDigest || model.FrozenProfileDigest(*profile) != p.ProfileDigest || profile.Provider != p.Provider || profile.Model != p.Model ||
		ss.ContractDigest != p.ContractDigest || ss.ProfileDigest != p.ProfileDigest {
		return nil, budget.ErrDenied
	}
	return st, nil
}

func (s *Store) OpenRequestBudgetOwned(token string, p budget.Policy) error {
	if !validBudgetPolicy(p) || p.State != "open" {
		return model.Invalidf("invalid budget policy")
	}
	return s.tx(func(tx *sql.Tx) error {
		st, err := verifyBudgetOwner(tx, token, p)
		if err != nil {
			return err
		}
		for _, t := range st.Tasks {
			if t.ID == p.TaskID {
				b := model.DefaultBudget()
				if t.Budget != nil {
					b = *t.Budget
				}
				if p.TaskTokens != b.MaxTokens {
					return budget.ErrDenied
				}
			}
		}
		if _, err := loadBudgetPolicy(tx, p.RunID); err != ErrNotFound {
			return budget.ErrConflict
		}
		ps, _, err := budgetRows(tx, p.TaskID)
		if err != nil {
			return err
		}
		for _, old := range ps {
			if old.ContractDigest != p.ContractDigest || old.TaskTokens != p.TaskTokens || old.TaskRequests != p.TaskRequests {
				return budget.ErrConflict
			}
		}
		_, err = tx.Exec("INSERT INTO budget_runs(run_id,task_id,session_id,state,payload) VALUES(?,?,?,?,?)", p.RunID, p.TaskID, p.SessionID, p.State, mustJSON(p))
		return err
	})
}

func budgetCharge(r budget.Record) int64 {
	if r.State == budget.Canceled {
		return 0
	}
	if r.State == budget.Settled {
		return *r.Settlement.Tokens.Total
	}
	n := r.Grant.ReservedTokens
	if r.Settlement != nil {
		n = max(n, knownSubtotal(r.Settlement.Tokens))
		if r.Settlement.Tokens.Total != nil {
			n = max(n, *r.Settlement.Tokens.Total)
		}
	}
	return n
}

func budgetRemaining(st *model.State, p budget.Policy, ps []budget.Policy, rs []budget.Record) (int64, int64, int64, error) {
	owned := map[string]bool{}
	for _, v := range ps {
		if v.State == "unknown" {
			return 0, 0, 0, budget.ErrUnknown
		}
		owned[v.RunID] = true
	}
	taskUsed, runUsed, requests := int64(0), int64(0), int64(0)
	for _, r := range st.Runs {
		if r.TaskID != p.TaskID || owned[r.ID] || !model.RunHadProcess(r) {
			continue
		}
		if r.Usage == nil || r.Usage.UsageCompleteness != model.UsageComplete || r.Usage.Tokens.Total == nil || *r.Usage.Tokens.Total < 0 || *r.Usage.Tokens.Total > budget.MaxCount {
			return 0, 0, 0, budget.ErrUnknown
		}
		taskUsed += *r.Usage.Tokens.Total
	}
	for _, r := range rs {
		if r.Overrun {
			return 0, 0, 0, budget.ErrDenied
		}
		n := budgetCharge(r)
		taskUsed += n
		if r.RunID == p.RunID {
			runUsed += n
		}
		if r.State != budget.Canceled {
			requests++
		}
	}
	return max(0, p.TaskTokens-taskUsed), max(0, p.RunTokens-runUsed), max(0, p.TaskRequests-requests), nil
}

func (s *Store) ReserveRequestOwned(token, runID string, r budget.Request) (budget.Grant, error) {
	var grant budget.Grant
	if !validBudgetRequest(r) {
		return grant, model.Invalidf("invalid budget request")
	}
	err := s.tx(func(tx *sql.Tx) error {
		p, err := loadBudgetPolicy(tx, runID)
		if err != nil {
			return err
		}
		st, err := verifyBudgetOwner(tx, token, p)
		if err != nil {
			return err
		}
		deadline, _ := time.Parse(time.RFC3339Nano, p.Deadline)
		if p.State != "open" || time.Now().After(deadline) || r.Provider != p.Provider || r.Model != p.Model {
			return budget.ErrDenied
		}
		old, err := loadBudgetRecord(tx, r.ID)
		if err == nil {
			if old.RunID != runID || !reflect.DeepEqual(old.Request, r) || old.State != budget.Reserved {
				return budget.ErrConflict
			}
			grant = old.Grant
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		ps, rs, err := budgetRows(tx, p.TaskID)
		if err != nil {
			return err
		}
		taskLeft, runLeft, requests, err := budgetRemaining(st, p, ps, rs)
		if err != nil {
			return err
		}
		output := min(r.MaxOutput, budget.MaxOutput, min(taskLeft, runLeft)-r.InputEstimate)
		if requests < 1 || output < 1 {
			return budget.ErrDenied
		}
		grant = budget.Grant{ID: r.ID, MaxOutput: output, ReservedTokens: r.InputEstimate + output}
		at := now()
		return putBudgetRecord(tx, budget.Record{Request: r, RunID: runID, Grant: grant, State: budget.Reserved, CreatedAt: at, UpdatedAt: at})
	})
	return grant, err
}

func (s *Store) BeginRequestOwned(token, runID string, b budget.Begin) error {
	if !uuidRE.MatchString(b.ID) || !digestRE.MatchString(b.Digest) {
		return model.Invalidf("invalid request begin")
	}
	return s.tx(func(tx *sql.Tx) error {
		p, err := loadBudgetPolicy(tx, runID)
		if err != nil {
			return err
		}
		if _, err = verifyBudgetOwner(tx, token, p); err != nil {
			return err
		}
		deadline, _ := time.Parse(time.RFC3339Nano, p.Deadline)
		if p.State != "open" || time.Now().After(deadline) {
			return budget.ErrDenied
		}
		r, err := loadBudgetRecord(tx, b.ID)
		if err != nil {
			return err
		}
		if r.RunID != runID || r.State != budget.Reserved {
			return budget.ErrConflict
		}
		r.State, r.SentDigest, r.UpdatedAt = budget.Sent, &b.Digest, now()
		return putBudgetRecord(tx, r)
	})
}

func (s *Store) SettleRequestOwned(token, runID string, v budget.Settlement) error {
	if !validSettlement(v) {
		return model.Invalidf("invalid request settlement")
	}
	return s.tx(func(tx *sql.Tx) error {
		p, err := loadBudgetPolicy(tx, runID)
		if err != nil {
			return err
		}
		if _, err = verifyBudgetOwner(tx, token, p); err != nil {
			return err
		}
		r, err := loadBudgetRecord(tx, v.ID)
		if err != nil {
			return err
		}
		if r.RunID != runID {
			return budget.ErrConflict
		}
		if r.Settlement != nil {
			if reflect.DeepEqual(*r.Settlement, v) {
				return nil
			}
			return budget.ErrConflict
		}
		if p.State != "open" || v.State == budget.Canceled && r.State != budget.Reserved || v.State != budget.Canceled && r.State != budget.Sent {
			return budget.ErrConflict
		}
		r.State, r.Settlement, r.UpdatedAt = v.State, &v, now()
		r.Overrun = requestOverrun(r)
		return putBudgetRecord(tx, r)
	})
}

func (s *Store) CloseRequestBudgetOwned(token, runID string) (budget.Outcome, error) {
	var result budget.Outcome
	err := s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		p, err := loadBudgetPolicy(tx, runID)
		if err != nil {
			return err
		}
		_, rs, err := budgetRows(tx, p.TaskID)
		if err != nil {
			return err
		}
		result.Confirmed = p.State != "unknown"
		var counts [5]int64
		var missing [5]bool
		for _, r := range rs {
			if r.RunID != runID {
				continue
			}
			result.Requests++
			if r.State == budget.Reserved || r.State == budget.Sent {
				r.State, r.UpdatedAt = budget.Unknown, now()
				if err := putBudgetRecord(tx, r); err != nil {
					return err
				}
			}
			if r.State == budget.Unknown {
				result.Confirmed = false
			}
			result.Overrun = result.Overrun || r.Overrun
			if r.State == budget.Canceled {
				continue
			}
			var tc model.TokenCounts
			if r.Settlement != nil {
				tc = r.Settlement.Tokens
			}
			for i, n := range []*int64{tc.Input, tc.Output, tc.CacheRead, tc.CacheWrite, tc.Total} {
				if n == nil {
					missing[i] = true
				} else {
					counts[i] += *n
				}
			}
		}
		p.State = "closed"
		if !result.Confirmed {
			p.State = "unknown"
		}
		if _, err := tx.Exec("UPDATE budget_runs SET state=?,payload=? WHERE run_id=?", p.State, mustJSON(p), runID); err != nil {
			return err
		}
		result.Usage = model.Usage{UsageCompleteness: model.UsagePartial, Source: model.UsageSourceExecutor}
		ptrs := []**int64{&result.Usage.Tokens.Input, &result.Usage.Tokens.Output, &result.Usage.Tokens.CacheRead, &result.Usage.Tokens.CacheWrite, &result.Usage.Tokens.Total}
		if result.Requests > 0 {
			for i := range ptrs {
				if !missing[i] {
					n := counts[i]
					*ptrs[i] = &n
				}
			}
		}
		if result.Requests == 0 || result.Usage.Tokens.Total == nil {
			result.Usage.UsageCompleteness = model.UsageUnknown
		} else if result.Confirmed && !slices.Contains(missing[:], true) {
			result.Usage.UsageCompleteness = model.UsageComplete
		}
		return nil
	})
	return result, err
}

func (s *Store) ReconcileRequestBudgetsOwned(token string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		ps, rs, err := budgetRows(tx, "")
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.State == budget.Reserved || r.State == budget.Sent {
				r.State, r.UpdatedAt = budget.Unknown, now()
				if err := putBudgetRecord(tx, r); err != nil {
					return err
				}
			}
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		changed := false
		for _, p := range ps {
			if p.State == "open" {
				p.State = "unknown"
				if _, err := tx.Exec("UPDATE budget_runs SET state=?,payload=? WHERE run_id=?", p.State, mustJSON(p), p.RunID); err != nil {
					return err
				}
				// Cover a crash between ledger settlement and the final run record.
				for i := range st.Runs {
					if st.Runs[i].ID == p.RunID {
						st.Runs[i].State, st.Runs[i].UpdatedAt = model.RunUnknown, now()
						changed = true
					}
				}
				for i := range st.Tasks {
					if st.Tasks[i].ID == p.TaskID {
						if st.Tasks[i].State != model.TaskUnknown {
							st.Tasks[i].RecordedState = st.Tasks[i].State
						}
						why := "request_budget_unverifiable"
						st.Tasks[i].State, st.Tasks[i].StateReason, st.Tasks[i].UpdatedAt = model.TaskUnknown, &why, now()
						changed = true
					}
				}
			}
		}
		if changed {
			return writeState(tx, st)
		}
		return nil
	})
}

func (s *Store) RequestBudgetRecords(runID string) ([]budget.Record, error) {
	tx, err := s.rdb.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := loadBudgetPolicy(tx, runID)
	if err != nil {
		return nil, err
	}
	_, rs, err := budgetRows(tx, p.TaskID)
	if err != nil {
		return nil, err
	}
	out := []budget.Record{}
	for _, r := range rs {
		if r.RunID == runID {
			out = append(out, r)
		}
	}
	return out, nil
}

func validateBudgetBackup(q querier) error {
	ps, rs, err := budgetRows(q, "")
	if err != nil {
		return ErrBadBackup
	}
	var count int
	if q.QueryRow("SELECT count(*) FROM budget_requests").Scan(&count) != nil || count != len(rs) {
		return ErrBadBackup
	}
	byRun := map[string]budget.Policy{}
	st, err := readState(q)
	if err != nil {
		return ErrBadBackup
	}
	for _, p := range ps {
		ss, err := loadSession(q, p.SessionID)
		if err != nil || ss.TaskID != p.TaskID || ss.ContractDigest != p.ContractDigest || ss.ProfileDigest != p.ProfileDigest {
			return ErrBadBackup
		}
		var sid string
		if q.QueryRow("SELECT session_id FROM session_runs WHERE run_id=?", p.RunID).Scan(&sid) != nil || sid != p.SessionID {
			return ErrBadBackup
		}
		bound := false
		for _, r := range st.Runs {
			if r.ID == p.RunID {
				bound = r.TaskID == p.TaskID && r.ProfileID == p.ProfileID
			}
		}
		if !bound || ss.ProfileID != p.ProfileID {
			return ErrBadBackup
		}
		byRun[p.RunID] = p
	}
	for _, r := range rs {
		p := byRun[r.RunID]
		if p.State != "open" && (r.State == budget.Reserved || r.State == budget.Sent) {
			return ErrBadBackup
		}
	}
	return nil
}
