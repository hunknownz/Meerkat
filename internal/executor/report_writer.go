package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// reportWriter fills only bindings from observed Git and frozen request facts.
// Report conclusions and check outcomes must be supplied by the execution agent.
func reportWriter(req Request, pp *prepared) func(context.Context, json.RawMessage) (any, error) {
	var mu sync.Mutex
	return func(ctx context.Context, body json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.Role != "developer" && req.Role != "reviewer" && req.Role != "polisher" {
			return nil, invalid("invalid report role")
		}
		var fields map[string]json.RawMessage
		if len(body) > maxReport || json.Unmarshal(body, &fields) != nil || fields == nil {
			return nil, invalid("invalid report draft")
		}
		allowed := map[string]bool{"summary": true, "checks": true, "knownGaps": true}
		if req.Role == "reviewer" {
			allowed["verdict"], allowed["findings"] = true, true
		} else {
			allowed["decision"] = true
		}
		for key := range fields {
			if !allowed[key] {
				return nil, invalid("invalid report draft field")
			}
		}
		head, err := gitOut(ctx, pp.worktree, "rev-parse", "HEAD")
		if err != nil || !shaRE.MatchString(head) || req.Role == "reviewer" && head != req.ExpectedSHA {
			return nil, invalid("report candidate changed")
		}
		fields["candidateSha"], _ = json.Marshal(head)
		if req.ContextDigest == "" {
			fields["contextDigest"] = json.RawMessage("null")
		} else {
			fields["contextDigest"], _ = json.Marshal(req.ContextDigest)
		}
		encoded, err := json.Marshal(fields)
		if err != nil || len(encoded) > maxReport {
			return nil, invalid("invalid report draft")
		}
		if _, ok := validateReport(req.Role, encoded); !ok {
			return nil, invalid("invalid report draft")
		}
		var strict Report
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&strict) != nil {
			return nil, invalid("invalid report draft")
		}
		file, err := os.OpenFile(pp.report, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			f, e := openNoFollow(pp.report)
			if e != nil {
				return nil, invalid("report unreadable")
			}
			defer f.Close()
			if st, e := f.Stat(); e != nil || !st.Mode().IsRegular() || st.Size() > maxReport || !ownedByMe(st) {
				return nil, invalid("report unreadable")
			}
			raw, e := io.ReadAll(io.LimitReader(f, maxReport+1))
			old, valid := validateReport(req.Role, raw)
			if e != nil || len(raw) > maxReport || !valid || old.CandidateSHA != head {
				return nil, invalid("report already exists")
			}
			var actual map[string]json.RawMessage
			if json.Unmarshal(raw, &actual) != nil {
				return nil, invalid("report unreadable")
			}
			canonical, _ := json.Marshal(actual)
			if !bytes.Equal(canonical, encoded) {
				return nil, invalid("report conflict")
			}
		} else {
			if err != nil {
				return nil, invalid("report write failed")
			}
			_, err = file.Write(encoded)
			if err == nil {
				err = file.Sync()
			}
			closeErr := file.Close()
			if err != nil || closeErr != nil {
				return nil, invalid("report write failed")
			}
		}
		hash := sha256.Sum256(encoded)
		return map[string]any{"candidateSha": head, "contextDigest": modelContext(req.ContextDigest), "reportDigest": hex.EncodeToString(hash[:])}, nil
	}
}

func modelContext(digest string) *string {
	if digest == "" {
		return nil
	}
	return &digest
}
