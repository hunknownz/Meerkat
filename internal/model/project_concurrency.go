package model

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
)

// Per-project concurrency bounds mirror the global task limit.
const (
	MinProjectConcurrency = 1
	MaxProjectConcurrency = 4
)

// projectSlug matches a registered project id (lowercase slug, prepared by core).
var projectSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidProjectConcurrencySlug reports whether k is an allowed project id form.
// Persistence and control paths validate slugs without reaching into core.
func ValidProjectConcurrencySlug(k string) bool {
	return len(k) <= 63 && projectSlug.MatchString(k)
}

// ValidProjectConcurrency reports whether every entry is a safe project slug
// with a task limit in 1..MaxProjectConcurrency.
func ValidProjectConcurrency(m map[string]int) bool {
	if len(m) > 128 {
		return false
	}
	for k, v := range m {
		if !ValidProjectConcurrencySlug(k) || v < MinProjectConcurrency || v > MaxProjectConcurrency {
			return false
		}
	}
	return true
}

// CopyProjectConcurrency returns a defensive copy; nil becomes an empty map.
func CopyProjectConcurrency(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// EqualProjectConcurrency compares two caps maps.
func EqualProjectConcurrency(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

var providerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)

// MarshalJSON preserves an explicit empty map (clear), while nil means unchanged.
func (p SettingsPatch) MarshalJSON() ([]byte, error) {
	v := map[string]any{}
	if p.MaxConcurrency != nil {
		v["maxConcurrency"] = *p.MaxConcurrency
	}
	if p.MaxFixRounds != nil {
		v["maxFixRounds"] = *p.MaxFixRounds
	}
	if p.DefaultProfiles != nil {
		v["defaultProfiles"] = p.DefaultProfiles
	}
	if p.ProjectConcurrency != nil {
		v["projectConcurrency"] = p.ProjectConcurrency
	}
	if p.ProviderConcurrency != nil {
		v["providerConcurrency"] = p.ProviderConcurrency
	}
	return json.Marshal(v)
}

// ValidProviderConcurrency accepts frozen profile provider names and task caps.
func ValidProviderConcurrency(m map[string]int) bool {
	if len(m) > 128 {
		return false
	}
	for k, v := range m {
		if !providerName.MatchString(k) || LooksLikeCredential(k) || v < 1 || v > MaxProjectConcurrency {
			return false
		}
	}
	return true
}

// ValidSettingsJSON rejects ambiguous keys and malformed concurrency maps before
// Go's case-insensitive struct decoding or duplicate-map overwrites can apply.
func ValidSettingsJSON(raw []byte) bool {
	if len(raw) > 64<<10 {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		key, ok := t.(string)
		if e != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "maxConcurrency", "maxFixRounds", "defaultProfiles", "projectConcurrency", "providerConcurrency":
		default:
			return false
		}
		var val json.RawMessage
		if d.Decode(&val) != nil {
			return false
		}
		if key == "projectConcurrency" || key == "providerConcurrency" {
			sub := json.NewDecoder(bytes.NewReader(val))
			tok, e = sub.Token()
			if e != nil || tok != json.Delim('{') {
				return false
			}
			entries := map[string]int{}
			for sub.More() {
				k, e := sub.Token()
				name, ok := k.(string)
				if e != nil || !ok {
					return false
				}
				if _, ok := entries[name]; ok {
					return false
				}
				var n int
				if sub.Decode(&n) != nil {
					return false
				}
				entries[name] = n
			}
			if _, e = sub.Token(); e != nil {
				return false
			}
			if key == "projectConcurrency" && !ValidProjectConcurrency(entries) || key == "providerConcurrency" && !ValidProviderConcurrency(entries) {
				return false
			}
		}
	}
	if _, e = d.Token(); e != nil {
		return false
	}
	_, e = d.Token()
	return e == io.EOF
}
