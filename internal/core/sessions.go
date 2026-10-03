package core

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/hunknownz/Meerkat/internal/executor"
	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/store"
)

var sessionDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func profileContract(p model.Profile) string {
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (c *Core) sessionBinding(s model.Session, worktree string) executor.SessionBinding {
	return executor.SessionBinding{ID: s.ID, ProviderID: s.ProviderID, File: filepath.Join(c.st.DataDir(), filepath.FromSlash(s.FileRef)), Digest: s.FileDigest, Worktree: worktree}
}

// selectSession only resumes verified idle history. A reviewer is always fresh
// so a previous candidate's conversation cannot bias a new independent review.
func (c *Core) selectSession(st *model.State, t model.Task, p model.Profile, role, sha string) (*model.Session, bool, error) {
	x, ok := c.reg[execName(p)].(executor.StatefulExecutor)
	if !ok || !x.Capabilities().PersistentSessions {
		return nil, false, nil
	}
	all, err := c.st.SessionsForTask(t.ID)
	if err != nil {
		return nil, false, err
	}
	for _, ss := range all {
		if ss.State == model.SessionUnknown || ss.State == model.SessionRunning {
			return nil, false, invalid("session identity or ownership is unresolved")
		}
	}
	contract, profile := taskContract(st, t), profileContract(p)
	if role != "reviewer" {
		for i := len(all) - 1; i >= 0; i-- {
			ss := all[i]
			if ss.Role != role {
				continue
			}
			if ss.Executor != execName(p) || ss.ProfileID != p.ID || ss.ProfileDigest != profile || ss.ContractDigest != contract || !sessionSHAProgress(st, t, ss.LastSHA, sha) {
				return nil, false, invalid("session frozen contract or SHA changed")
			}
			b := c.sessionBinding(ss, t.Worktree)
			snap, err := x.InspectSession(b)
			if err != nil || snap.ProviderID != ss.ProviderID || snap.Digest != ss.FileDigest {
				return nil, false, invalid("session history changed")
			}
			return &ss, false, nil
		}
	}
	id, at := newUUID(), now()
	ss := model.Session{ID: id, TaskID: t.ID, Role: role, Executor: execName(p), ProfileID: p.ID, ProfileDigest: profile, ContractDigest: contract,
		FileRef: store.SessionFileRef(id), LastSHA: sha, State: model.SessionIdle, CreatedAt: at, UpdatedAt: at}
	root := filepath.Join(c.st.DataDir(), "sessions")
	if err := privateSessionDir(root, true); err != nil {
		return nil, false, err
	}
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, false, invalid("session directory unavailable")
	}
	b := c.sessionBinding(ss, t.Worktree)
	snap, err := x.InitializeSession(b)
	if err != nil {
		return nil, false, err
	}
	ss.ProviderID, ss.FileDigest = snap.ProviderID, snap.Digest
	for _, path := range []string{root, c.st.DataDir()} {
		dir, e := os.Open(path)
		if e != nil {
			return nil, false, invalid("session directory could not be saved")
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return nil, false, invalid("session directory could not be saved")
		}
	}
	return &ss, true, nil
}

func privateSessionDir(path string, create bool) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = os.Mkdir(path, 0o700); err == nil || errors.Is(err, os.ErrExist) {
			fi, err = os.Lstat(path)
		}
	}
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		return invalid("session directory is unsafe")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return invalid("session directory is unsafe")
	}
	return nil
}

// Another role may have advanced this task's candidate after the session last
// ran. Accept only a contiguous chain of this task's already verified runs.
// An arbitrary clean commit or unrelated task's SHA never advances history.
func sessionSHAProgress(st *model.State, t model.Task, old, head string) bool {
	if old == head {
		return true
	}
	if deref(t.CandidateSha) != head || !isAncestor(t.Worktree, old, head) {
		return false
	}
	cur := old
	for _, r := range st.Runs {
		if r.TaskID != t.ID || r.State != model.RunSucceeded || r.Role == "reviewer" || r.ContextRef == nil || *r.ContextRef != t.ContextRef {
			continue
		}
		sm := summaryOf(r)
		if sm.BaselineSha == cur && sm.Outcome == "success" && shaRE.MatchString(sm.ResultSha) {
			cur = sm.ResultSha
			if cur == head {
				return true
			}
		}
	}
	return false
}

func (c *Core) startRoleRecord(s *model.Session, runID string, fresh bool, fn func(*model.State) error) error {
	if s == nil {
		return c.update(fn)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	err := c.st.StartSessionRunOwned(c.token, *s, runID, fresh, fn)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return err
}

func (c *Core) finishRoleRecord(s *model.Session, runID string, fn func(*model.State) error) error {
	if s == nil {
		return c.update(fn)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.isLost() {
		return ErrLeaseLost
	}
	err := c.st.FinishSessionRunOwned(c.token, *s, runID, fn)
	if errors.Is(err, store.ErrLeaseLost) {
		c.loseLease()
	}
	return err
}
