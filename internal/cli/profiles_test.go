package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileDiscoveryIsReadOnlySafeAndProvidesStableNames(t *testing.T) {
	dir := filepath.Join(privDir(t), "data")
	code, out, errOut := run(t, "profile", "list", "--data-dir", dir)
	if code != 0 || !strings.Contains(out, `"data": []`) {
		t.Fatal(code, out, errOut)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("empty discovery created data-dir/database")
	}
	profiles := filepath.Join(dir, "profiles")
	os.MkdirAll(profiles, 0o700)
	files := map[string]string{
		"shared-pi.json": `{"executor":"pi","provider":"fixture","model":"test","authEnv":"PRIVATE_REF","piCommand":["private-command"]}`,
		"legacy.json":    `{"projectId":"example","executor":"pi","provider":"fixture","model":"test","authEnv":"PRIVATE_REF"}`,
		"invalid.json":   `{"authEnv":"sk-credential-value-never-published"}`,
	}
	for name, text := range files {
		os.WriteFile(filepath.Join(profiles, name), []byte(text), 0o600)
	}
	code, out, errOut = run(t, "profile", "list", "--data-dir", dir)
	if code != 0 {
		t.Fatal(out, errOut)
	}
	var result struct {
		Data []struct {
			ID, Binding, ProjectID string
			Valid                  bool
		}
	}
	if json.Unmarshal([]byte(out), &result) != nil || len(result.Data) != 3 || result.Data[0].ID != "invalid" || result.Data[0].Valid || result.Data[1].Binding != "project" || result.Data[1].ProjectID != "example" || result.Data[2].ID != "shared-pi" || result.Data[2].Binding != "reusable" || !result.Data[2].Valid {
		t.Fatal("discovery metadata", out)
	}
	for _, secret := range []string{"PRIVATE_REF", "private-command", "sk-credential", "authEnv", "configFile", "piCommand", dir} {
		if strings.Contains(out+errOut, secret) {
			t.Fatal("discovery leaked private config", secret)
		}
	}
	for file, text := range files {
		got, _ := os.ReadFile(filepath.Join(profiles, file))
		if string(got) != text {
			t.Fatal("discovery modified a Profile")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "meerkat.db")); !os.IsNotExist(err) {
		t.Fatal("discovery created a database")
	}
}
