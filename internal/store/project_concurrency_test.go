package store

import (
	"path/filepath"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestConcurrencySettingsPersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := model.DefaultSettings()
	set.ProjectConcurrency = map[string]int{"demo": 1}
	set.ProviderConcurrency = map[string]int{"relay": 2}
	if err = s.SetSettings(set); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetSettings()
	if err != nil || !model.EqualProjectConcurrency(set.ProjectConcurrency, got.ProjectConcurrency) || !model.EqualProjectConcurrency(set.ProviderConcurrency, got.ProviderConcurrency) {
		t.Fatal(got, err)
	}
	set.ProjectConcurrency["demo"] = 0
	if s.SetSettings(set) == nil {
		t.Fatal("invalid cap saved")
	}
}
