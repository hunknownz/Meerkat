package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hunknownz/Meerkat/internal/model"
)

// DiagnosticCheck contains fixed, credential-free text. Compatibility is not
// authority to run or evidence of an actual per-request gate handshake.
type DiagnosticCheck struct {
	ID              string `json:"id"`
	Status          string `json:"status"` // ok, warning, blocked, not_checked
	Message         string `json:"message"`
	Next            string `json:"next,omitempty"`
	ObservedVersion string `json:"observedVersion,omitempty"`
}

// DiagnosticExecutor is optional; each adapter owns its version/config rules.
type DiagnosticExecutor interface {
	Diagnose(context.Context, model.Profile, bool) []DiagnosticCheck
}

func diag(id, status, message, next string) DiagnosticCheck {
	return DiagnosticCheck{ID: id, Status: status, Message: message, Next: next}
}

// piDiagnosticCommand unwraps only configure.mjs's supported env wrapper. A
// probe omits all configured arguments, including model, prompt and extensions.
func piDiagnosticCommand(p model.Profile) (binary, agentDir string, ok bool) {
	argv := p.PiCommand
	if len(argv) == 0 {
		argv = []string{"pi"}
	}
	if filepath.Base(argv[0]) == "env" {
		if len(argv) < 3 || !strings.HasPrefix(argv[1], "PI_CODING_AGENT_DIR=") {
			return "", "", false
		}
		agentDir = strings.TrimPrefix(argv[1], "PI_CODING_AGENT_DIR=")
		if !filepath.IsAbs(agentDir) || filepath.Clean(agentDir) != agentDir {
			return "", "", false
		}
		argv = argv[2:]
	}
	// Interpreters/wrappers need an adapter-specific probe; do not accidentally
	// report Node's or a shell's version as the executor's version.
	if strings.Contains(argv[0], "=") || strings.HasPrefix(argv[0], "-") {
		return "", "", false
	}
	switch filepath.Base(argv[0]) {
	case "node", "env", "sh", "bash", "zsh", "python", "python3":
		return "", "", false
	}
	return argv[0], agentDir, true
}

func (*Pi) Diagnose(ctx context.Context, p model.Profile, probe bool) []DiagnosticCheck {
	out := []DiagnosticCheck{diag("executor.protocol", "ok", "This adapter implements Pi RPC and the request-budget bridge for Pi 0.99.1.", "")}
	binary, agentDir, ok := piDiagnosticCommand(p)
	if !ok {
		return append(out, diag("executor.command", "blocked", "The configured command wrapper cannot be inspected safely.", "Use the Pi executable directly or configure.mjs's isolated agent-directory wrapper."))
	}
	resolved, err := exec.LookPath(binary)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
	}
	if err != nil {
		out = append(out, diag("executor.command", "blocked", "The configured executor executable was not found.", "Install Pi 0.99.1 or configure its absolute executable path."))
	} else {
		out = append(out, diag("executor.command", "ok", "The executor executable is available.", ""))
	}
	out = append(out, piDiagnosticModel(p, agentDir))
	if !probe {
		out = append(out, diag("executor.version", "not_checked", "The executor was not launched; its installed version is unverified.", "Use --probe-executor with this profile for an isolated version-only check."))
	} else if err == nil {
		version, ok := probePiVersion(ctx, resolved)
		switch {
		case !ok:
			out = append(out, diag("executor.version", "blocked", "The isolated version probe failed, timed out or returned an invalid version.", "Check the configured executable and Node 22 installation."))
		case version != "0.99.1":
			out = append(out, diag("executor.version", "blocked", "The installed executor version is incompatible with the current budget bridge.", "Install the pinned Pi 0.99.1 version."))
			out[len(out)-1].ObservedVersion = version
		default:
			out = append(out, diag("executor.version", "ok", "The isolated executable reports Pi 0.99.1.", ""))
			out[len(out)-1].ObservedVersion = version
		}
	}
	out = append(out, diag("executor.credentials", "not_checked", "Credential values and account balance were not inspected.", "Export the configured authEnv in the service shell before an authorized run."),
		diag("executor.gate", "not_checked", "Static compatibility is not a verified live request-budget gate.", "Meerkat verifies the gate handshake before each run's first model prompt."))
	return out
}

func piDiagnosticModel(p model.Profile, agentDir string) DiagnosticCheck {
	if agentDir == "" {
		return diag("executor.model", "warning", "This profile uses external Pi configuration; its selected model/API was not inspected.", "Use an isolated provider configured with --base-url and --api openai-completions, or verify the existing provider's API.")
	}
	fi, err := os.Lstat(agentDir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 || !ownedByMe(fi) {
		return diag("executor.model", "blocked", "The isolated Pi configuration directory is unsafe or missing.", "Recreate a private executor profile with configure.mjs.")
	}
	f, err := openNoFollow(filepath.Join(agentDir, "models.json"))
	if err != nil {
		return diag("executor.model", "blocked", "The isolated model configuration is missing or unreadable.", "Recreate the selected private model configuration.")
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || !ownedByMe(fi) || fi.Size() > 1<<20 {
		return diag("executor.model", "blocked", "The isolated model configuration is unsafe or oversized.", "Use a private 0600 model configuration owned by the current user.")
	}
	var cfg struct {
		Providers map[string]struct {
			API     string `json:"api"`
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
			Models  []struct {
				ID    string   `json:"id"`
				API   string   `json:"api"`
				Input []string `json:"input"`
			} `json:"models"`
		} `json:"providers"`
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(b) > 1<<20 || json.Unmarshal(b, &cfg) != nil {
		return diag("executor.model", "blocked", "The isolated model configuration is invalid.", "Recreate the selected private model configuration.")
	}
	c, ok := cfg.Providers[p.Provider]
	if !ok {
		return diag("executor.model", "blocked", "The selected provider is absent from the isolated configuration.", "Match the profile's provider and model to models.json.")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || !supportedModelEndpoint(u) || strings.ContainsAny(c.BaseURL, "?#") || model.LooksLikeCredential(c.BaseURL) || c.APIKey != "${"+p.AuthEnv+"}" {
		return diag("executor.model", "blocked", "The endpoint or credential reference does not match supported private configuration.", "Use HTTPS or HTTP on 127.0.0.1/::1, without URL credentials, query or fragment, and an authEnv reference.")
	}
	for _, m := range c.Models {
		if m.ID != p.Model {
			continue
		}
		api := m.API
		if api == "" {
			api = c.API
		}
		if api != "openai-completions" {
			return diag("executor.model", "blocked", "The selected model API is unsupported by the request-budget bridge.", "Select a text HTTP SSE model using openai-completions.")
		}
		for _, input := range m.Input {
			if input != "text" {
				return diag("executor.model", "warning", "The model advertises media input; only text requests are supported by this bridge.", "Keep this task's requests text-only.")
			}
		}
		return diag("executor.model", "ok", "The isolated selected model uses the supported API and environment credential reference.", "")
	}
	return diag("executor.model", "blocked", "The selected model is absent from the isolated configuration.", "Match the profile's model to models.json.")
}

func supportedModelEndpoint(u *url.URL) bool {
	if u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

type versionOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 256 {
		b.overflow = true
		return n, nil
	}
	b.Buffer.Write(p)
	return n, nil
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func probePiVersion(ctx context.Context, binary string) (string, bool) {
	dir, err := os.MkdirTemp("", "meerkat-version-")
	if err != nil {
		return "", false
	}
	defer os.RemoveAll(dir)
	if real, e := filepath.EvalSymlinks(dir); e == nil {
		dir = real
	} else {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "PI_CODING_AGENT_DIR=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "NODE_DISABLE_COMPILE_CACHE=1"}
	ownGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = 100 * time.Millisecond
	var out versionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err = cmd.Run()
	v := strings.TrimSpace(out.String())
	return v, err == nil && ctx.Err() == nil && !out.overflow && versionPattern.MatchString(v)
}
