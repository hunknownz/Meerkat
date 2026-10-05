//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectProfileAllowsPrivateReadOnlyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(p, []byte(`{"projectId":"example","provider":"fixture","model":"fixture","authEnv":"FIXTURE_KEY"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectProfile(p); err != nil {
		t.Fatalf("private read-only profile rejected: %v", err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectProfile(p); err == nil {
		t.Fatal("group-readable profile accepted")
	}
}
