package model

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testRunID     = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	testSessionID = "b7e91a2b-3c44-4f7e-8a27-5c9f01234567"
	testRequestID = "d95c0f43-9a2e-4ab1-8d63-2f1ec0729add"
	testTaskID    = "4c8a0f64-9e1d-42b3-8c57-3a0d6e2f91b4"
	testAuthRef   = "runner-trusted-ref"
	testMessage   = "Add a heading to the report and rerun the suite."
)

// legacyWrapUpJSON is the exact historical serialized shape, prior to the
// optional Kind and Message fields.
const legacyWrapUpJSON = `{"runId":"f47ac10b-58cc-4372-a567-0e02b2c3d479","sessionId":"b7e91a2b-3c44-4f7e-8a27-5c9f01234567","requestId":"d95c0f43-9a2e-4ab1-8d63-2f1ec0729add","authorizationRef":"runner-trusted-ref","apply":true}`

func instructionInput(m string) WrapUpInput {
	return WrapUpInput{
		RunID:            testRunID,
		SessionID:        testSessionID,
		RequestID:        testRequestID,
		AuthorizationRef: testAuthRef,
		Apply:            true,
		Kind:             "instruction",
		Message:          m,
	}
}

func wrapUpInput() WrapUpInput {
	in := instructionInput("")
	in.Kind = ""
	return in
}

func TestLegacyWrapUpJSONCompatibility(t *testing.T) {
	var in WrapUpInput
	if err := json.Unmarshal([]byte(legacyWrapUpJSON), &in); err != nil {
		t.Fatalf("unmarshal legacy wrap_up JSON: %v", err)
	}
	if in.Kind != "" || in.Message != "" {
		t.Fatalf("legacy JSON must decode to blank Kind/Message, got Kind=%q Message=%q", in.Kind, in.Message)
	}
	if got := ControlKind(in); got != "wrap_up" {
		t.Fatalf("ControlKind(legacy) = %q, want wrap_up", got)
	}
	if !ValidWrapUpInput(in) {
		t.Fatal("ValidWrapUpInput(legacy input) = false, want true")
	}
	if ValidInstructionInput(in) {
		t.Fatal("ValidInstructionInput(legacy input) = true, want false")
	}

	// Omitted fields stay omitted on re-serialization so historical digests are stable.
	round, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal legacy wrap_up input: %v", err)
	}
	if string(round) != legacyWrapUpJSON {
		t.Fatalf("round-trip JSON changed: got %s want %s", round, legacyWrapUpJSON)
	}
}

func TestControlKindDefault(t *testing.T) {
	if got := ControlKind(wrapUpInput()); got != "wrap_up" {
		t.Fatalf("ControlKind(blank Kind) = %q, want wrap_up", got)
	}
	if got := ControlKind(instructionInput(testMessage)); got != "instruction" {
		t.Fatalf("ControlKind(instruction) = %q, want instruction", got)
	}
}

func TestValidWrapUpInput(t *testing.T) {
	historical := wrapUpInput()
	if !ValidWrapUpInput(historical) {
		t.Fatal("ValidWrapUpInput(historical) = false, want true")
	}
	explicit := historical
	explicit.Kind = "wrap_up"
	if !ValidWrapUpInput(explicit) {
		t.Fatal("ValidWrapUpInput(explicit wrap_up) = false, want true")
	}
	withMessage := historical
	withMessage.Message = "please do this"
	if ValidWrapUpInput(withMessage) {
		t.Fatal("ValidWrapUpInput with Message = true, want false")
	}
	asInstruction := instructionInput(testMessage)
	if ValidWrapUpInput(asInstruction) {
		t.Fatal("ValidWrapUpInput(instruction) = true, want false")
	}
}

func TestValidInstructionInputBase(t *testing.T) {
	if !ValidInstructionInput(instructionInput(testMessage)) {
		t.Fatal("ValidInstructionInput(valid) = false, want true")
	}
	cases := []struct {
		name string
		mut  func(*WrapUpInput)
	}{
		{"wrong kind", func(in *WrapUpInput) { in.Kind = "wrap_up" }},
		{"blank kind", func(in *WrapUpInput) { in.Kind = "" }},
		{"bad run id", func(in *WrapUpInput) { in.RunID = "not-a-uuid" }},
		{"bad session id", func(in *WrapUpInput) { in.SessionID = "12345" }},
		{"bad request id", func(in *WrapUpInput) { in.RequestID = "" }},
		{"apply false", func(in *WrapUpInput) { in.Apply = false }},
		{"blank authorization", func(in *WrapUpInput) { in.AuthorizationRef = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := instructionInput(testMessage)
			tc.mut(&in)
			if ValidInstructionInput(in) {
				t.Fatalf("ValidInstructionInput(%s) = true, want false", tc.name)
			}
		})
	}
}

func TestInstructionMessageBounds(t *testing.T) {
	if !ValidInstructionMessage("a") {
		t.Fatal("ValidInstructionMessage(single rune) = false, want true")
	}
	if ValidInstructionMessage("   ") {
		t.Fatal("ValidInstructionMessage(blank) = true, want false")
	}
	if ValidInstructionMessage("") {
		t.Fatal("ValidInstructionMessage(empty) = true, want false")
	}

	// Codepoint bound: exactly 4000 ASCII runes allowed, 4001 rejected.
	if !ValidInstructionMessage(strings.Repeat("a", 4000)) {
		t.Fatal("ValidInstructionMessage(4000 runes) = false, want true")
	}
	if ValidInstructionMessage(strings.Repeat("a", 4001)) {
		t.Fatal("ValidInstructionMessage(4001 runes) = true, want false")
	}

	// Multibyte runes: 4000 three-byte runes (12000 bytes) allowed.
	if !ValidInstructionMessage(strings.Repeat("€", 4000)) {
		t.Fatal("ValidInstructionMessage(4000 x 3-byte runes) = false, want true")
	}

	// Byte bound: 4000 four-byte runes is exactly 16000 bytes and allowed;
	// any longer message exceeds both bounds and is rejected.
	sixteenK := strings.Repeat("\U00010437", 4000)
	if len(sixteenK) != 16000 {
		t.Fatalf("four-byte rune fixture = %d bytes, want 16000", len(sixteenK))
	}
	if !ValidInstructionMessage(sixteenK) {
		t.Fatal("ValidInstructionMessage(16000 bytes) = false, want true")
	}
	if ValidInstructionMessage(sixteenK + "x") {
		t.Fatal("ValidInstructionMessage(16001 bytes) = true, want false")
	}
}

func TestInstructionMessageTextRules(t *testing.T) {
	tooLong := []struct {
		name string
		msg  string
	}{
		{"invalid utf8", "\xff\xfe"},
		{"control NUL", "do this\x00now"},
		{"control BEL", "do\x07this"},
		{"delete", "do\u007fthis"},
		{"credential sk", "use sk-ant-test-abcdefghijklmnopqrstuvwxyz1234"},
		{"credential gh", "token ghp_abcdefghijklmnopqrstuvwxyz123456"},
		{"credential aws", "AKIAABCDEFGHIJKLMNOP"},
	}
	for _, tc := range tooLong {
		t.Run(tc.name, func(t *testing.T) {
			if ValidInstructionMessage(tc.msg) {
				t.Fatalf("ValidInstructionMessage(%s) = true, want false", tc.name)
			}
			if ValidInstructionInput(instructionInput(tc.msg)) {
				t.Fatalf("ValidInstructionInput(%s) = true, want false", tc.name)
			}
		})
	}

	allowed := []string{
		"line one\nline two",
		"tab\tseparated",
		"mixed\n\tindent",
	}
	for _, msg := range allowed {
		if !ValidInstructionMessage(msg) {
			t.Fatalf("ValidInstructionMessage(%q) = false, want true", msg)
		}
	}
}

func validReceipt(kind string) ControlReceipt {
	createdAt := "2025-01-02T03:04:05.123456789Z"
	updatedAt := "2025-01-02T03:04:06Z"
	queued := "queued"
	sessionID := testSessionID
	return ControlReceipt{
		RequestID:   testRequestID,
		TaskID:      testTaskID,
		RunID:       testRunID,
		SessionID:   &sessionID,
		Kind:        kind,
		State:       ControlAcknowledged,
		Disposition: &queued,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		RunState:    RunRunning,
	}
}

func TestValidControlReceiptKinds(t *testing.T) {
	cases := []struct {
		name string
		kind string
		want bool
	}{
		{"wrap_up ack", "wrap_up", true},
		{"instruction ack", "instruction", true},
		{"stop ack", "stop", false}, // identical-state rule: stop forbids disposition/ack
		{"unknown kind", "reboot", false},
		{"blank kind", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidControlReceipt(validReceipt(tc.kind)); got != tc.want {
				t.Fatalf("ValidControlReceipt(%s) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

func TestInstructionReceiptAcknowledgementRules(t *testing.T) {
	base := validReceipt("instruction")
	if !ValidControlReceipt(base) {
		t.Fatal("instruction receipt acknowledged with disposition = false, want true")
	}

	// Acknowledgement demands a disposition and no reason, identical for wrap_up.
	ack := base
	ack.Disposition = nil
	if ValidControlReceipt(ack) {
		t.Fatal("instruction ack without disposition = true, want false")
	}

	// A non-acknowledged kind with a disposition is invalid for instruction too.
	dispositioned := base

	// A non-acknowledged kind with a disposition is invalid for instruction too.
	dispositioned.State = ControlAccepted
	if ValidControlReceipt(dispositioned) {
		t.Fatal("instruction accepted with disposition = true, want false")
	}

	// Accepted state requires no reason.
	accepted := base
	accepted.State = ControlAccepted
	accepted.Disposition = nil
	if !ValidControlReceipt(accepted) {
		t.Fatal("instruction accepted without reason/disposition = false, want true")
	}
	accepted.Reason = strPtr("executor_refused")
	if ValidControlReceipt(accepted) {
		t.Fatal("instruction accepted with reason = true, want false")
	}

	// Rejected/sending states fall through to the final reason validation.
	sending := base
	sending.State = ControlSending
	sending.Disposition = nil
	if !ValidControlReceipt(sending) {
		t.Fatal("instruction sending = false, want true")
	}
	sending.Reason = strPtr("protocol_reply_unknown")
	if ValidControlReceipt(sending) {
		t.Fatal("instruction sending with reason = true, want false")
	}

	rejected := base
	rejected.State = ControlRejected
	rejected.Disposition = nil
	rejected.Reason = strPtr("unsupported_control")
	if !ValidControlReceipt(rejected) {
		t.Fatal("instruction rejected with valid reason = false, want true")
	}
	rejected.Reason = strPtr("bogus reason")
	if ValidControlReceipt(rejected) {
		t.Fatal("instruction rejected with unknown reason = true, want false")
	}
	rejected.Reason = nil
	if ValidControlReceipt(rejected) {
		t.Fatal("instruction rejected without reason = true, want false")
	}
}

func strPtr(s string) *string { return &s }
