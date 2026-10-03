package checkpoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git failed: %v %s", e, b)
	}
	return strings.TrimSpace(string(b))
}
func fixture(t *testing.T) string {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "repo")
	os.Mkdir(repo, 0o755)
	run(t, repo, "init", "-q", "-b", "main")
	run(t, repo, "config", "user.name", "t")
	run(t, repo, "config", "user.email", "t@example.com")
	os.WriteFile(filepath.Join(repo, "existing"), []byte("original"), 0o644)
	run(t, repo, "add", ".")
	run(t, repo, "commit", "-qm", "base")
	wt := filepath.Join(base, "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "codex/checkpoint", wt)
	return wt
}
func TestCaptureBinaryDeletionModesUnicodeAndExactIndex(t *testing.T) {
	wt := fixture(t)
	os.Remove(filepath.Join(wt, "existing"))
	path := "空 格.bin"
	os.WriteFile(filepath.Join(wt, path), []byte{0, 255, 1, 2}, 0o755)
	run(t, wt, "add", "-A")
	os.WriteFile(filepath.Join(wt, path), []byte{0, 255, 3, 4}, 0o755)
	s, raw, e := Capture(wt, []string{"existing", path})
	if e != nil || len(s.Files) != 2 || !strings.Contains(s.IndexPatch, "GIT binary patch") {
		t.Fatal(s, e)
	}
	if _, e := Decode(raw); e != nil {
		t.Fatal(e)
	}
	b := Binding{Digest: Digest(raw), Scope: []string{"existing", path}}
	if e := Verify(wt, b); e != nil {
		t.Fatal(e)
	}
	run(t, wt, "add", "--", path)
	if e := Verify(wt, b); e == nil {
		t.Fatal("changed index accepted with identical working bytes")
	}
}
func TestCaptureRefusesOutOfScopeSymlinksAndOversizedFiles(t *testing.T) {
	for _, kind := range []string{"scope", "symlink", "size"} {
		t.Run(kind, func(t *testing.T) {
			wt := fixture(t)
			scope := []string{"new"}
			switch kind {
			case "scope":
				os.WriteFile(filepath.Join(wt, "outside"), []byte("x"), 0o644)
			case "symlink":
				os.Symlink("existing", filepath.Join(wt, "new"))
			case "size":
				f, e := os.Create(filepath.Join(wt, "new"))
				if e != nil {
					t.Fatal(e)
				}
				f.Truncate(maxFileBytes + 1)
				f.Close()
			}
			if _, _, e := Capture(wt, scope); e == nil {
				t.Fatal("unsafe capture accepted")
			}
		})
	}
}
