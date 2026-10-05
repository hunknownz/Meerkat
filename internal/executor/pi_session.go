package executor

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/hunknownz/Meerkat/internal/platform"
	"io"
	"os"
	"path/filepath"
	"time"
)

const MaxSessionBytes = 64 << 20

func (*Pi) Capabilities() Capabilities {
	return Capabilities{Protocol: "pi-rpc-v0.99.1", PersistentSessions: true, BidirectionalControl: true, GracefulWrapUp: true, GracefulPause: true, QueuedFollowUp: true, UsageEvents: true, RequestBudgetGate: true}
}

func sessionParent(b SessionBinding) error {
	if !safeIDRE.MatchString(b.ID) || !filepath.IsAbs(b.File) || !filepath.IsAbs(b.Worktree) {
		return invalid("invalid session binding")
	}
	fi, err := os.Lstat(filepath.Dir(b.File))
	if err != nil || !fi.IsDir() || !platform.Private(filepath.Dir(b.File), fi, 0o700) {
		return invalid("session directory is unsafe")
	}
	return nil
}

// InitializeSession writes the adapter's format to a new 0600 file and syncs it
// before the scheduler persists its reference. It never overwrites a history.
func (p *Pi) InitializeSession(b SessionBinding) (SessionSnapshot, error) {
	if err := sessionParent(b); err != nil {
		return SessionSnapshot{}, err
	}
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": b.ID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": b.Worktree})
	f, err := platform.OpenFile(b.File, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return SessionSnapshot{}, invalid("session file already exists or is unsafe")
	}
	_, err = f.Write(append(header, '\n'))
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		return SessionSnapshot{}, invalid("session file could not be saved")
	}
	e := platform.SyncDir(filepath.Dir(b.File))
	if e != nil {
		return SessionSnapshot{}, invalid("session directory could not be saved")
	}
	b.ProviderID = b.ID
	return p.InspectSession(b)
}

// InspectSession validates the complete framing, identity and worktree without
// returning message text. Hashes cover exact bytes, including unknown entries.
func (*Pi) InspectSession(b SessionBinding) (SessionSnapshot, error) {
	if err := sessionParent(b); err != nil {
		return SessionSnapshot{}, err
	}
	f, err := platform.OpenFile(b.File, os.O_RDWR, 0)
	if err != nil {
		return SessionSnapshot{}, invalid("session file unavailable")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !platform.Private(b.File, fi, 0o600) || fi.Size() < 1 || fi.Size() > MaxSessionBytes {
		return SessionSnapshot{}, invalid("session file unsafe or oversized")
	}
	h := sha256.New()
	scan := bufio.NewScanner(io.TeeReader(io.LimitReader(f, MaxSessionBytes+1), h))
	scan.Buffer(make([]byte, 64<<10), maxLine)
	var providerID string
	n := 0
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 || !json.Valid(line) {
			return SessionSnapshot{}, invalid("session format is invalid")
		}
		var v struct {
			Type    string `json:"type"`
			Version int    `json:"version"`
			ID      string `json:"id"`
			Cwd     string `json:"cwd"`
		}
		if json.Unmarshal(line, &v) != nil || v.Type == "" {
			return SessionSnapshot{}, invalid("session format is invalid")
		}
		if n == 0 {
			if v.Type != "session" || v.Version != 3 || v.ID != b.ProviderID || v.Cwd != b.Worktree {
				return SessionSnapshot{}, invalid("session identity changed")
			}
			providerID = v.ID
		} else if v.Type == "session" {
			return SessionSnapshot{}, invalid("session contains multiple headers")
		}
		n++
	}
	if scan.Err() != nil || n == 0 {
		return SessionSnapshot{}, invalid("session format is invalid")
	}
	if err := f.Sync(); err != nil {
		return SessionSnapshot{}, invalid("session history could not be synced")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != fi.Size() || !after.ModTime().Equal(fi.ModTime()) {
		return SessionSnapshot{}, invalid("session changed during inspection")
	}
	return SessionSnapshot{ProviderID: providerID, Digest: hex.EncodeToString(h.Sum(nil))}, nil
}
