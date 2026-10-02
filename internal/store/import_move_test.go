package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

const uOther = "00000000-0000-4000-8000-0000000000ee"

// destProject is the preexisting destination record: same logical id/name, new repository, newer times.
func destProject() model.Project {
	return model.Project{ID: uP, Name: "P", Repositories: []string{"/new/repo"}, CreatedAt: "2026-02-01T00:00:00Z", UpdatedAt: "2026-02-02T00:00:00Z"}
}

func seedDest(t *testing.T, s *Store, extra ...model.Project) {
	t.Helper()
	if err := s.Update(func(st *model.State) error {
		st.Projects = append(append(st.Projects, destProject()), extra...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func goodMove() ProjectMove {
	return ProjectMove{ProjectID: uP, FromRepositories: []string{"/repo"}, ToRepositories: []string{"/new/repo"}}
}

func stateCanon(t *testing.T, s *Store) string {
	t.Helper()
	st, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := s.History()
	im, _ := s.Imports()
	return canon(st) + canon(h) + canon(im)
}

func TestImportProjectMoveExplicit(t *testing.T) {
	s, _ := openTemp(t)
	seedDest(t, s)
	from, root := legacySource(t, legacyFixture())
	before := stateCanon(t, s)

	// No option: the changed project still refuses (existing behavior unchanged), atomically.
	plain, err := s.ImportLegacy(from, []string{root})
	if !errors.Is(err, ErrConflict) || len(plain.Conflicts) != 1 || plain.Conflicts[0] != (ImportConflict{"projects", uP}) {
		t.Fatalf("want project conflict, got %v %+v", err, plain.Conflicts)
	}
	if plain.ProjectMoves != nil || stateCanon(t, s) != before {
		t.Fatal("refused import changed state")
	}

	moves := []ProjectMove{goodMove()}
	rep, err := s.ImportLegacyWithOptions(from, []string{root}, ImportOptions{ProjectMoves: moves})
	if err != nil {
		t.Fatalf("move import: %v %+v", err, rep.Conflicts)
	}
	if rep.SourceHash != plain.SourceHash || rep.ImportID == plain.ImportID || rep.ImportID == strings.TrimPrefix(rep.SourceHash, "sha256:")[:32] {
		t.Fatalf("identity must keep sourceHash and bind mapping: %+v", rep)
	}
	if rep.ProjectMoves == nil || rep.ProjectMoves.Count != 1 || len(rep.ProjectMoves.ProjectIDs) != 1 || rep.ProjectMoves.ProjectIDs[0] != uP ||
		rep.ProjectMoves.Digest != projectMovesDigest(moves) || rep.Duplicates["projectMoves"] != 1 || rep.Imported["projects"] != 0 {
		t.Fatalf("move report %+v", rep)
	}
	rb, _ := json.Marshal(rep)
	if strings.Contains(string(rb), "/repo") || strings.Contains(string(rb), "private context") {
		t.Fatalf("report leaks paths or content: %s", rb)
	}
	st, _ := s.Read()
	if len(st.Projects) != 1 || canon(st.Projects[0]) != canon(destProject()) {
		t.Fatalf("destination project must stay intact: %+v", st.Projects)
	}
	// Incoming relationships, ids and history preserved; task source repo/worktree untouched; still non-executable.
	if len(st.Tasks) != 1 || st.Tasks[0].ID != uT || st.Tasks[0].ProjectID != uP || st.Tasks[0].Repository != "/repo" || st.Tasks[0].Worktree != "/wt" ||
		st.Tasks[0].State != model.TaskUnknown || st.Tasks[0].Origin != model.OriginLegacyImport {
		t.Fatalf("task %+v", st.Tasks)
	}
	if len(st.Contexts) != 1 || st.Contexts[0].Digest != contextDigest(st.Contexts[0]) || len(st.Profiles) != 1 || st.Profiles[0].ID != uPr {
		t.Fatal("context/profile not preserved")
	}
	for _, r := range st.Runs {
		if r.ID == uR1 && (r.Usage == nil || *r.Usage.Tokens.Total != 15 || *r.Usage.Tokens.Input != 10 || r.State != model.RunUnknown) {
			t.Fatalf("run usage %+v", r)
		}
		if r.ID == uR2 && r.Usage != nil {
			t.Fatal("unknown usage must stay nil")
		}
	}
	rows, _ := s.MetricsRows()
	for _, r := range rows {
		if r.RunID == uSR && (r.Input == nil || *r.Input != 1 || r.Output != nil || r.CostUsd != nil) {
			t.Fatalf("receipt tokens/fees %+v", r)
		}
	}
	hist, _ := s.History()
	if len(hist) != 4 {
		t.Fatalf("history %d", len(hist))
	}
	for _, h := range hist {
		if h.Executable {
			t.Fatal("history must be non-executable")
		}
	}
	// Private imports payload keeps the original project snapshot and mapping.
	var raw []byte
	if err := s.db.QueryRow("SELECT payload FROM imports WHERE id = ?", rep.ImportID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var rec importRecord
	if err := json.Unmarshal(raw, &rec); err != nil || len(rec.ProjectRelocations) != 1 {
		t.Fatalf("record %s %v", raw, err)
	}
	pr := rec.ProjectRelocations[0]
	if pr.Original.CreatedAt != "x" || pr.Original.UpdatedAt != "x" || !sameList(pr.Original.Repositories, []string{"/repo"}) ||
		canon(pr.Target) != canon(destProject()) || canon(pr.Move) != canon(goodMove()) {
		t.Fatalf("relocation %+v", pr)
	}
	// Sanitized listing never exposes the private payload.
	im, _ := s.Imports()
	ib, _ := json.Marshal(im)
	if len(im) != 1 || strings.Contains(string(ib), "/repo") || strings.Contains(string(ib), "projectRelocations") {
		t.Fatalf("imports leak %s", ib)
	}

	// Exact repeat: no-op.
	after := stateCanon(t, s)
	rep2, err := s.ImportLegacyWithOptions(from, []string{root}, ImportOptions{ProjectMoves: moves})
	if err != nil || !rep2.Repeat || rep2.ImportID != rep.ImportID || stateCanon(t, s) != after {
		t.Fatalf("repeat %+v %v", rep2, err)
	}
	// Same source without the mapping is not silently skipped: it refuses.
	if _, err := s.ImportLegacy(from, []string{root}); !errors.Is(err, ErrConflict) {
		t.Fatalf("no-mapping re-import must refuse, got %v", err)
	}
	if stateCanon(t, s) != after {
		t.Fatal("refused re-import changed state")
	}
}

func TestImportProjectMoveRefusals(t *testing.T) {
	other := model.Project{ID: uOther, Name: "Other", Repositories: []string{"/o"}, CreatedAt: "x", UpdatedAt: "x"}
	cases := map[string]struct {
		moves   []ProjectMove
		dest    func(*model.Project)
		fixture func(map[string]any)
		invalid bool
	}{
		"changed source": {moves: []ProjectMove{{uP, []string{"/elsewhere"}, []string{"/new/repo"}}}},
		"changed target": {moves: []ProjectMove{{uP, []string{"/repo"}, []string{"/another"}}}},
		"target moved":   {moves: []ProjectMove{goodMove()}, dest: func(p *model.Project) { p.Repositories = []string{"/third"} }},
		"name mismatch":  {moves: []ProjectMove{goodMove()}, dest: func(p *model.Project) { p.Name = "Renamed" }},
		"missing":        {moves: []ProjectMove{goodMove(), {"not-incoming", []string{"/a"}, []string{"/b"}}}},
		"duplicate":      {moves: []ProjectMove{goodMove(), goodMove()}, invalid: true},
		"relative":       {moves: []ProjectMove{{uP, []string{"repo"}, []string{"/new/repo"}}}, invalid: true},
		"empty target":   {moves: []ProjectMove{{uP, []string{"/repo"}, []string{}}}, invalid: true},
		"no-op move":     {moves: []ProjectMove{{uP, []string{"/repo"}, []string{"/repo"}}}, invalid: true},
		"unrelated conflict": {moves: []ProjectMove{goodMove()}, fixture: func(m map[string]any) {
			m["projects"] = append(m["projects"].([]any), map[string]any{"id": uOther, "name": "Changed", "repositories": []string{"/o"}, "createdAt": "x", "updatedAt": "x"})
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := openTemp(t)
			dp := destProject()
			if tc.dest != nil {
				tc.dest(&dp)
			}
			if err := s.Update(func(st *model.State) error { st.Projects = []model.Project{dp, other}; return nil }); err != nil {
				t.Fatal(err)
			}
			fx := legacyFixture()
			if tc.fixture != nil {
				tc.fixture(fx)
			}
			from, root := legacySource(t, fx)
			before := stateCanon(t, s)
			rep, err := s.ImportLegacyWithOptions(from, []string{root}, ImportOptions{ProjectMoves: tc.moves})
			if tc.invalid {
				if !errors.Is(err, model.ErrInvalid) {
					t.Fatalf("want invalid, got %v", err)
				}
			} else if !errors.Is(err, ErrConflict) || len(rep.Conflicts) == 0 {
				t.Fatalf("want conflict, got %v %+v", err, rep)
			}
			if stateCanon(t, s) != before {
				t.Fatal("refused import changed state")
			}
			if _, err := s.LoadIssueReceipt(uD); !errors.Is(err, ErrNotFound) {
				t.Fatal("refused import wrote a receipt")
			}
			if _, err := os.Stat(s.bodyPath(uD)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused import published a body")
			}
		})
	}
}

func TestParseProjectMovesStrict(t *testing.T) {
	ok := `[{"projectId":"generic-id","fromRepositories":["/old/repo"],"toRepositories":["/new/repo"]}]`
	if m, err := ParseProjectMoves([]byte(ok)); err != nil || len(m) != 1 || m[0].ProjectID != "generic-id" {
		t.Fatalf("parse %v %+v", err, m)
	}
	bad := []string{
		`[]`, `{}`, `null`, ok + `[]`,
		`[{"projectId":"a","fromRepositories":["/x"],"toRepositories":["/y"],"force":true}]`,
		`[{"projectId":"","fromRepositories":["/x"],"toRepositories":["/y"]}]`,
		`[{"projectId":"a","fromRepositories":["/x/../y"],"toRepositories":["/y"]}]`,
		`[{"projectId":"a","fromRepositories":["/x","/x"],"toRepositories":["/y"]}]`,
		`[{"projectId":"a","fromRepositories":["/x"],"toRepositories":["/y"]},{"projectId":"a","fromRepositories":["/z"],"toRepositories":["/y"]}]`,
		"[" + strings.Repeat(`{"projectId":"a","fromRepositories":["/x"],"toRepositories":["/y"]},`, 2000) + "]",
	}
	for _, b := range bad {
		if _, err := ParseProjectMoves([]byte(b)); err == nil {
			t.Fatalf("accepted %.80s", b)
		}
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "moves.json")
	if err := os.WriteFile(p, []byte(ok), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectMoves(p); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProjectMoves("moves.json"); !errors.Is(err, ErrUnsafeSource) {
		t.Fatal("relative path accepted")
	}
	if _, err := LoadProjectMoves(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file accepted")
	}
	ln := filepath.Join(dir, "link.json")
	os.Symlink(p, ln)
	if _, err := LoadProjectMoves(ln); !errors.Is(err, ErrUnsafeSource) {
		t.Fatal("symlink accepted")
	}
}
