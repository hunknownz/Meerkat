package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/model"
)

const (
	promptTestDigest  = "sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36"
	promptTestSHA     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	placeholderSHA    = "<final HEAD from git rev-parse HEAD>"
	digestInstruction = "byte-for-byte"
)

func promptRequest(role string) Request {
	return Request{
		Role:          role,
		Profile:       modelProfile("p", "project"),
		TaskBrief:     "bounded scope",
		ContextDigest: promptTestDigest,
		ExpectedSHA:   promptTestSHA,
	}
}

func modelProfile(id, project string) model.Profile {
	return model.Profile{ID: id, ProjectID: project}
}

func exampleJSON(t *testing.T, prompt string) []byte {
	t.Helper()
	const fence = "```json\n"
	i := strings.Index(prompt, fence)
	if i < 0 {
		t.Fatalf("prompt lacks a json example fence: %q", prompt)
	}
	rest := prompt[i+len(fence):]
	j := strings.Index(rest, "\n```")
	if j < 0 {
		t.Fatal("json example fence is not closed")
	}
	return []byte(rest[:j])
}

func TestReportExampleParsesForEachRole(t *testing.T) {
	for _, role := range []string{"developer", "reviewer", "polisher"} {
		p := buildPrompt(promptRequest(role), "/worktree", "/private/report", nil)
		ex := exampleJSON(t, p)
		var anyv any
		if err := json.Unmarshal(ex, &anyv); err != nil {
			t.Fatalf("%s example is not valid JSON: %v\n%s", role, err, ex)
		}
		if !strings.Contains(string(ex), promptTestDigest) {
			t.Fatalf("%s example lost the prefixed digest:\n%s", role, ex)
		}
		if role == "reviewer" {
			if !strings.Contains(string(ex), promptTestSHA) {
				t.Fatalf("reviewer example does not use the exact ExpectedSHA:\n%s", ex)
			}
			if strings.Contains(string(ex), placeholderSHA) {
				t.Fatalf("reviewer example must not carry the final-HEAD placeholder:\n%s", ex)
			}
		} else if !strings.Contains(string(ex), placeholderSHA) {
			t.Fatalf("%s example must instruct substituting the final HEAD:\n%s", role, ex)
		}
		if !strings.Contains(p, digestInstruction) {
			t.Fatalf("%s prompt lacks the byte-for-byte digest instruction:\n%s", role, p)
		}
	}
}

func TestReportExampleNullContextDigest(t *testing.T) {
	for _, role := range []string{"developer", "reviewer", "polisher"} {
		r := promptRequest(role)
		r.ContextDigest = ""
		ex := exampleJSON(t, buildPrompt(r, "/worktree", "/private/report", nil))
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(ex, &raw); err != nil {
			t.Fatalf("%s example is not valid JSON: %v\n%s", role, err, ex)
		}
		if v, ok := raw["contextDigest"]; !ok || string(v) != "null" {
			t.Fatalf("%s example must use null for absent context: %s", role, v)
		}
		if strings.Contains(string(ex), promptTestDigest) {
			t.Fatalf("%s example leaked a digest into the null case:\n%s", role, ex)
		}
	}
}

func TestReviewerExamplePassesStrictValidation(t *testing.T) {
	ex := reportExample("reviewer", promptTestDigest, promptTestSHA)
	rep, ok := validateReport("reviewer", []byte(ex))
	if !ok || rep == nil {
		t.Fatalf("reviewer example must pass strict report validation: %s", ex)
	}
	if rep.CandidateSHA != promptTestSHA || rep.ContextDigest == nil || *rep.ContextDigest != promptTestDigest {
		t.Fatalf("reviewer example lost the exact SHA or digest: %+v", rep)
	}
	exNull := reportExample("reviewer", "", promptTestSHA)
	if rep, ok := validateReport("reviewer", []byte(exNull)); !ok || rep == nil || rep.ContextDigest != nil {
		t.Fatalf("reviewer example with null context must pass validation: %s", exNull)
	}
}

func TestReportPromptEscapesUnicodeAndQuotes(t *testing.T) {
	r := promptRequest("developer")
	r.TaskBrief = `Task: implement "quotes" and backslash \ paths for café & "naïve" runes.`
	r.Context.Text = "Context with \"double quotes\", 'single', backslash \\, tab\t, newline\nand Unicode: ✓ α β γ 日本語."
	p := buildPrompt(r, "/worktree", "/private/report", nil)
	for _, want := range []string{`implement "quotes"`, `backslash \ paths`, "café", `"naïve" runes`, `"double quotes"`, "日本語", promptTestDigest} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt did not preserve %q verbatim:\n%s", want, p)
		}
	}
	ex := exampleJSON(t, p)
	var anyv any
	if err := json.Unmarshal(ex, &anyv); err != nil {
		t.Fatalf("example must stay valid JSON alongside quoted/Unicode brief and context: %v\n%s", err, ex)
	}
}
