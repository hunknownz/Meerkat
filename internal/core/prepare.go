package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hunknownz/Meerkat/internal/model"
	"github.com/hunknownz/Meerkat/internal/platform"
)

// MaxInput is the strict prepare input limit.
const MaxInput = 1 << 20

const (
	maxProfileWall   = 86_400
	maxProfileTokens = 1_000_000_000
	maxConfigFile    = 64 << 10
)

var (
	uuidRE      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	slugRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	hashRE      = regexp.MustCompile(`^(?:sha256:)?[0-9a-f]{64}$`)
	changeIDRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	issuePathRE = regexp.MustCompile(`^/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/issues/[1-9][0-9]{0,9}$`)
	plainRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
	secretSegRE = regexp.MustCompile(`(?i)^(?:\.env(?:\..*)?|\.git|\.pi-developer|\.ssh|\.aws|\.gnupg|\.npmrc|\.pypirc|\.netrc|id_rsa.*|id_ed25519.*|.*\.pem|.*\.key|.*\.p12|.*\.pfx|credentials(?:\..*)?|secrets?(?:\..*)?)$`)
)

var invalid = model.Invalidf

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// canonical renders sorted-key JSON compatible with the 0.2.x JS canonical().
func canonical(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = jsonStr(k) + ":" + canonical(t[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = canonical(x)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case string:
		return jsonStr(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func jsonStr(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	out := strings.TrimSuffix(b.String(), "\n")
	return strings.NewReplacer(`\u2028`, "\u2028", `\u2029`, "\u2029").Replace(out)
}

func toAny(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func hasCtl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func str(v, where string, max int, multiline bool) (string, error) {
	t := v
	if !multiline {
		t = strings.TrimSpace(v)
	}
	if !utf8.ValidString(t) || utf8.RuneCountInString(t) < 1 || utf8.RuneCountInString(t) > max || strings.TrimSpace(t) == "" {
		return "", invalid("%s must be 1..%d characters", where, max)
	}
	if !multiline && hasCtl(t) {
		return "", invalid("%s must not contain control characters", where)
	}
	if strings.ContainsRune(t, 0) || model.LooksLikeCredential(t) {
		return "", invalid("%s contains a credential-like or invalid value", where)
	}
	return t, nil
}

// Input is the strict prepare input (0.2.x task-input JSON plus optional changeId).
type Input struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Repository   string            `json:"repository"`
	Worktree     string            `json:"worktree"`
	Title        string            `json:"title"`
	Goal         string            `json:"goal"`
	Scope        []string          `json:"scope"`
	Acceptance   []string          `json:"acceptance"`
	Context      *ContextInput     `json:"context"`
	Profiles     map[string]string `json:"profiles"`
	Dependencies []string          `json:"dependencies"`
	IssueRef     *issueInput       `json:"issueRef"`
	Budget       *budgetInput      `json:"budget"`
	ChangeID     *string           `json:"changeId"`
}

// ContextInput is the context part of a prepare input.
type ContextInput struct {
	ID      string        `json:"id"`
	Version int           `json:"version"`
	Text    string        `json:"text"`
	Sources []sourceInput `json:"sources"`
}

type sourceInput struct {
	URL   *string `json:"url"`
	Title *string `json:"title"`
	Hash  *string `json:"hash"`
}

type issueInput struct {
	URL       string  `json:"url"`
	Title     string  `json:"title"`
	UpdatedAt *string `json:"updatedAt"`
	BodyHash  *string `json:"bodyHash"`
}

type budgetInput struct {
	Mode           *string              `json:"mode"`
	MaxTokens      *int64               `json:"maxTokens"`
	MaxWallSeconds *int64               `json:"maxWallSeconds"`
	MaxFixRounds   *int                 `json:"maxFixRounds"`
	StageReserves  *model.StageReserves `json:"stageReserves"`
}

// decodeStrict decodes exactly one JSON value into v, refusing unknown keys and trailing data.
func decodeStrict(raw []byte, v any, max int) error {
	if len(raw) > max {
		return invalid("input exceeds %d bytes", max)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("input is not valid strict JSON (unknown field or wrong type)")
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid("input has trailing data")
	}
	return nil
}

// NormalizeScope validates explicit relative scope paths.
func NormalizeScope(scope []string) ([]string, error) {
	if len(scope) < 1 || len(scope) > 50 {
		return nil, invalid("scope must list 1..50 relative paths")
	}
	set := map[string]bool{}
	for i, raw := range scope {
		p, err := str(raw, fmt.Sprintf("scope[%d]", i), 300, false)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(p, `\*?[]{}`) {
			return nil, invalid("scope[%d] must be an explicit path (no globs or backslashes)", i)
		}
		if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || (len(p) > 1 && p[1] == ':') {
			return nil, invalid("scope[%d] must be relative", i)
		}
		segs := []string{}
		for _, s := range strings.Split(p, "/") {
			if s == ".." {
				return nil, invalid("scope[%d] must not traverse outside the repository", i)
			}
			if s != "" && s != "." {
				if secretSegRE.MatchString(s) {
					return nil, invalid("scope[%d] names an unsafe path segment", i)
				}
				segs = append(segs, s)
			}
		}
		if len(segs) == 0 {
			return nil, invalid("scope[%d] must name a path inside the repository", i)
		}
		set[path.Clean(strings.Join(segs, "/"))] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func normIssue(in *issueInput) (*model.IssueRef, error) {
	if in == nil {
		return nil, nil
	}
	raw, err := str(in.URL, "issueRef.url", 500, false)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Port() != "" || u.Hostname() == "" || u.Opaque != "" {
		return nil, invalid("issueRef.url must be https without credentials, port, query or fragment")
	}
	if !issuePathRE.MatchString(u.Path) || u.RawPath != "" {
		return nil, invalid("issueRef.url must look like https://host/owner/repo/issues/<number>")
	}
	title, err := str(in.Title, "issueRef.title", 300, false)
	if err != nil {
		return nil, err
	}
	out := &model.IssueRef{URL: "https://" + strings.ToLower(u.Hostname()) + u.Path, Title: title}
	if in.UpdatedAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *in.UpdatedAt)
		if err != nil {
			return nil, invalid("issueRef.updatedAt must be an ISO timestamp")
		}
		out.UpdatedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if in.BodyHash != nil {
		if !hashRE.MatchString(*in.BodyHash) {
			return nil, invalid("issueRef.bodyHash must be a sha256 hex digest")
		}
		out.BodyHash = *in.BodyHash
	}
	return out, nil
}

func normBudget(in *budgetInput) (*model.Budget, error) {
	b := model.DefaultBudget()
	b.Mode = "monitor"
	if in == nil {
		return &b, nil
	}
	if in.MaxTokens != nil {
		if *in.MaxTokens < 1 || *in.MaxTokens > 10_000_000 {
			return nil, invalid("budget.maxTokens must be an integer 1..10000000")
		}
		b.MaxTokens = *in.MaxTokens
		// Explicit legacy maxTokens continues to select a hard task cap.
		b.Mode = ""
	}
	if in.Mode != nil {
		if *in.Mode != "monitor" && *in.Mode != "enforce" {
			return nil, invalid("budget.mode must be monitor or enforce")
		}
		b.Mode = *in.Mode
	}
	if in.MaxWallSeconds != nil {
		if *in.MaxWallSeconds < 1 || *in.MaxWallSeconds > 86_400 {
			return nil, invalid("budget.maxWallSeconds must be an integer 1..86400")
		}
		b.MaxWallSeconds = *in.MaxWallSeconds
	}
	if in.MaxFixRounds != nil {
		if *in.MaxFixRounds < 0 || *in.MaxFixRounds > model.MaxFixRoundsLimit {
			return nil, invalid("budget.maxFixRounds must be an integer 0..2")
		}
		b.MaxFixRounds = *in.MaxFixRounds
	}
	if in.StageReserves != nil {
		r := *in.StageReserves
		for _, n := range []int64{r.ReviewTokens, r.FixTokens, r.PolishTokens, r.WrapUpTokens} {
			if n < 0 || n > b.MaxTokens {
				return nil, invalid("stage reserve tokens must be within task authorization")
			}
		}
		if r.WrapUpSeconds < 0 || r.WrapUpSeconds >= b.MaxWallSeconds ||
			b.HardTokenCap() && int64(2+b.MaxFixRounds)*r.ReviewTokens+int64(b.MaxFixRounds)*r.FixTokens+r.PolishTokens+r.WrapUpTokens >= b.MaxTokens {
			return nil, invalid("stage reserves leave no development allowance")
		}
		b.StageReserves = &r
	}
	return &b, nil
}

// ContextDigest is sha256 of canonical {sources, text}.
func ContextDigest(text string, sources []model.ContextSource) string {
	if sources == nil {
		sources = []model.ContextSource{}
	}
	return "sha256:" + sha(canonical(map[string]any{"text": text, "sources": toAny(sources)}))
}

func normContext(c *ContextInput) (model.Context, error) {
	var out model.Context
	if c == nil {
		return out, invalid("context is required")
	}
	if c.ID != "" && !uuidRE.MatchString(c.ID) {
		return out, invalid("context.id must be a lowercase UUID")
	}
	if c.Version < 1 || c.Version > 10_000 {
		return out, invalid("context.version must be an integer 1..10000")
	}
	text, err := str(c.Text, "context.text", 200_000, true)
	if err != nil {
		return out, err
	}
	if len(c.Sources) > 50 {
		return out, invalid("context.sources must have at most 50 entries")
	}
	sources := []model.ContextSource{}
	for i, s := range c.Sources {
		var o model.ContextSource
		w := fmt.Sprintf("context.sources[%d]", i)
		if s.URL != nil {
			raw, err := str(*s.URL, w+".url", 500, false)
			if err != nil {
				return out, err
			}
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Opaque != "" {
				return out, invalid("%s.url must be https without credentials", w)
			}
			o.URL = "https://" + u.Host + u.EscapedPath() // query/fragment dropped
		}
		if s.Title != nil {
			if o.Title, err = str(*s.Title, w+".title", 300, false); err != nil {
				return out, err
			}
		}
		if s.Hash != nil {
			if !hashRE.MatchString(*s.Hash) {
				return out, invalid("%s.hash must be a sha256 hex digest", w)
			}
			o.Hash = *s.Hash
		}
		if o == (model.ContextSource{}) {
			return out, invalid("%s is empty", w)
		}
		sources = append(sources, o)
	}
	return model.Context{ID: c.ID, Version: c.Version, Text: text, Sources: sources, Digest: ContextDigest(text, sources)}, nil
}

// profileConfig holds private execution settings and an optional legacy project binding.
type profileConfig struct {
	ProjectID    json.RawMessage      `json:"projectId"`
	Executor     string               `json:"executor"`
	Provider     string               `json:"provider"`
	Model        string               `json:"model"`
	AuthEnv      string               `json:"authEnv"`
	Instructions []string             `json:"instructions"`
	Limits       *model.ProfileLimits `json:"limits"`
	PiCommand    json.RawMessage      `json:"piCommand"`
}

var authEnvRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// freezeProfile reads a private config and returns a project-owned frozen snapshot.
func freezeProfile(configFile, role, projectID string) (model.Profile, error) {
	p, err := inspectProfile(configFile, role, &projectID)
	if err == nil {
		p.ProjectID = projectID
	}
	return p, err
}

// InspectProfile validates one explicit private config without creating a Task
// or reading the credential referenced by authEnv. Prepare uses the same parser.
func InspectProfile(configFile string) (model.Profile, error) {
	return inspectProfile(configFile, "developer", nil)
}

func inspectProfile(configFile, role string, expectedProject *string) (model.Profile, error) {
	var p model.Profile
	where := "profiles." + role
	if !filepath.IsAbs(configFile) || filepath.Clean(configFile) != configFile {
		return p, invalid("%s must be a clean absolute config path", where)
	}
	real, err := filepath.EvalSymlinks(configFile)
	if err != nil {
		return p, invalid("%s config not found; select an existing execution Profile or configure a reusable one", where)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return p, invalid("%s config not found", where)
	}
	// Unix profiles may be read-only; keep the existing user-only permission
	// contract. Windows checks the current-user/SYSTEM ACL independently.
	if !fi.Mode().IsRegular() || !platform.Private(real, fi, fi.Mode().Perm()&0o700) || fi.Size() > maxConfigFile {
		return p, invalid("%s config must be a private regular file owned by the current user", where)
	}
	b, err := os.ReadFile(real)
	if err != nil || len(b) > maxConfigFile {
		return p, invalid("%s config is unreadable", where)
	}
	var c profileConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return p, invalid("%s config is not valid JSON", where)
	}
	projectID := ""
	if len(c.ProjectID) != 0 {
		if json.Unmarshal(c.ProjectID, &projectID) != nil || !plainRE.MatchString(projectID) || model.LooksLikeCredential(projectID) {
			return p, invalid("%s config projectId must be a plain name", where)
		}
		if expectedProject != nil && projectID != *expectedProject {
			return p, invalid("%s config projectId does not match project.id; select a reusable Profile without projectId for cross-project work", where)
		}
	}
	if c.Executor == "" {
		c.Executor = "pi"
	}
	for _, s := range []string{c.Executor, c.Provider, c.Model} {
		if !plainRE.MatchString(s) || model.LooksLikeCredential(s) {
			return p, invalid("%s executor, provider and model must be plain names", where)
		}
	}
	if !authEnvRE.MatchString(c.AuthEnv) || !model.ValidEnvName(c.AuthEnv) {
		return p, invalid("%s authEnv must be an environment variable name", where)
	}
	if c.Instructions == nil {
		c.Instructions = []string{}
	}
	if len(c.Instructions) > 20 {
		return p, invalid("%s has too many instructions", where)
	}
	for _, ip := range c.Instructions {
		if ip == "" || filepath.IsAbs(ip) || !filepath.IsLocal(ip) || hasCtl(ip) || len(ip) > 300 {
			return p, invalid("%s instructions must be relative paths", where)
		}
	}
	cmd := []string{"pi"}
	if len(c.PiCommand) > 0 {
		var one string
		if json.Unmarshal(c.PiCommand, &one) == nil {
			cmd = []string{one}
		} else if json.Unmarshal(c.PiCommand, &cmd) != nil {
			return p, invalid("%s piCommand must be a string array", where)
		}
	}
	if len(cmd) == 0 || len(cmd) > 20 || slices.ContainsFunc(cmd, func(a string) bool { return a == "" || strings.ContainsRune(a, 0) || model.LooksLikeCredential(a) }) {
		return p, invalid("%s piCommand must be a non-empty credential-free string array", where)
	}
	lim := model.ProfileLimits{MaxWallSeconds: 1800, MaxTokens: 2_000_000}
	if c.Limits != nil {
		if c.Limits.MaxWallSeconds != 0 {
			lim.MaxWallSeconds = c.Limits.MaxWallSeconds
		}
		if c.Limits.MaxTokens != 0 {
			lim.MaxTokens = c.Limits.MaxTokens
		}
	}
	if lim.MaxWallSeconds < 1 || lim.MaxWallSeconds > maxProfileWall || lim.MaxTokens < 1 || lim.MaxTokens > maxProfileTokens {
		return p, invalid("%s limits out of range", where)
	}
	p = model.Profile{ProjectID: projectID, Reusable: len(c.ProjectID) == 0, Role: role, Executor: c.Executor, Provider: c.Provider, Model: c.Model, AuthEnv: c.AuthEnv,
		Instructions: slices.Clone(c.Instructions), Limits: lim, PiCommand: cmd, ConfigFile: real}
	p.ConfigDigest = profileDigest(p)
	return p, nil
}

func profileDigest(p model.Profile) string {
	projectID := p.ProjectID
	if p.Reusable {
		projectID = ""
	}
	return "sha256:" + sha(canonical(toAny(map[string]any{"projectId": projectID, "executor": p.Executor, "provider": p.Provider,
		"model": p.Model, "authEnv": p.AuthEnv, "instructions": p.Instructions, "limits": p.Limits, "piCommand": p.PiCommand})))
}

type preparedFacts struct {
	repository, worktree, branch, head string
}

// checkRepositoryPair validates a linked, clean, unprotected worktree sharing the repository's common dir.
func checkRepositoryPair(repository, worktree string) (preparedFacts, error) {
	var f preparedFacts
	repo, ok1 := realDir(repository)
	wt, ok2 := realDir(worktree)
	if !ok1 || !ok2 {
		return f, invalid("repository and worktree must be existing absolute directories")
	}
	if repo == wt {
		return f, invalid("worktree must be a linked worktree distinct from repository")
	}
	if gitTop(repo) != repo {
		return f, invalid("repository must be a Git root")
	}
	if gitTop(wt) != wt {
		return f, invalid("worktree must be a Git worktree root")
	}
	rc := gitAbs(repo, "rev-parse", "--git-common-dir")
	wc := gitAbs(wt, "rev-parse", "--git-common-dir")
	if rc == "" || rc != wc {
		return f, invalid("repository and worktree must share the same Git common directory")
	}
	if gd := gitAbs(wt, "rev-parse", "--git-dir"); gd == "" || gd == wc {
		return f, invalid("refusing primary worktree; use a linked worktree")
	}
	fx := inspect(wt)
	if !fx.Exists {
		return f, invalid("worktree HEAD is not readable")
	}
	if fx.Branch == "" {
		return f, invalid("worktree HEAD is detached")
	}
	if slices.Contains(protectedBranches, fx.Branch) {
		return f, invalid("refusing protected branch")
	}
	if !fx.Clean {
		return f, invalid("worktree is not clean")
	}
	return preparedFacts{repository: repo, worktree: wt, branch: fx.Branch, head: fx.Head}, nil
}

func checkInstructions(wt string, p model.Profile) error {
	root, err := os.OpenRoot(wt)
	if err != nil {
		return invalid("worktree not readable")
	}
	defer root.Close()
	for _, ip := range p.Instructions {
		fi, err := root.Stat(ip)
		if err != nil || !fi.Mode().IsRegular() {
			return invalid("profiles.%s instruction file missing", p.Role)
		}
	}
	return nil
}

// validated is a fully checked prepare input; producing it never writes state.
type validated struct {
	projectID, projectName, title, goal string
	scope, acceptance, deps             []string
	changeID                            *string
	issue                               *model.IssueRef
	budget                              *model.Budget
	ctx                                 model.Context
	facts                               preparedFacts
	frozen                              map[string]model.Profile
}

// Prepare validates a strict input and records a new ready task.
func (c *Core) Prepare(raw []byte) (model.Task, error) { return c.prepare(raw, model.OriginNative) }

// validateInput performs every pure prepare check (files, Git, executors); no keys, processes or DB writes.
func (c *Core) validateInput(raw []byte) (validated, error) {
	return c.validateInputFor(raw, false)
}

func (c *Core) validateInputFor(raw []byte, delegate bool) (validated, error) {
	var in Input
	var task validated
	if err := decodeStrict(raw, &in, MaxInput); err != nil {
		return task, err
	}
	projectID, err := str(in.Project.ID, "project.id", 63, false)
	if err != nil || !slugRE.MatchString(projectID) {
		return task, invalid("project.id must be a lowercase slug")
	}
	projectName, err := str(in.Project.Name, "project.name", 120, false)
	if err != nil {
		return task, err
	}
	title, err := str(in.Title, "title", 200, false)
	if err != nil {
		return task, err
	}
	goal, err := str(in.Goal, "goal", 4000, true)
	if err != nil {
		return task, err
	}
	scope, err := NormalizeScope(in.Scope)
	if err != nil {
		return task, err
	}
	if len(in.Acceptance) < 1 || len(in.Acceptance) > 50 {
		return task, invalid("acceptance must list 1..50 items")
	}
	acceptance := make([]string, len(in.Acceptance))
	for i, a := range in.Acceptance {
		if acceptance[i], err = str(a, fmt.Sprintf("acceptance[%d]", i), 500, false); err != nil {
			return task, err
		}
	}
	if len(in.Dependencies) > 50 {
		return task, invalid("dependencies must list at most 50 task UUIDs")
	}
	deps := []string{}
	for _, d := range in.Dependencies {
		if !uuidRE.MatchString(d) {
			return task, invalid("dependencies must be task UUIDs")
		}
		if !slices.Contains(deps, d) {
			deps = append(deps, d)
		}
	}
	var changeID *string
	if in.ChangeID != nil {
		if !changeIDRE.MatchString(*in.ChangeID) || model.LooksLikeCredential(*in.ChangeID) {
			return task, invalid("changeId must be a short safe identifier")
		}
		v := *in.ChangeID
		changeID = &v
	}
	issue, err := normIssue(in.IssueRef)
	if err != nil {
		return task, err
	}
	budget, err := normBudget(in.Budget)
	if err != nil {
		return task, err
	}
	ctx, err := normContext(in.Context)
	if err != nil {
		return task, err
	}
	for k := range in.Profiles {
		if !model.IsRole(k) {
			return task, invalid("profiles has an unknown role")
		}
	}
	facts, err := checkRepositoryPair(in.Repository, in.Worktree)
	if err != nil {
		return task, err
	}
	frozen := map[string]model.Profile{}
	for _, role := range model.Roles {
		cf, ok := in.Profiles[role]
		if !ok {
			if delegate && role != "developer" {
				continue
			}
			return task, invalid("profiles.%s is required", role)
		}
		cf = ResolveProfileReference(c.st.DataDir(), cf)
		p, err := freezeProfile(cf, role, projectID)
		if err != nil {
			return task, err
		}
		ex, ok := c.reg[p.Executor]
		if !ok {
			return task, invalid("profiles.%s executor is not registered", role)
		}
		if err := ex.Validate(p); err != nil {
			return task, invalid("profiles.%s is not valid for its executor", role)
		}
		if err := checkInstructions(facts.worktree, p); err != nil {
			return task, err
		}
		frozen[role] = p
	}
	if again := inspect(facts.worktree); again.Head != facts.head || again.Branch != facts.branch || !again.Clean {
		return task, invalid("worktree changed during preparation")
	}

	return validated{projectID: projectID, projectName: projectName, title: title, goal: goal, scope: scope, acceptance: acceptance,
		deps: deps, changeID: changeID, issue: issue, budget: budget, ctx: ctx, facts: facts, frozen: frozen}, nil
}

// checkRefs validates dependencies and the context version against st and resolves the context reference.
func checkRefs(st *model.State, v validated) (model.ContextRef, error) {
	for _, d := range v.deps {
		i := slices.IndexFunc(st.Tasks, func(t model.Task) bool { return t.ID == d })
		if i < 0 {
			return model.ContextRef{}, invalid("dependency does not exist")
		}
		if st.Tasks[i].ProjectID != v.projectID {
			return model.ContextRef{}, invalid("dependency belongs to a different project")
		}
	}
	return resolveContext(st, v.projectID, v.ctx)
}

func (c *Core) prepare(raw []byte, origin string) (model.Task, error) {
	var task model.Task
	v, err := c.validateInputFor(raw, origin == OriginDelegate)
	if err != nil {
		return task, err
	}
	projectID, projectName, facts, frozen := v.projectID, v.projectName, v.facts, v.frozen
	err = c.update(func(st *model.State) error {
		ref, err := checkRefs(st, v)
		if err != nil {
			return err
		}
		ts := now()
		pi := slices.IndexFunc(st.Projects, func(p model.Project) bool { return p.ID == projectID })
		if pi < 0 {
			st.Projects = append(st.Projects, model.Project{ID: projectID, Name: projectName, Repositories: []string{}, CreatedAt: ts, UpdatedAt: ts})
			pi = len(st.Projects) - 1
		}
		if !slices.Contains(st.Projects[pi].Repositories, facts.repository) {
			st.Projects[pi].Repositories = append(st.Projects[pi].Repositories, facts.repository)
			st.Projects[pi].UpdatedAt = ts
		}
		ids := map[string]string{}
		for _, role := range model.Roles {
			f, selected := frozen[role]
			if !selected {
				continue
			}
			i := slices.IndexFunc(st.Profiles, func(p model.Profile) bool {
				return p.ProjectID == projectID && p.Role == role && p.ConfigDigest == f.ConfigDigest && p.ConfigFile == f.ConfigFile
			})
			if i < 0 {
				f.ID = projectID + "." + role + "." + f.ConfigDigest[7:19]
				if slices.ContainsFunc(st.Profiles, func(p model.Profile) bool { return p.ID == f.ID }) {
					f.ID = projectID + "." + role + "." + newUUID()[:8]
				}
				f.CreatedAt = ts
				st.Profiles = append(st.Profiles, f)
				i = len(st.Profiles) - 1
			}
			ids[role] = st.Profiles[i].ID
		}
		branch, base := facts.branch, facts.head
		task = model.Task{ID: newUUID(), ProjectID: projectID, ChangeID: v.changeID, Repository: facts.repository, Worktree: facts.worktree,
			Branch: &branch, Title: v.title, Goal: v.goal, Scope: v.scope, Acceptance: v.acceptance, Dependencies: v.deps, ContextRef: ref,
			ProfileIDs: ids, State: model.TaskReady, BaselineSha: &base, CreatedAt: ts, UpdatedAt: ts, IssueRef: v.issue, Budget: v.budget,
			Origin: origin}
		st.Tasks = append(st.Tasks, task)
		return nil
	})
	return task, err
}

// resolveContext reuses or records an immutable context version.
func resolveContext(st *model.State, projectID string, ctx model.Context) (model.ContextRef, error) {
	if ctx.ID != "" {
		for _, c := range st.Contexts {
			if c.ID != ctx.ID {
				continue
			}
			if c.ProjectID != projectID {
				return model.ContextRef{}, invalid("context.id belongs to a different project")
			}
			if c.Version == ctx.Version {
				if c.Digest != ctx.Digest {
					return model.ContextRef{}, invalid("context version already exists with different content; use a new version")
				}
				return c.Ref(), nil
			}
		}
	} else {
		for _, c := range st.Contexts {
			if c.ProjectID == projectID && c.Version == ctx.Version && c.Digest == ctx.Digest {
				return c.Ref(), nil
			}
		}
		ctx.ID = newUUID()
	}
	ctx.ProjectID, ctx.CreatedAt = projectID, now()
	st.Contexts = append(st.Contexts, ctx)
	return ctx.Ref(), nil
}
