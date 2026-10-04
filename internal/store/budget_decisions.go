package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
	budget "github.com/hunknownz/Meerkat/internal/requestbudget"
)

const migrationV7 = `CREATE TABLE budget_decisions (request_id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL REFERENCES tasks(id), revision INTEGER NOT NULL, payload TEXT NOT NULL,
 UNIQUE(task_id,revision)); PRAGMA user_version = 7;`
const decisionTable = "budget_decisions"

func budgetDecisions(q querier, taskID string) ([]model.BudgetDecision, error) {
	var exists int
	if q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", decisionTable).Scan(&exists) != nil {
		return nil, ErrConflict
	}
	if exists == 0 {
		return []model.BudgetDecision{}, nil
	} // Old backups have no decisions.
	query, args := "SELECT request_id,task_id,revision,payload FROM budget_decisions", []any{}
	if taskID != "" {
		query += " WHERE task_id=?"
		args = append(args, taskID)
	}
	query += " ORDER BY task_id,revision"
	rows, e := q.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.BudgetDecision{}
	for rows.Next() {
		var id, tid string
		var rev int64
		var raw []byte
		var d model.BudgetDecision
		if rows.Scan(&id, &tid, &rev, &raw) != nil || json.Unmarshal(raw, &d) != nil || !model.ValidBudgetDecision(d.BudgetDecisionInput) || d.RequestID != id || d.Proposal.TaskID != tid || d.Proposal.Revision+1 != rev {
			return nil, ErrConflict
		}
		if _, e := time.Parse(time.RFC3339Nano, d.CreatedAt); e != nil {
			return nil, ErrConflict
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func originalBudget(t model.Task) model.Budget {
	if t.Budget != nil {
		return *t.Budget
	}
	return model.DefaultBudget()
}
func allowanceAt(q querier, st *model.State, t model.Task, revision *int64) (model.BudgetAuthorization, error) {
	b := originalBudget(t)
	v := model.BudgetAuthorization{TaskID: t.ID, OriginalTokens: b.MaxTokens, OriginalWallSeconds: b.MaxWallSeconds, AuthorizedTokens: b.MaxTokens, AuthorizedWallSeconds: b.MaxWallSeconds, Decisions: []model.BudgetDecisionSummary{}}
	ds, e := budgetDecisions(q, t.ID)
	if e != nil {
		return v, e
	}
	for _, d := range ds {
		p := d.Proposal
		if p.Revision != v.Revision || p.CurrentTokens != v.AuthorizedTokens || p.CurrentWallSeconds != v.AuthorizedWallSeconds || p.ContractDigest != model.FrozenTaskDigest(st, t) {
			return v, ErrConflict
		}
		if revision != nil && v.Revision == *revision {
			return v, nil
		}
		v.Revision++
		v.AddedTokens += p.AddTokens
		v.AddedWallSeconds += p.AddWallSeconds
		v.AuthorizedTokens += p.AddTokens
		v.AuthorizedWallSeconds += p.AddWallSeconds
		v.Decisions = append(v.Decisions, model.BudgetDecisionSummary{RequestID: d.RequestID, Revision: v.Revision, AddTokens: p.AddTokens, AddWallSeconds: p.AddWallSeconds, Reason: p.Reason, CreatedAt: d.CreatedAt})
	}
	if revision != nil && v.Revision != *revision {
		return v, ErrConflict
	}
	return v, nil
}
func taskByID(st *model.State, id string) *model.Task {
	for i := range st.Tasks {
		if st.Tasks[i].ID == id {
			return &st.Tasks[i]
		}
	}
	return nil
}

func (s *Store) BudgetAuthorization(taskID string) (model.BudgetAuthorization, error) {
	tx, e := s.rdb.Begin()
	if e != nil {
		return model.BudgetAuthorization{}, e
	}
	defer tx.Rollback()
	st, e := readState(tx)
	if e != nil {
		return model.BudgetAuthorization{}, e
	}
	t := taskByID(st, taskID)
	if t == nil {
		return model.BudgetAuthorization{}, ErrNotFound
	}
	return allowanceAt(tx, st, *t, nil)
}
func (s *Store) EffectiveBudget(taskID string) (model.Budget, int64, error) {
	tx, e := s.rdb.Begin()
	if e != nil {
		return model.Budget{}, 0, e
	}
	defer tx.Rollback()
	st, e := readState(tx)
	if e != nil {
		return model.Budget{}, 0, e
	}
	t := taskByID(st, taskID)
	if t == nil {
		return model.Budget{}, 0, ErrNotFound
	}
	v, e := allowanceAt(tx, st, *t, nil)
	b := originalBudget(*t)
	b.MaxTokens, b.MaxWallSeconds = v.AuthorizedTokens, v.AuthorizedWallSeconds
	return b, v.Revision, e
}

// Proposal evidence is computed from one DB snapshot, with every previous
// request and usage still charged. Worktree/profile inspection is done by Core.
func proposeBudget(q querier, st *model.State, in model.BudgetIncrease) (model.BudgetProposal, error) {
	t := taskByID(st, in.TaskID)
	if t == nil {
		return model.BudgetProposal{}, ErrNotFound
	}
	if t.State != model.TaskPaused && !(t.State == model.TaskStopped && t.StateReason != nil && slices.Contains([]string{"budget_tokens", "budget_time", "token_limit", "wall_timeout"}, *t.StateReason)) {
		return model.BudgetProposal{}, model.Invalidf("budget decision requires a paused or budget-stopped task")
	}
	var claimed int
	if q.QueryRow("SELECT count(*) FROM worktree_claims WHERE task_id=? OR worktree=?", t.ID, t.Worktree).Scan(&claimed) != nil || claimed != 0 {
		return model.BudgetProposal{}, ErrConflict
	}
	v, e := allowanceAt(q, st, *t, nil)
	if e != nil {
		return model.BudgetProposal{}, e
	}
	ps, rs, e := budgetRows(q, t.ID)
	if e != nil {
		return model.BudgetProposal{}, e
	}
	for _, p := range ps {
		if p.State != "closed" {
			return model.BudgetProposal{}, budget.ErrUnknown
		}
	}
	for _, r := range rs {
		if r.Overrun {
			return model.BudgetProposal{}, budget.ErrDenied
		}
		if r.State != budget.Settled && r.State != budget.Canceled {
			return model.BudgetProposal{}, budget.ErrUnknown
		}
	}
	// Per-run request-budget ledger evidence. Every policy above is closed and
	// every record settled or canceled, so settled request totals are the only
	// reported token totals; canceled never-sent reservations consume no reported tokens.
	runPolicy := map[string]bool{}
	for _, p := range ps {
		runPolicy[p.RunID] = true
	}
	runSettled, runTotal := map[string]int{}, map[string]int64{}
	for _, rec := range rs {
		if rec.State == budget.Settled {
			runSettled[rec.RunID]++
			runTotal[rec.RunID] += *rec.Settlement.Tokens.Total
		}
	}
	ss, e := sessions(q, t.ID)
	if e != nil {
		return model.BudgetProposal{}, e
	}
	for _, s := range ss {
		if s.State != model.SessionIdle || s.ActiveRunID != nil {
			return model.BudgetProposal{}, budget.ErrUnknown
		}
	}
	runs := []model.Run{}
	for _, r := range st.Runs {
		if r.TaskID != t.ID {
			continue
		}
		if !model.IsTerminalRunState(r.State) || r.State == model.RunUnknown {
			return model.BudgetProposal{}, budget.ErrUnknown
		}
		if model.RunHadProcess(r) {
			if r.Usage == nil || r.Usage.Tokens.Total == nil {
				return model.BudgetProposal{}, budget.ErrUnknown
			}
			if r.Usage.UsageCompleteness != model.UsageComplete {
				// A partial reported breakdown is evidence only when the exact
				// run's closed ledger records at least one settled request and the
				// raw request totals sum exactly to the reported run total. Unknown
				// completeness, a missing ledger for this run, pending requests and
				// mismatched totals are still unresolved.
				if r.Usage.UsageCompleteness != model.UsagePartial || !runPolicy[r.ID] ||
					runSettled[r.ID] < 1 || runTotal[r.ID] != *r.Usage.Tokens.Total {
					return model.BudgetProposal{}, budget.ErrUnknown
				}
			}
			a, ae := time.Parse(time.RFC3339Nano, r.StartedAt)
			if r.EndedAt == nil {
				return model.BudgetProposal{}, budget.ErrUnknown
			}
			b, be := time.Parse(time.RFC3339Nano, *r.EndedAt)
			if ae != nil || be != nil || b.Before(a) {
				return model.BudgetProposal{}, budget.ErrUnknown
			}
		}
		runs = append(runs, r)
	}
	cps, e := checkpoints(q, t.ID)
	if e != nil {
		return model.BudgetProposal{}, e
	}
	raw, _ := json.Marshal([]any{t, v, runs, ps, rs, ss, cps})
	p := model.BudgetProposal{BudgetIncrease: in, SchemaVersion: 1, Revision: v.Revision, CurrentTokens: v.AuthorizedTokens, CurrentWallSeconds: v.AuthorizedWallSeconds, ContractDigest: model.FrozenTaskDigest(st, *t), EvidenceDigest: fmt.Sprintf("%x", sha256.Sum256(raw))}
	if !model.ValidBudgetProposal(p) {
		return p, model.Invalidf("additional budget exceeds task limits")
	}
	return p, nil
}
func (s *Store) ProposeBudget(in model.BudgetIncrease) (model.BudgetProposal, error) {
	if !model.ValidBudgetIncrease(in) {
		return model.BudgetProposal{}, model.Invalidf("invalid budget increase")
	}
	tx, e := s.rdb.Begin()
	if e != nil {
		return model.BudgetProposal{}, e
	}
	defer tx.Rollback()
	st, e := readState(tx)
	if e != nil {
		return model.BudgetProposal{}, e
	}
	return proposeBudget(tx, st, in)
}
func (s *Store) BudgetDecision(requestID string) (model.BudgetDecision, error) {
	return loadBudgetDecision(s.rdb, requestID)
}
func loadBudgetDecision(q querier, requestID string) (model.BudgetDecision, error) {
	var raw []byte
	var taskID string
	var revision int64
	var d model.BudgetDecision
	e := q.QueryRow("SELECT task_id,revision,payload FROM budget_decisions WHERE request_id=?", requestID).Scan(&taskID, &revision, &raw)
	if e == sql.ErrNoRows {
		return d, ErrNotFound
	}
	if e != nil || json.Unmarshal(raw, &d) != nil || !model.ValidBudgetDecision(d.BudgetDecisionInput) || d.RequestID != requestID || d.Proposal.TaskID != taskID || d.Proposal.Revision+1 != revision {
		return d, ErrConflict
	}
	if _, e := time.Parse(time.RFC3339Nano, d.CreatedAt); e != nil {
		return d, ErrConflict
	}
	return d, nil
}
func (s *Store) ApplyBudgetDecisionOwned(token string, in model.BudgetDecisionInput) (model.BudgetDecision, error) {
	var out model.BudgetDecision
	if !model.ValidBudgetDecision(in) {
		return out, model.Invalidf("explicit apply and authorization reference required")
	}
	e := s.tx(func(tx *sql.Tx) error {
		if e := checkToken(tx, token); e != nil {
			return e
		}
		var e error
		out, e = loadBudgetDecision(tx, in.RequestID)
		if e == nil {
			if !reflect.DeepEqual(out.BudgetDecisionInput, in) {
				return ErrConflict
			}
			return nil
		}
		if e != ErrNotFound {
			return e
		}
		st, e := readState(tx)
		if e != nil {
			return e
		}
		p, e := proposeBudget(tx, st, in.Proposal.BudgetIncrease)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(p, in.Proposal) {
			return model.Invalidf("budget proposal evidence or revision changed")
		}
		out = model.BudgetDecision{BudgetDecisionInput: in, CreatedAt: now()}
		_, e = tx.Exec("INSERT INTO budget_decisions(request_id,task_id,revision,payload) VALUES(?,?,?,?)", in.RequestID, in.Proposal.TaskID, in.Proposal.Revision+1, mustJSON(out))
		return e
	})
	return out, e
}
func validateDecisionBackup(q querier) error {
	ds, e := budgetDecisions(q, "")
	if e != nil {
		return ErrBadBackup
	}
	st, e := readState(q)
	if e != nil {
		return ErrBadBackup
	}
	seen := map[string]bool{}
	for _, d := range ds {
		t := taskByID(st, d.Proposal.TaskID)
		if t == nil {
			return ErrBadBackup
		}
		if !seen[t.ID] {
			if _, e := allowanceAt(q, st, *t, nil); e != nil {
				return ErrBadBackup
			}
			seen[t.ID] = true
		}
	}
	return nil
}
