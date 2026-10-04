package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

type instructionFake struct {
	fakeCore
	received []model.WrapUpInput
}

func (f *instructionFake) RequestInstruction(in model.WrapUpInput) (model.ControlReceipt, error) {
	if !model.ValidInstructionInput(in) {
		return model.ControlReceipt{}, model.Invalidf("invalid instruction")
	}
	f.received = append(f.received, in)
	return model.ControlReceipt{RequestID: in.RequestID, RunID: in.RunID, Kind: "instruction", State: model.ControlAccepted}, nil
}
func TestBrowserInstructionRequiresOwnerAndAllowsUnicodeBound(t *testing.T) {
	f := &instructionFake{}
	svc, e := New(f, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Shutdown()
	srv, host := startHTTP(t, svc)
	url := srv.URL + "/api/workflow/runs/11111111-1111-4111-8111-111111111111/instruction"
	in := map[string]string{"sessionId": "22222222-2222-4222-8222-222222222222", "requestId": "33333333-3333-4333-8333-333333333333", "message": strings.Repeat("🌱", 4000)}
	b, _ := json.Marshal(in)
	res, _ := do(t, "POST", url, string(b), map[string]string{"Content-Type": "application/json"})
	if res.StatusCode != http.StatusForbidden || len(f.received) != 0 {
		t.Fatal("unauthorized instruction", res.StatusCode)
	}
	h := map[string]string{"Content-Type": "application/json", "Origin": "http://" + host, "X-Meerkat-Token": svc.Token()}
	res, m := do(t, "POST", url, string(b), h)
	if res.StatusCode != 200 || m["ok"] != true || len(f.received) != 1 || f.received[0].Message != in["message"] {
		t.Fatal("Unicode bound rejected", res.StatusCode, m)
	}
	in["message"] += "a"
	b, _ = json.Marshal(in)
	res, _ = do(t, "POST", url, string(b), h)
	if res.StatusCode != 400 || len(f.received) != 1 {
		t.Fatal("overlong accepted", res.StatusCode)
	}
	for _, bad := range []string{`{"message":"one","message":"two"}`, `{"Message":"alias"}`, `{"message":null}`, `{} {}`} {
		res, _ = do(t, "POST", url, bad, h)
		if res.StatusCode != 400 {
			t.Fatal("bad instruction", bad, res.StatusCode)
		}
	}
}

func TestUnixInstructionSharesStrictInputContract(t *testing.T) {
	f := &instructionFake{}
	svc, _ := New(f, nil, nil)
	defer svc.Shutdown()
	in := model.WrapUpInput{RunID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", RequestID: "33333333-3333-4333-8333-333333333333", Message: strings.Repeat("🌱", 4000), Kind: "instruction", AuthorizationRef: "Human UI action", Apply: true}
	b, _ := json.Marshal(in)
	if r := svc.Do(Request{Op: "request-instruction", Input: b}); !r.OK {
		t.Fatal(r)
	}
	b = []byte(strings.Replace(string(b), `"message":`, `"message":"first","message":`, 1))
	if r := svc.Do(Request{Op: "request-instruction", Input: b}); r.OK {
		t.Fatal("duplicate accepted")
	}
}
