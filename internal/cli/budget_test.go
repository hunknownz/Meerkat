package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/server"
)

func TestBudgetCLIProposalFileExplicitApplyAndReceipt(t *testing.T) {
	d := privDir(t)
	ln, err := server.ListenUnix(d)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	id := "88000000-0000-4000-8000-000000000001"
	task := "99000000-0000-4000-8000-000000000001"
	p := model.BudgetProposal{BudgetIncrease: model.BudgetIncrease{TaskID: task, AddTokens: 100, Reason: "Finish original scope"}, SchemaVersion: 1, Revision: 0, CurrentTokens: 1000, CurrentWallSeconds: 600, ContractDigest: strings.Repeat("a", 64), EvidenceDigest: strings.Repeat("b", 64)}
	got := make(chan server.Request, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			func(c net.Conn) {
				defer c.Close()
				var r server.Request
				if json.NewDecoder(c).Decode(&r) != nil {
					return
				}
				got <- r
				var v any = p
				if r.Op != "propose-budget" {
					v = model.BudgetDecisionSummary{RequestID: id, Revision: 1, AddTokens: 100, Reason: p.Reason, CreatedAt: "2026-10-03T00:00:00Z"}
				}
				b, _ := json.Marshal(v)
				json.NewEncoder(c).Encode(server.Response{OK: true, Data: b})
			}(c)
		}
	}()
	file := filepath.Join(d, "proposal.json")
	if code, _, e := run(t, "budget", "propose", "--data-dir", d, "--task", task, "--add-tokens", "100", "--reason", p.Reason, "--output", file); code != 0 {
		t.Fatal(code, e)
	}
	if r := <-got; r.Op != "propose-budget" {
		t.Fatal(r.Op)
	}
	fi, _ := os.Stat(file)
	if fi.Mode().Perm() != 0o600 {
		t.Fatal("proposal not private")
	}
	args := []string{"budget", "apply", "--data-dir", d, "--input", file, "--request-id", id, "--authorization", "EXPLICIT_TEST_AUTH"}
	if code, _, _ := run(t, args...); code != ExitUsage {
		t.Fatal("implicit application", code)
	}
	select {
	case <-got:
		t.Fatal("missing --apply reached service")
	default:
	}
	if code, _, e := run(t, append(args, "--apply")...); code != 0 {
		t.Fatal(code, e)
	}
	r := <-got
	var in model.BudgetDecisionInput
	json.Unmarshal(r.Input, &in)
	if r.Op != "apply-budget-decision" || !in.Apply || in.AuthorizationRef != "EXPLICIT_TEST_AUTH" || in.Proposal != p {
		t.Fatal("CLI changed proposal")
	}
	if code, _, e := run(t, "budget", "receipt", "--data-dir", d, "--request-id", id); code != 0 {
		t.Fatal(code, e)
	}
	if r := <-got; r.Op != "budget-decision" || r.RequestID != id {
		t.Fatal(r)
	}
	ln.Close()
	<-done
}
