package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

func TestReusableDelegateUsesNamesAndKeepsNestedCustomerTreesClean(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	file := sharedFixtureProfile(t, e)
	profileDir := filepath.Join(e.data, "profiles")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, filepath.Join(profileDir, "shared-pi.json")); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"customer-a", "customer-b"} {
		outer := filepath.Join(e.root, project)
		repo := filepath.Join(outer, "source")
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		business := filepath.Join(outer, "business.md")
		os.WriteFile(business, []byte("PRIVATE-"+project), 0o644)
		sh(t, repo, "init", "-q", "-b", "main")
		if err := commit(repo, "README", "customer baseline"); err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(e.root, project+"-isolated-worktree")
		sh(t, repo, "worktree", "add", "-q", "-b", "codex/delegate", wt)
		input := e.input(wt, func(m map[string]any) {
			m["project"] = map[string]any{"id": project, "name": project}
			m["repository"] = repo
			m["context"] = map[string]any{"version": 1, "text": "PRIVATE-" + project}
			m["profiles"] = map[string]any{"developer": "shared-pi"}
		})
		dry, err := e.c.DryPrepare(input)
		if err != nil || len(dry.Profiles) != 1 || dry.Profiles["developer"].Provider != "prov" {
			t.Fatal("developer-only name selection", dry, err)
		}
		if _, err := e.c.Prepare(input); err == nil || !strings.Contains(err.Error(), "reviewer is required") {
			t.Fatal("workflow did not require its review configuration", err)
		}
		result, err := e.c.Delegate(context.Background(), input)
		if err != nil || result.Mode != "delegate" || len(result.Tasks) != 1 || result.Tasks[0].State != model.TaskFirstDelivery || deref(result.Tasks[0].StateReason) != DelegateCandidate {
			t.Fatal("delegate receipt was not an unreviewed candidate", result, err)
		}
		task := taskOf(e.state(), result.Tasks[0].ID)
		if len(task.ProfileIDs) != 1 || task.ProjectID != project || len(taskProviders(e.state(), task)) != 1 {
			t.Fatal("delegate froze or reserved unused roles")
		}
		if head := sh(t, wt, "rev-parse", "HEAD"); head != deref(task.CandidateSha) || sh(t, wt, "rev-list", "--count", deref(task.BaselineSha)+"..HEAD") != "1" || sh(t, wt, "diff", "--name-only", deref(task.BaselineSha), "HEAD") != "src/a.txt" {
			t.Fatal("candidate evidence did not bind to one scoped commit")
		}
		if sh(t, repo, "status", "--porcelain") != "" {
			t.Fatal("primary customer repository changed")
		}
		entries, _ := os.ReadDir(outer)
		if len(entries) != 2 || entries[0].Name() != "business.md" || entries[1].Name() != "source" {
			t.Fatal("auxiliary materials polluted customer outer tree")
		}
		contents, _ := os.ReadFile(business)
		if string(contents) != "PRIVATE-"+project {
			t.Fatal("customer business contract changed")
		}
	}
	if len(e.fx.reqs) != 2 || len(e.state().Runs) != 2 || len(e.state().Deliveries) != 2 {
		t.Fatal("single delegate executed extra roles")
	}
	a, b := e.fx.reqs[0], e.fx.reqs[1]
	if a.Role != "developer" || b.Role != "developer" || a.Profile.ConfigDigest != b.Profile.ConfigDigest || a.Profile.ID == b.Profile.ID || a.Context.ID == b.Context.ID || a.Session.ID == b.Session.ID || a.Session.File == b.Session.File || a.Context.Text == b.Context.Text {
		t.Fatal("cross-project configuration reuse mixed execution contracts")
	}
	for _, req := range e.fx.reqs {
		for _, file := range []string{req.ReportPath, req.Session.File, req.Profile.ConfigFile} {
			rel, err := filepath.Rel(e.data, file)
			if err != nil || !filepath.IsLocal(rel) {
				t.Fatal("auxiliary material was not in private data-dir", file)
			}
		}
	}
	if snapshot, err := e.c.Snapshot(); err != nil || snapshot.Usage.KnownSubtotal != 200 {
		t.Fatal("candidate/usage query failed", err)
	}
}

func TestLegacyDelegateReservesOnlyItsExecutedProvider(t *testing.T) {
	s := &model.State{Profiles: []model.Profile{{ID: "dev", Provider: "execution"}, {ID: "rev", Provider: "review"}, {ID: "pol", Provider: "polish"}}}
	task := model.Task{Origin: OriginDelegate, ProfileIDs: map[string]string{"developer": "dev", "reviewer": "rev", "polisher": "pol"}}
	if got := taskProviders(s, task); !reflect.DeepEqual(got, []string{"execution"}) {
		t.Fatal("delegate reserved providers it cannot execute", got)
	}
	task.Origin = model.OriginNative
	if got := taskProviders(s, task); !reflect.DeepEqual(got, []string{"execution", "polish", "review"}) {
		t.Fatal("workflow lost role provider reservations", got)
	}
}

func TestReusableDeveloperOnlyDelegateResumeKeepsFrozenSessionAndUsage(t *testing.T) {
	e, f := checkpointEnv(t)
	file := sharedFixtureProfile(t, e)
	task, err := e.c.prepare(e.input(e.worktree("single-profile-resume"), func(m map[string]any) {
		m["profiles"] = map[string]any{"developer": file}
	}), OriginDelegate)
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.c.dispatchLocked(context.Background(), func() ([]string, error) { return []string{task.ID}, nil }, false, false, true)
	if err != nil || first.Tasks[0].State != model.TaskPaused {
		t.Fatal(first, err)
	}
	before := e.state()
	checkpoints, _ := e.st.CheckpointsForTask(task.ID)
	result, err := e.c.ResumeDelegate(context.Background(), task.ID)
	if err != nil || result.Mode != "delegate" || result.Tasks[0].State != model.TaskFirstDelivery || deref(result.Tasks[0].StateReason) != DelegateCandidate {
		t.Fatal(result, err)
	}
	after := e.state()
	if len(before.Tasks) != len(after.Tasks) || len(before.Contexts) != len(after.Contexts) || len(after.Profiles) != 1 || len(after.Runs) != 2 || f.calls != 2 {
		t.Fatal("developer-only continuation changed frozen selection or ran extra roles")
	}
	consumed, _ := e.st.CheckpointsForTask(task.ID)
	if consumed[0].State != "consumed" || consumed[0].SessionID != checkpoints[0].SessionID {
		t.Fatal("developer-only continuation replaced its session")
	}
	if tokens, _ := budgetUse(after, task.ID); tokens != 200 {
		t.Fatal("developer-only continuation lost original usage", tokens)
	}
}

func sharedFixtureProfile(t *testing.T, e *env) string {
	t.Helper()
	file := filepath.Join(e.root, "shared-execution.json")
	if err := os.WriteFile(file, []byte(`{"executor":"fake","provider":"prov","model":"m-1","authEnv":"FAKE_SECRET_ENV","piCommand":["pi-private-cmd"],"instructions":[],"limits":{"maxTokens":1000,"maxWallSeconds":600}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, role := range model.Roles {
		e.profiles[role] = file
	}
	return file
}

func TestReusableProfileAcrossIndependentProjects(t *testing.T) {
	e := setup(t)
	useSessions(t, e)
	file := sharedFixtureProfile(t, e)
	t.Setenv("FAKE_SECRET_ENV", "credential-fixture-value-never-published")
	inputs := map[string][]byte{}
	tasks := map[string]model.Task{}
	for _, project := range []string{"fixture-a", "fixture-b"} {
		repo := filepath.Join(e.root, project)
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		sh(t, repo, "init", "-q", "-b", "main")
		if err := commit(repo, "README", project); err != nil {
			t.Fatal(err)
		}
		wt := filepath.Join(e.root, project+"-worktree")
		sh(t, repo, "worktree", "add", "-q", "-b", "codex/shared-profile", wt)
		inputs[project] = e.input(wt, func(m map[string]any) {
			m["project"] = map[string]any{"id": project, "name": project}
			m["repository"] = repo
			m["context"] = map[string]any{"version": 1, "text": "PRIVATE-" + project}
			m["changeId"] = project + "-change"
		})
		before := e.state()
		dry, err := e.c.DryPrepare(inputs[project])
		if err != nil || !dry.Valid || dry.ProjectID != project || dry.Repository != repo || dry.Worktree != wt {
			t.Fatal("cross-project dry-run", dry, err)
		}
		if !reflect.DeepEqual(before, e.state()) {
			t.Fatal("dry-run changed state")
		}
		out, _ := json.Marshal(dry)
		for _, secret := range []string{"FAKE_SECRET_ENV", "credential-fixture-value", file, "PRIVATE-", "pi-private-cmd"} {
			if strings.Contains(string(out), secret) {
				t.Fatal("dry-run leaked private configuration")
			}
		}
		task, err := e.c.Prepare(inputs[project])
		if err != nil || task.ProjectID != project || deref(task.ChangeID) != project+"-change" {
			t.Fatal("cross-project preparation", task, err)
		}
		tasks[project] = task
	}
	a, b := tasks["fixture-a"], tasks["fixture-b"]
	if a.ContextRef.ID == b.ContextRef.ID || a.Worktree == b.Worktree || a.ProfileIDs["developer"] == b.ProfileIDs["developer"] {
		t.Fatal("project snapshots were shared")
	}
	s := e.state()
	digest := ""
	for _, p := range s.Profiles {
		if !p.Reusable || p.ConfigFile != file || p.ConfigDigest != profileDigest(p) {
			t.Fatal("invalid reusable snapshot", p.ID)
		}
		if digest != "" && digest != p.ConfigDigest {
			t.Fatal("one source config produced different configuration digests")
		}
		digest = p.ConfigDigest
		if _, reason := e.c.verifiedProfile(s, model.DefaultSettings(), tasks[p.ProjectID], p.Role); reason != "" {
			t.Fatal("project-scoped frozen config could not be verified", reason)
		}
	}
	// Attempting to borrow another project's frozen Profile or Context stays blocked.
	borrowed := a
	borrowed.ProfileIDs = map[string]string{"developer": b.ProfileIDs["developer"]}
	if _, reason := e.c.verifiedProfile(s, model.DefaultSettings(), borrowed, "developer"); reason != "profile_missing" {
		t.Fatal("cross-project frozen Profile accepted", reason)
	}
	var reuse map[string]any
	json.Unmarshal(inputs["fixture-b"], &reuse)
	reuse["context"].(map[string]any)["id"] = a.ContextRef.ID
	raw, _ := json.Marshal(reuse)
	if _, err := e.c.DryPrepare(raw); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatal("cross-project context accepted", err)
	}
	res := e.exec(a.ID, b.ID) // local stateful fixture only; no Pi process/provider call
	for _, task := range res.Tasks {
		if task.State != model.TaskDelivered {
			t.Fatal("shared config fixture failed", res)
		}
	}
	owners := map[string]string{}
	for _, req := range e.fx.reqs {
		ss, err := e.st.SessionForRun(req.RunID)
		if err != nil || req.Session == nil || ss.ID != req.Session.ID || ss.TaskID != tasks[req.Profile.ProjectID].ID || req.Context.ProjectID != req.Profile.ProjectID || req.Context.Text != "PRIVATE-"+req.Profile.ProjectID || req.Worktree != tasks[req.Profile.ProjectID].Worktree {
			t.Fatal("request borrowed another project's session/context/worktree", req.RunID, err)
		}
		if prev, ok := owners[req.Session.File]; ok && prev != req.Profile.ProjectID {
			t.Fatal("projects shared a session file")
		}
		owners[req.Session.File] = req.Profile.ProjectID
	}
	if len(owners) != len(e.fx.reqs) || len(owners) != 8 {
		t.Fatal("expected separate development/polish/review histories", len(owners))
	}
	snap, err := e.c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(snap)
	for _, private := range []string{"FAKE_SECRET_ENV", "credential-fixture-value", file, "PRIVATE-", "history.jsonl", "pi-private-cmd", "reusable"} {
		if strings.Contains(string(out), private) {
			t.Fatal("public snapshot leaked private state", private)
		}
	}
}

func TestReusableProfileKeepsLegacyContractsAndRejectsChangedConfig(t *testing.T) {
	e := setup(t)
	legacy := e.prepare(e.worktree("legacy-bound"), nil)
	before := e.state()
	legacyProfile := before.Profiles[0]
	legacyJSON, _ := json.Marshal(legacyProfile)
	if legacyProfile.Reusable || strings.Contains(string(legacyJSON), "reusable") {
		t.Fatal("legacy serialized contract changed")
	}
	legacyDigest := "sha256:" + sha(canonical(toAny(map[string]any{"projectId": "demo", "executor": "fake", "provider": "prov", "model": "m-1", "authEnv": "FAKE_SECRET_ENV", "instructions": []string{}, "limits": model.ProfileLimits{MaxTokens: 1000, MaxWallSeconds: 600}, "piCommand": []string{"pi-private-cmd"}})))
	if legacyProfile.ConfigDigest != legacyDigest {
		t.Fatal("legacy configuration digest changed")
	}
	if _, err := freezeProfile(e.profiles["developer"], "developer", "fixture-b"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatal("legacy binding lost", err)
	}
	shared := sharedFixtureProfile(t, e)
	unbound := e.prepare(e.worktree("new-unbound"), nil)
	after := e.state()
	for i := range before.Profiles {
		if !reflect.DeepEqual(before.Profiles[i], after.Profiles[i]) {
			t.Fatal("existing frozen snapshot changed")
		}
	}
	if !reflect.DeepEqual(legacy, taskOf(after, legacy.ID)) {
		t.Fatal("existing task changed")
	}
	if _, reason := e.c.verifiedProfile(after, model.DefaultSettings(), legacy, "developer"); reason != "" {
		t.Fatal("legacy profile no longer verifies", reason)
	}
	content, _ := os.ReadFile(shared)
	os.WriteFile(shared, []byte(strings.Replace(string(content), `"m-1"`, `"new-model"`, 1)), 0o600)
	if _, reason := e.c.verifiedProfile(after, model.DefaultSettings(), unbound, "developer"); reason != "profile_changed_requires_prepare" {
		t.Fatal("changed reusable config silently adapted", reason)
	}
	// Editing the original file to remove its binding cannot migrate an already frozen task.
	var source map[string]any
	content, _ = os.ReadFile(legacyProfile.ConfigFile)
	json.Unmarshal(content, &source)
	delete(source, "projectId")
	changed, _ := json.Marshal(source)
	os.WriteFile(legacyProfile.ConfigFile, changed, 0o600)
	if _, reason := e.c.verifiedProfile(after, model.DefaultSettings(), legacy, "developer"); reason != "profile_changed_requires_prepare" {
		t.Fatal("changed legacy config silently adapted", reason)
	}
}

func TestExecutionProfileSelectionErrors(t *testing.T) {
	e := setup(t)
	missing := filepath.Join(e.root, "missing.json")
	if _, err := freezeProfile(missing, "developer", "fixture-b"); err == nil || !strings.Contains(err.Error(), "config not found") || !strings.Contains(err.Error(), "reusable") || strings.Contains(err.Error(), "projectId") {
		t.Fatal("missing execution configuration was reported as a project requirement", err)
	}
	file := sharedFixtureProfile(t, e)
	for _, binding := range []string{`""`, `null`, `42`} {
		os.WriteFile(file, []byte(`{"projectId":`+binding+`,"executor":"fake","provider":"prov","model":"m-1","authEnv":"FAKE_SECRET_ENV"}`), 0o600)
		if _, err := InspectProfile(file); err == nil {
			t.Fatal("invalid explicit binding accepted", binding)
		}
	}
}
