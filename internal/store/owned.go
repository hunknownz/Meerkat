package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hunknownz/Meerkat/internal/model"
)

// checkToken verifies inside tx that token still holds the controller lease.
func checkToken(tx *sql.Tx, token string) error {
	if token == "" {
		return ErrLeaseLost
	}
	var cur string
	err := tx.QueryRow("SELECT token FROM controller_lease WHERE id = 1").Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && cur != token) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("store: read lease failed")
	}
	return nil
}

// UpdateOwned is Update fenced by the controller lease: the token check, fn and the write happen in one
// immediate transaction, so a lost lease can never be followed by a write.
func (s *Store) UpdateOwned(token string, fn func(*model.State) error) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		st, err := readState(tx)
		if err != nil {
			return err
		}
		if err := fn(st); err != nil {
			return err
		}
		return writeState(tx, st)
	})
}

// SetSettingsOwned replaces settings only while token holds the lease.
func (s *Store) SetSettingsOwned(token string, set model.Settings) error {
	if set.MaxConcurrency < model.MinConcurrency || set.MaxConcurrency > model.MaxConcurrency || set.MaxFixRounds < 0 || set.MaxFixRounds > model.MaxFixRoundsLimit {
		return model.Invalidf("settings out of range")
	}
	if set.DefaultProfiles == nil {
		set.DefaultProfiles = map[string]map[string]string{}
	}
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO settings (id, payload) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET payload = excluded.payload", mustJSON(set)); err != nil {
			return fmt.Errorf("store: write settings failed")
		}
		return nil
	})
}

// FinishStopOwned marks a pending stop request processed with the run's actual outcome while token holds the lease.
func (s *Store) FinishStopOwned(token, requestID, outcome string) error {
	return s.tx(func(tx *sql.Tx) error {
		if err := checkToken(tx, token); err != nil {
			return err
		}
		res, err := tx.Exec("UPDATE stop_receipts SET state = ?, processed_at = ?, outcome = ? WHERE request_id = ? AND state = ?",
			model.StopProcessed, now(), outcome, strings.ToLower(requestID), model.StopPending)
		if err != nil {
			return fmt.Errorf("store: finish stop failed")
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNotFound
		}
		return nil
	})
}
