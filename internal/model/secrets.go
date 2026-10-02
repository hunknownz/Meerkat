package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalid marks validation failures. Messages never include the offending value.
var ErrInvalid = errors.New("invalid")

type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }
func (e *invalidError) Unwrap() error { return ErrInvalid }

func errInvalid(msg string) error { return &invalidError{msg: msg} }

// Invalidf returns an ErrInvalid error. Callers must not format secret values or file contents into it.
func Invalidf(format string, a ...any) error { return errInvalid(fmt.Sprintf(format, a...)) }

// SecretKeyRE matches nested keys that must never be persisted inside free-form objects
// (workflow/store.mjs SECRET_KEY_RE).
var SecretKeyRE = regexp.MustCompile(`(?i)^(api[_-]?key|secret|password|passwd|authorization|auth|credentials?|env|authEnv|cookie|transcript|prompt|stdout|stderr|rawError|token|access[_-]?token|refresh[_-]?token|private[_-]?key)$`)

// credentialRE matches high-confidence credential values.
var credentialRE = regexp.MustCompile(`(sk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[abprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|AIza[0-9A-Za-z_-]{35}|(?i:bearer)\s+[A-Za-z0-9._~+/-]{20,}=*)`)

// envNameRE is a plausible environment variable name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// LooksLikeCredential reports whether s contains a high-confidence credential value.
func LooksLikeCredential(s string) bool { return credentialRE.MatchString(s) }

// ValidEnvName reports whether s is an environment variable name (a reference, not a value).
func ValidEnvName(s string) bool { return envNameRE.MatchString(s) && !LooksLikeCredential(s) }

// CheckFreeForm rejects secret-like keys anywhere in a decoded JSON value and credential-like strings.
// where names the location (field path), never the value.
func CheckFreeForm(v any, where string) error {
	return checkValue(v, where, 0)
}

func checkValue(v any, where string, depth int) error {
	if depth > 32 {
		return Invalidf("%s is nested too deeply", where)
	}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if SecretKeyRE.MatchString(k) {
				return Invalidf("%s contains a forbidden secret-like key", where)
			}
			if err := checkValue(x, where+"."+safeKey(k), depth+1); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := checkValue(x, fmt.Sprintf("%s[%d]", where, i), depth+1); err != nil {
				return err
			}
		}
	case string:
		if LooksLikeCredential(t) {
			return Invalidf("%s contains a credential-like value", where)
		}
	}
	return nil
}

// CheckRawFreeForm decodes raw JSON and applies CheckFreeForm. Empty raw is accepted.
func CheckRawFreeForm(raw json.RawMessage, where string) error {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return Invalidf("%s is not valid JSON", where)
	}
	return CheckFreeForm(v, where)
}

// CheckStrings rejects credential-like values in plain strings.
func CheckStrings(where string, values ...string) error {
	for _, s := range values {
		if LooksLikeCredential(s) {
			return Invalidf("%s contains a credential-like value", where)
		}
	}
	return nil
}

func safeKey(k string) string {
	if len(k) > 40 {
		k = k[:40]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, k)
}
