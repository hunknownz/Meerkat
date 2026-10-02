package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

// Issue receipt states. "unknown" means a POST may have happened; it must be reconciled by marker before any retry.
const (
	IssuePending = "pending"
	IssuePosting = "posting"
	IssueUnknown = "unknown"
	IssuePosted  = "posted"
)

// MaxIssueBodyBytes bounds a prepared update body.
const MaxIssueBodyBytes = 64 << 10

// IssueReceipt is the typed per-delivery sync record. The body lives in a private file; the DB holds path/hash/result.
type IssueReceipt struct {
	SchemaVersion int     `json:"schemaVersion"`
	DeliveryID    string  `json:"deliveryId"`
	TaskID        string  `json:"taskId"`
	IssueURL      *string `json:"issueUrl"`
	BodyHash      string  `json:"bodyHash"`
	BodyPath      string  `json:"bodyPath,omitempty"`
	Marker        string  `json:"marker"`
	State         string  `json:"state"`
	Attempts      int     `json:"attempts"`
	Rev           int64   `json:"rev"`
	ClaimToken    string  `json:"claimToken,omitempty"`
	ClaimedAt     string  `json:"claimedAt,omitempty"`
	PreparedAt    string  `json:"preparedAt"`
	UpdatedAt     string  `json:"updatedAt"`
	PostedAt      string  `json:"postedAt,omitempty"`
	LastAttemptAt string  `json:"lastAttemptAt,omitempty"`
	CommentURL    string  `json:"commentUrl,omitempty"`
	LastError     string  `json:"lastError,omitempty"`
	FoundExisting bool    `json:"foundExisting,omitempty"`
	Origin        string  `json:"origin,omitempty"`
}

// DeliveryMarker is the exact per-delivery dedupe marker (compatible with 0.2.x).
func DeliveryMarker(deliveryID string) string { return "<!-- meerkat-delivery:" + deliveryID + " -->" }

func hashBody(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func validReceipt(r IssueReceipt) error {
	if !uuidRE.MatchString(r.DeliveryID) || !uuidRE.MatchString(r.TaskID) || !strings.HasPrefix(r.BodyHash, "sha256:") ||
		!hashHexRE.MatchString(strings.TrimPrefix(r.BodyHash, "sha256:")) {
		return model.Invalidf("issue receipt identity is invalid")
	}
	if !contains([]string{IssuePending, IssuePosting, IssueUnknown, IssuePosted}, r.State) || r.Attempts < 0 || r.Attempts > 1000 {
		return model.Invalidf("issue receipt state is invalid")
	}
	if len(r.CommentURL) > 600 || len(r.LastError) > 100 || (r.IssueURL != nil && len(*r.IssueURL) > 500) {
		return model.Invalidf("issue receipt fields are too long")
	}
	if r.Marker != DeliveryMarker(r.DeliveryID) {
		return model.Invalidf("issue receipt marker is invalid")
	}
	return model.CheckStrings("issue receipt", r.CommentURL, r.LastError)
}

func loadReceipt(q querier, deliveryID string) (IssueReceipt, error) {
	var r IssueReceipt
	var p []byte
	err := q.QueryRow("SELECT payload FROM issue_receipts WHERE id = ?", deliveryID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil || json.Unmarshal(p, &r) != nil {
		return r, fmt.Errorf("store: corrupt record")
	}
	return r, nil
}

// LoadIssueReceipt returns the receipt for a delivery or ErrNotFound.
func (s *Store) LoadIssueReceipt(deliveryID string) (IssueReceipt, error) {
	return loadReceipt(s.db, strings.ToLower(deliveryID))
}

// SaveIssueReceipt creates a receipt once. If one exists it is returned unchanged (created=false); a receipt for the
// same delivery with a different task or body hash is a conflict.
func (s *Store) SaveIssueReceipt(r IssueReceipt) (IssueReceipt, bool, error) {
	r.SchemaVersion, r.Marker = 1, DeliveryMarker(r.DeliveryID)
	if err := validReceipt(r); err != nil {
		return r, false, err
	}
	created := false
	err := s.tx(func(tx *sql.Tx) error {
		old, err := loadReceipt(tx, r.DeliveryID)
		if err == nil {
			if old.TaskID != r.TaskID || old.BodyHash != r.BodyHash {
				return fmt.Errorf("%w: issue receipt differs", ErrConflict)
			}
			r = old
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		r.Rev, created = 1, true
		if r.UpdatedAt == "" {
			r.UpdatedAt = now()
		}
		if _, err := tx.Exec("INSERT INTO issue_receipts (id, created_at, payload) VALUES (?, ?, ?)", r.DeliveryID, now(), mustJSON(r)); err != nil {
			return fmt.Errorf("%w: issue receipt exists", ErrConflict)
		}
		return nil
	})
	return r, created, err
}

// CompareAndSetIssueReceipt atomically replaces the receipt only if the stored revision equals next.Rev. Identity
// (task, body hash, marker) is immutable. Returns the stored record with the incremented revision.
func (s *Store) CompareAndSetIssueReceipt(next IssueReceipt) (IssueReceipt, error) {
	if err := validReceipt(next); err != nil {
		return next, err
	}
	err := s.tx(func(tx *sql.Tx) error {
		old, err := loadReceipt(tx, next.DeliveryID)
		if err != nil {
			return err
		}
		if old.Rev != next.Rev {
			return fmt.Errorf("%w: issue receipt changed concurrently", ErrConflict)
		}
		if old.TaskID != next.TaskID || old.BodyHash != next.BodyHash || old.BodyPath != next.BodyPath {
			return fmt.Errorf("%w: issue receipt identity is immutable", ErrConflict)
		}
		if old.State == IssuePosted && next.State != IssuePosted {
			return fmt.Errorf("%w: posted receipts are final", ErrConflict)
		}
		next.Rev++
		next.UpdatedAt = now()
		if _, err := tx.Exec("UPDATE issue_receipts SET payload = ? WHERE id = ?", mustJSON(next), next.DeliveryID); err != nil {
			return fmt.Errorf("store: write failed")
		}
		return nil
	})
	return next, err
}

func (s *Store) bodyPath(deliveryID string) string {
	return filepath.Join(s.bodyDir(), deliveryID+".md")
}

func (s *Store) stageBody(deliveryID string, body []byte) (string, error) {
	if !uuidRE.MatchString(deliveryID) || len(body) > MaxLegacyBodyBytes {
		return "", model.Invalidf("issue body is invalid")
	}
	if err := checkDir(s.bodyDir()); err != nil {
		return "", err
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(s.bodyDir(), "."+deliveryID+"."+tok[:12]+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("store: stage body failed")
	}
	_, werr := f.Write(body)
	serr := f.Sync()
	if cerr := f.Close(); werr != nil || serr != nil || cerr != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("store: stage body failed")
	}
	return tmp, nil
}

// publishBody renames a staged body into place. An existing identical body is kept; a different one is never replaced.
func (s *Store) publishBody(deliveryID, tmp string) error {
	dst := s.bodyPath(deliveryID)
	if old, err := readSafe(dst, MaxLegacyBodyBytes); err != nil {
		os.Remove(tmp)
		return err
	} else if old != nil {
		nb, _ := os.ReadFile(tmp)
		os.Remove(tmp)
		if hashBody(old) != hashBody(nb) {
			return fmt.Errorf("%w: issue body differs", ErrConflict)
		}
		return nil
	}
	if err := os.Link(tmp, dst); err != nil { // no-clobber publish
		os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: issue body published concurrently", ErrConflict)
		}
		return fmt.Errorf("store: publish body failed")
	}
	os.Remove(tmp)
	return nil
}

// WriteIssueBody stores a prepared body in the private data directory (0600) and returns its path and hash.
func (s *Store) WriteIssueBody(deliveryID string, body []byte) (string, string, error) {
	if len(body) > MaxIssueBodyBytes {
		return "", "", model.Invalidf("issue body is too large")
	}
	tmp, err := s.stageBody(deliveryID, body)
	if err != nil {
		return "", "", err
	}
	if err := s.publishBody(deliveryID, tmp); err != nil {
		return "", "", err
	}
	return s.bodyPath(deliveryID), hashBody(body), nil
}

// ReadIssueBody reads the private body for a receipt and verifies its hash.
func (s *Store) ReadIssueBody(r IssueReceipt) ([]byte, error) {
	if r.BodyPath == "" || r.BodyPath != s.bodyPath(r.DeliveryID) {
		return nil, fmt.Errorf("%w: issue body path", ErrNotFound)
	}
	b, err := readSafe(r.BodyPath, MaxLegacyBodyBytes)
	if err != nil || b == nil {
		return nil, fmt.Errorf("%w: issue body missing", ErrNotFound)
	}
	if hashBody(b) != r.BodyHash {
		return nil, fmt.Errorf("%w: issue body changed after preparation", ErrConflict)
	}
	return b, nil
}

// ClaimStale reports whether a claim is older than ttl (or absent).
func (r IssueReceipt) ClaimStale(ttl time.Duration) bool {
	if r.ClaimToken == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, r.ClaimedAt)
	return err != nil || time.Since(t) > ttl
}
