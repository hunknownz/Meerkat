package model

import (
	"encoding/json"
	"testing"
)

func TestConcurrencyValidationAndJSON(t *testing.T) {
	for _, raw := range []string{`{}`, `{"projectConcurrency":{}}`, `{"projectConcurrency":{"demo":1},"providerConcurrency":{"relay/vendor":4}}`} {
		if !ValidSettingsJSON([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`{"projectConcurrency":null}`, `{"projectConcurrency":{"demo":1,"demo":2}}`, `{"projectConcurrency":{"demo":1.5}}`, `{"projectConcurrency":{"demo":0}}`, `{"projectConcurrency":{"Demo":2}}`, `{"providerConcurrency":{"relay":5}}`, `{"ProjectConcurrency":{}}`, `{"projectConcurrency":{},"projectConcurrency":{}}`, `{} {}`} {
		if ValidSettingsJSON([]byte(raw)) {
			t.Fatal("accepted", raw)
		}
	}
	a := map[string]int{"demo": 1}
	b := CopyProjectConcurrency(a)
	b["demo"] = 2
	if a["demo"] != 1 || EqualProjectConcurrency(a, b) || !EqualProjectConcurrency(nil, map[string]int{}) {
		t.Fatal("copy or comparison")
	}
}

func TestSettingsPatchEmptyMapsReachAuthority(t *testing.T) {
	raw, err := json.Marshal(SettingsPatch{ProjectConcurrency: map[string]int{}, ProviderConcurrency: map[string]int{}})
	if err != nil || string(raw) != `{"projectConcurrency":{},"providerConcurrency":{}}` {
		t.Fatal(string(raw), err)
	}
}
