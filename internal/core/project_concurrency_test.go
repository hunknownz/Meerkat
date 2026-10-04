package core

import (
	"encoding/json"
	"github.com/hunknownz/Meerkat/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func prepareProject(t *testing.T, e *env, name, provider string) model.Task {
	t.Helper()
	profiles := map[string]string{}
	for role, path := range e.profiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil {
			t.Fatal("profile")
		}
		p["projectId"] = name
		p["provider"] = provider
		raw, _ = json.Marshal(p)
		dst := filepath.Join(e.root, name+"-"+role+".json")
		if os.WriteFile(dst, raw, 0600) != nil {
			t.Fatal("write profile")
		}
		profiles[role] = dst
	}
	return e.prepare(e.worktree(name+"-task"), func(m map[string]any) {
		m["project"] = map[string]any{"id": name, "name": name}
		m["profiles"] = profiles
	})
}

func waitQueueReason(t *testing.T, e *env, id, reason string) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if deref(taskOf(e.state(), id).StateReason) == reason {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queue reason", taskOf(e.state(), id))
}

func TestProjectConcurrencyFairnessAndDynamicSettings(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	a := e.prepare(e.worktree("cap-a"), nil)
	b := e.prepare(e.worktree("cap-b"), nil)
	c := prepareProject(t, e, "other", "other-relay")
	if _, err := e.c.Settings(model.SettingsPatch{ProjectConcurrency: map[string]int{"demo": 1}}); err != nil {
		t.Fatal(err)
	}
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	rb := dispatch(t, e.c, b.ID)
	waitQueueReason(t, e, b.ID, "project_concurrency")
	rc := dispatch(t, e.c, c.ID)
	waitActive(t, e, 2)
	if taskOf(e.state(), b.ID).State != model.TaskQueued {
		t.Fatal("project cap ignored")
	}
	three := 3
	if _, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &three, ProjectConcurrency: map[string]int{"demo": 2}}); err != nil {
		t.Fatal(err)
	}
	waitActive(t, e, 3)
	one := 1
	if _, err := e.c.Settings(model.SettingsPatch{ProjectConcurrency: map[string]int{"demo": one}}); err != nil {
		t.Fatal(err)
	}
	if e.fx.active.Load() != 3 {
		t.Fatal("active work interrupted by lower cap")
	}
	close(e.fx.gate)
	for _, r := range []model.DispatchReceipt{ra, rb, rc} {
		if settled(t, e.c, r.Operation.ID).Tasks[0].TaskState != model.TaskDelivered {
			t.Fatal("delivery")
		}
	}
	set, err := e.c.Settings(model.SettingsPatch{MaxConcurrency: &one})
	if err != nil || set.ProjectConcurrency["demo"] != 1 {
		t.Fatal("omitted caps not preserved", err)
	}
	set, err = e.c.Settings(model.SettingsPatch{ProjectConcurrency: map[string]int{}})
	if err != nil || len(set.ProjectConcurrency) != 0 {
		t.Fatal("clear", err)
	}
	if _, err = e.c.Settings(model.SettingsPatch{ProjectConcurrency: map[string]int{"missing": 1}}); err == nil {
		t.Fatal("unknown project accepted")
	}
}

func TestProviderConcurrencySkipsSaturatedQueue(t *testing.T) {
	e := setup(t)
	e.fx.gate = make(chan struct{})
	a := e.prepare(e.worktree("provider-a"), nil)
	b := prepareProject(t, e, "same-provider", "prov")
	c := prepareProject(t, e, "different-provider", "second-relay")
	if _, err := e.c.Settings(model.SettingsPatch{ProviderConcurrency: map[string]int{"prov": 1}}); err != nil {
		t.Fatal(err)
	}
	ra := dispatch(t, e.c, a.ID)
	waitActive(t, e, 1)
	rb := dispatch(t, e.c, b.ID)
	waitQueueReason(t, e, b.ID, "provider_concurrency")
	rc := dispatch(t, e.c, c.ID)
	waitActive(t, e, 2)
	close(e.fx.gate)
	for _, r := range []model.DispatchReceipt{ra, rb, rc} {
		if settled(t, e.c, r.Operation.ID).Tasks[0].TaskState != model.TaskDelivered {
			t.Fatal("delivery")
		}
	}
	if _, err := e.c.Settings(model.SettingsPatch{ProviderConcurrency: map[string]int{"unknown": 1}}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
