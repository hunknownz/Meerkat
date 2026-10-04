package executor

import (
	"strings"
	"testing"

	"github.com/hunknownz/Meerkat/internal/checkpoint"
)

func TestCheckpointPromptContinuesOriginalTask(t *testing.T) {
	r := Request{Role: "developer", TaskBrief: "original bounded scope"}
	fresh := buildPrompt(r, "/worktree", "/private/report", nil)
	r.Checkpoint = &checkpoint.Binding{}
	resumed := buildPrompt(r, "/worktree", "/private/report", nil)
	if strings.Contains(fresh, "explicit continuation") || !strings.Contains(resumed, "explicit continuation") || !strings.Contains(resumed, r.TaskBrief) || !strings.Contains(resumed, "All previous usage remains charged") {
		t.Fatal("resume prompt lost task or continuation state")
	}
}
