package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hunknownz/Meerkat/internal/store"
)

func TestBackupRestorePreservesIssueBodies(t *testing.T) {
	const did, tid = "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2"
	d := filepath.Join(privDir(t), "data")
	st, err := store.Open(d)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("prepared update")
	path, hash, err := st.WriteIssueBody(did, body)
	if err != nil {
		t.Fatal(err)
	}
	orig, _, err := st.SaveIssueReceipt(store.IssueReceipt{DeliveryID: did, TaskID: tid, BodyHash: hash, BodyPath: path, State: store.IssuePending})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	bk := filepath.Join(privDir(t), "b.db")
	if c, _, e := run(t, "backup", "--data-dir", d, "--output", bk); c != 0 {
		t.Fatal(e)
	}
	if err := os.Remove(path); err != nil { // original body unavailable
		t.Fatal(err)
	}
	fresh := filepath.Join(privDir(t), "fresh")
	if c, _, e := run(t, "restore", "--backup", bk, "--to", fresh); c != 0 {
		t.Fatal(e)
	}
	rs, err := store.Open(fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	r, err := rs.LoadIssueReceipt(did)
	if err != nil || r.BodyHash != orig.BodyHash || r.State != orig.State || r.Rev != orig.Rev {
		t.Fatal("receipt changed")
	}
	if b, err := rs.ReadIssueBody(r); err != nil || !bytes.Equal(b, body) {
		t.Fatal("body not restored")
	}
	// a second restore into the now-existing directory is refused
	if c, _, _ := run(t, "restore", "--backup", bk, "--to", fresh); c != ExitUsage {
		t.Fatal("restore overwrote")
	}
	// a backup whose bodies cannot be recovered fails explicitly and leaves nothing behind
	bad := filepath.Join(privDir(t), "bad.db")
	if err := os.WriteFile(bad, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(privDir(t), "gone")
	if c, _, _ := run(t, "restore", "--backup", bad, "--to", gone); c != ExitUsage {
		t.Fatal("bad backup restored")
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Fatal("partial restore left behind")
	}
	// backing up a store whose body is missing is refused
	if c, _, _ := run(t, "backup", "--data-dir", d, "--output", filepath.Join(privDir(t), "x.db")); c == 0 {
		t.Fatal("backup without body succeeded")
	}
}
