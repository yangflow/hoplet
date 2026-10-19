// Package codex is the runtime driver for Codex (ADR 0027): one `codex app-server` process per controlled
// session, spoken to in JSON-RPC over its standard streams with the experimental API. The thread ID is the
// session ID, and the history is the rollout file the provider reads, so the driver streams no conversation.
package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/yangflow/hoplet/agent/internal/runtime"
)

// Options configures the driver.
type Options struct {
	// Bin is the Codex executable; empty means `codex` on PATH.
	Bin string
	// ImageDir is where the images of a prompt are written for Codex to read as local files; empty means a
	// directory under the system's temporary directory. The files of a process are removed when it exits.
	ImageDir string
}

// Driver starts Codex app-server processes.
type Driver struct {
	bin      string
	imageDir string

	mu          sync.Mutex
	versionSeen bool
	version     string
	models      []modelInfo
	modelsSeen  bool
}

func New(opts Options) *Driver {
	bin := opts.Bin
	if bin == "" {
		bin = "codex"
	}
	dir := opts.ImageDir
	if dir == "" {
		dir = os.TempDir() + string(os.PathSeparator) + "hoplet-codex-images"
	}
	return &Driver{bin: bin, imageDir: dir}
}

func (d *Driver) Name() string { return "codex" }

func (d *Driver) Capabilities() []runtime.Capability {
	return []runtime.Capability{runtime.CapPermissions, runtime.CapInterrupt, runtime.CapImages, runtime.CapSteer}
}

// Option IDs and the value that leaves a setting to Codex (ADR 0018).
const (
	optMode          = "mode"
	optModel         = "model"
	optEffort        = "effort"
	optCollaboration = "collaboration"

	valueDefault = "default"
	collabPlan   = "plan"

	modeReadOnly       = "read-only"
	modeWorkspaceWrite = "workspace-write"
	modeAutoReview     = "auto-review"
	modeFullAccess     = "full-access"
)

// modeDef is one value of the `mode` option as the pair Codex takes: an approval policy and a sandbox, plus the
// reviewer of approvals. The thread-level sandbox is a string, the per-turn one an object with the same kind.
type modeDef struct {
	value, name, description, icon, risk string
	approval                             string // approvalPolicy
	sandbox                              string // thread/start sandbox
	policy                               string // turn/start sandboxPolicy type
	reviewer                             string // approvalsReviewer
}

// modes are the values of `mode`, in the order they are offered.
var modes = []modeDef{
	{modeReadOnly, "Read only", "Read files; ask before edits and commands.", "shield", "", "on-request", "read-only", "readOnly", "user"},
	{modeWorkspaceWrite, "Workspace write", "Edit the workspace without asking; ask before leaving it.", "file-pen", "", "on-request", "workspace-write", "workspaceWrite", "user"},
	{modeAutoReview, "Auto review", "Edit the workspace; Codex's reviewer decides which requests to ask about.", "sparkles", "", "on-request", "workspace-write", "workspaceWrite", "auto_review"},
	{modeFullAccess, "Full access", "No sandbox and no approvals: Codex can change anything the user can.", "shield-alert", "high", "never", "danger-full-access", "dangerFullAccess", "user"},
}

func modeByValue(v string) (modeDef, bool) {
	for _, m := range modes {
		if m.value == v {
			return m, true
		}
	}
	return modeDef{}, false
}

// modeOf is the mode whose pair Codex reports, if it is one the driver declares.
func modeOf(approval, policy, reviewer string) (string, bool) {
	if reviewer == "" {
		reviewer = "user"
	}
	for _, m := range modes {
		if m.approval == approval && m.policy == policy && m.reviewer == reviewer {
			return m.value, true
		}
	}
	return "", false
}

// planExitModes are the modes an approved plan may switch to: every mode but the one without a sandbox.
func planExitModes() []string {
	var out []string
	for _, m := range modes {
		if m.value != modeFullAccess {
			out = append(out, m.value)
		}
	}
	return out
}

// modelInfo is one entry of Codex's model list.
type modelInfo struct {
	ID            string
	Name          string
	Description   string
	Efforts       []string
	DefaultEffort string
}

// Config declares the mode, the model, the reasoning effort and the collaboration mode, in that order. The
// models and efforts come from the app server's `model/list` once the driver has read it (LoadModels, or the
// first process); until then only the default is offered. A setting applies at the next turn.
func (d *Driver) Config() []runtime.ConfigOption {
	d.mu.Lock()
	models := append([]modelInfo(nil), d.models...)
	d.mu.Unlock()

	modeValues := make([]runtime.ConfigValue, 0, len(modes))
	for _, m := range modes {
		modeValues = append(modeValues, runtime.ConfigValue{Value: m.value, Name: m.name, Description: m.description, Risk: m.risk, Icon: m.icon})
	}
	modelValues := []runtime.ConfigValue{{Value: valueDefault, Name: "Default model"}}
	// The efforts of every model, in the order the models list them; the default leaves it to the model. The
	// generic option has no values per model, so the driver leaves out an effort the chosen model lacks.
	effortValues := []runtime.ConfigValue{{Value: valueDefault, Name: "Default effort"}}
	seen := map[string]bool{}
	for _, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		modelValues = append(modelValues, runtime.ConfigValue{Value: m.ID, Name: name, Description: m.Description})
		for _, e := range m.Efforts {
			if !seen[e] {
				seen[e] = true
				effortValues = append(effortValues, runtime.ConfigValue{Value: e, Name: e})
			}
		}
	}
	return []runtime.ConfigOption{
		{
			ID: optMode, Name: "Mode", Category: "mode", Type: runtime.ConfigSelect, Current: modeWorkspaceWrite,
			Apply: runtime.ApplyNextTurn, Options: modeValues,
		},
		{
			ID: optModel, Name: "Model", Category: "model", Type: runtime.ConfigSelect, Current: valueDefault,
			Apply: runtime.ApplyNextTurn, Options: modelValues,
		},
		{
			ID: optEffort, Name: "Effort", Category: "thought_level", Type: runtime.ConfigSelect, Current: valueDefault,
			Apply: runtime.ApplyNextTurn, Options: effortValues,
		},
		{
			ID: optCollaboration, Name: "Collaboration", Category: "model_config", Type: runtime.ConfigSelect, Current: valueDefault,
			Apply: runtime.ApplyNextTurn,
			Options: []runtime.ConfigValue{
				{Value: valueDefault, Name: "Default", Description: "Carry out the request.", Icon: "sparkles"},
				{Value: collabPlan, Name: "Plan", Description: "Explore and plan; no edits or commands.", Icon: "list-checks"},
			},
		},
	}
}

// supportsEffort says whether the model offers the effort; a model the driver has not read counts as offering it.
func (d *Driver) supportsEffort(model, effort string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.models {
		if m.ID == model {
			for _, e := range m.Efforts {
				if e == effort {
					return true
				}
			}
			return false
		}
	}
	return true
}

func (d *Driver) setModels(models []modelInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.models, d.modelsSeen = models, true
}

func (d *Driver) haveModels() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.modelsSeen
}

// checkVersion runs `codex --version` until a supported version was seen once.
func (d *Driver) checkVersion(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.versionSeen {
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("run %s --version: %w", d.bin, err)
	}
	v := parseVersionOutput(string(out))
	if v == "" {
		return errors.New("codex --version printed nothing")
	}
	if err := CheckVersion(v); err != nil {
		return err
	}
	d.versionSeen, d.version = true, v
	return nil
}

// Version is the installed codex-cli version; it fails when the version cannot be read or is not one the
// driver was tested against, so a supported version is always tested.
func (d *Driver) Version(ctx context.Context) (string, bool, error) {
	if err := d.checkVersion(ctx); err != nil {
		return "", false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version, true, nil
}

// LoadModels reads the model list from a short-lived app server so that Config offers the models; it does
// nothing once the list was read.
func (d *Driver) LoadModels(ctx context.Context) error {
	if d.haveModels() {
		return nil
	}
	if err := d.checkVersion(ctx); err != nil {
		return err
	}
	p, err := d.launch("")
	if err != nil {
		return err
	}
	defer func() { _ = p.Kill() }()
	if err := p.initialize(ctx); err != nil {
		return p.wrapErr(err)
	}
	return p.loadModels(ctx)
}

// Start resumes the session's thread, or creates a thread under no ID the caller chose: a New spec is
// refused, because Codex assigns the ID (the manager uses StartNew).
func (d *Driver) Start(ctx context.Context, spec runtime.StartSpec) (runtime.Process, error) {
	if spec.New || spec.SessionID == "" {
		return nil, errors.New("codex assigns a new session's ID: start it with StartNew")
	}
	p, err := d.open(ctx, spec)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// StartNew creates a thread in spec.Dir and returns its ID with the process that holds it.
func (d *Driver) StartNew(ctx context.Context, spec runtime.StartSpec) (string, runtime.Process, error) {
	spec.New, spec.SessionID = true, ""
	p, err := d.open(ctx, spec)
	if err != nil {
		return "", nil, err
	}
	return p.threadID, p, nil
}

// open launches the app server, initializes it and starts or resumes the thread; the process reports Ready when
// the thread is open.
func (d *Driver) open(ctx context.Context, spec runtime.StartSpec) (*process, error) {
	if err := d.checkVersion(ctx); err != nil {
		return nil, err
	}
	p, err := d.launch(spec.Dir)
	if err != nil {
		return nil, err
	}
	p.configure(spec.Config)
	fail := func(err error) (*process, error) {
		_ = p.Kill()
		return nil, p.wrapErr(err)
	}
	if err := p.initialize(ctx); err != nil {
		return fail(err)
	}
	if !d.haveModels() {
		_ = p.loadModels(ctx) // the model list is for the settings; a failure leaves the defaults
	}
	if err := p.openThread(ctx, spec); err != nil {
		return fail(err)
	}
	p.emit(runtime.Ready{})
	return p, nil
}

// launch starts `codex app-server` in dir, in its own process group, and begins reading its output.
func (d *Driver) launch(dir string) (*process, error) {
	cmd := exec.Command(d.bin, "app-server")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", d.bin, err)
	}
	p := newProcess(d, cmd, stdin, &stderr)
	go p.read(stdout)
	return p, nil
}

// tailBuffer keeps the last bytes written to it, for error messages.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailLimit = 4 << 10

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailLimit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailLimit:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
