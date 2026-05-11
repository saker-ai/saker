// bash.go: BashTool struct, constructors, setters, and Tool interface entry points.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

const (
	defaultBashTimeout = 10 * time.Minute
	maxBashTimeout     = 60 * time.Minute
	maxBashOutputLen   = 30000
	bashDescript       = `
	# Bash Tool Documentation

	Executes bash commands in a persistent shell session with optional timeout, ensuring proper handling and security measures.

	**IMPORTANT**: This tool is for terminal operations like git, npm, docker, etc. DO NOT use it for file operations (reading, writing, editing, searching, finding files) - use specialized tools instead.

	## Pre-Execution Steps

	### 1. Directory Verification
	- If creating new directories/files, first use 'ls' to verify the parent directory exists
	- Example: Before 'mkdir foo/bar', run 'ls foo' to check "foo" exists

	### 2. Command Execution
	- Always quote file paths with spaces using double quotes
	- Examples:
	- ✅ 'cd "/Users/name/My Documents"'
	- ❌ 'cd /Users/name/My Documents'
	- ✅ 'python "/path/with spaces/script.py"'
	- ❌ 'python /path/with spaces/script.py'

	## Usage Notes

	- **Required**: command argument
	- **Optional**: timeout in milliseconds (max 600000ms/10 min, default 120000ms/2 min)
	- **Description**: Write clear 5-10 word description of command purpose
	- **Output limit**: Saved to disk if exceeds 30000 characters
	- **Async execution**: Set 'async=true' for long-running tasks (dev servers, log tailing). Use BashStatus with task_id to poll status (no output consumption), BashOutput with task_id to poll output, and KillTask to stop.

	## Command Preferences

	Avoid using Bash for these operations - use dedicated tools instead:
	- File search → Use **Glob** (NOT find/ls)
	- Content search → Use **Grep** (NOT grep/rg)
	- Read files → Use **Read** (NOT cat/head/tail)
	- Edit files → Use **Edit** (NOT sed/awk)
	- Write files → Use **Write** (NOT echo >/cat <<EOF)
	- Communication → Output text directly (NOT echo/printf)

	## Multiple Commands

	- **Parallel (independent)**: Make multiple Bash tool calls in single message
	- **Sequential (dependent)**: Chain with '&&' (e.g., 'git add . && git commit -m "message" && git push')
	- **Sequential (ignore failures)**: Use ';'
	- **DO NOT**: Use newlines to separate commands (except in quoted strings)

	## Working Directory

	Maintain current directory by using absolute paths and avoiding 'cd':
	- ✅ 'pytest /foo/bar/tests'
	- ❌ 'cd /foo/bar && pytest tests'

	---

	## Git Commit Protocol

	**Only create commits when explicitly requested by user.**

	### Git Safety Rules
	- ❌ NEVER update git config
	- ❌ NEVER run destructive commands (push --force, hard reset) unless explicitly requested
	- ❌ NEVER skip hooks (--no-verify, --no-gpg-sign) unless explicitly requested
	- ❌ NEVER force push to main/master (warn user if requested)
	- ⚠️ Avoid 'git commit --amend' (only use when: user explicitly requests OR adding pre-commit hook edits)
	- ✅ Before amending: ALWAYS check authorship ('git log -1 --format='%an %ae'')
	- ⚠️ NEVER commit unless explicitly asked

	### Commit Steps

	**1. Gather information (parallel)**
	'''bash
	git status
	git diff
	git log
	'''

	**2. Analyze and draft**
	- Summarize change nature (feature/enhancement/fix/refactor/test/docs)
	- Don't commit secret files (.env, credentials.json) - warn user
	- Draft concise 1-2 sentence message focusing on "why" not "what"

	**3. Execute commit (sequential where needed)**
	'''bash
	git add [files]
	git commit -m "$(cat <<'EOF'
	Commit message here.
	EOF
	)"
	git status  # Verify success
	'''

	**4. Handle pre-commit hook failures**
	- Retry ONCE if commit fails
	- If files modified by hook, verify safe to amend:
	- Check authorship: 'git log -1 --format='%an %ae''
	- Check not pushed: 'git status' shows "Your branch is ahead"
	- If both true → amend; otherwise → create NEW commit

	### Important Notes
	- ❌ NEVER run additional code exploration commands
	- ❌ NEVER use TaskCreate or Task tools
	- ❌ DO NOT push unless explicitly asked
	- ❌ NEVER use '-i' flag (interactive not supported)
	- ⚠️ Don't create empty commits if no changes
	- ✅ ALWAYS use HEREDOC for commit messages

	---

	## Pull Request Protocol

	Use 'gh' command via Bash tool for ALL GitHub tasks (issues, PRs, checks, releases).

	### PR Creation Steps

	**1. Understand branch state (parallel)**
	'''bash
	git status
	git diff
	git log
	git diff [base-branch]...HEAD
	'''
	Check if branch tracks remote and is up to date.

	**2. Analyze and draft**
	Review ALL commits (not just latest) that will be included in PR.

	**3. Create PR (parallel where possible)**
	'''bash
	# Create branch if needed
	# Push with -u flag if needed
	gh pr create --title "the pr title" --body "$(cat <<'EOF'
	## Summary
	<1-3 bullet points>

	## Test plan
	[Bulleted markdown checklist of TODOs for testing the pull request...]
	EOF
	)"
	'''

	### Important Notes
	- ❌ DO NOT use TaskCreate or Task tools
	- ✅ Return PR URL when done

	---

	## Other Common Operations

	**View PR comments:**
	'''bash
	gh api repos/foo/bar/pulls/123/comments
	'''
	`
)

var bashSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"command": map[string]interface{}{
			"type":        "string",
			"description": "Command string executed via bash without shell metacharacters.",
		},
		"timeout": map[string]interface{}{
			"type":        "number",
			"description": "Optional timeout in seconds (defaults to 30, caps at 120).",
		},
		"workdir": map[string]interface{}{
			"type":        "string",
			"description": "Optional working directory relative to the sandbox root.",
		},
		"async": map[string]interface{}{
			"type":        "boolean",
			"description": "Run command asynchronously and return a task_id immediately.",
		},
		"task_id": map[string]interface{}{
			"type":        "string",
			"description": "Optional async task id to use when async=true.",
		},
	},
	Required: []string{"command"},
}

// BashTool executes validated commands using bash within a sandbox.
type BashTool struct {
	sandbox *security.Sandbox
	root    string
	timeout time.Duration
	env     sandboxenv.ExecutionEnvironment

	outputThresholdBytes int
}

// NewBashTool builds a BashTool rooted at the current directory.
func NewBashTool() *BashTool {
	return NewBashToolWithRoot("")
}

// NewBashToolWithRoot builds a BashTool rooted at the provided directory.
func NewBashToolWithRoot(root string) *BashTool {
	resolved := resolveRoot(root)
	return &BashTool{
		sandbox: security.NewSandbox(resolved),
		root:    resolved,
		timeout: defaultBashTimeout,

		outputThresholdBytes: maxBashOutputLen,
	}
}

// NewBashToolWithSandbox builds a BashTool with a custom sandbox.
// Used when sandbox needs to be pre-configured (e.g., disabled mode).
func NewBashToolWithSandbox(root string, sandbox *security.Sandbox) *BashTool {
	resolved := resolveRoot(root)
	return &BashTool{
		sandbox: sandbox,
		root:    resolved,
		timeout: defaultBashTimeout,

		outputThresholdBytes: maxBashOutputLen,
	}
}

// SetOutputThresholdBytes controls when output is spooled to disk.
func (b *BashTool) SetOutputThresholdBytes(threshold int) {
	if b == nil {
		return
	}
	b.outputThresholdBytes = threshold
}

func (b *BashTool) effectiveOutputThresholdBytes() int {
	if b == nil || b.outputThresholdBytes <= 0 {
		return maxBashOutputLen
	}
	return b.outputThresholdBytes
}

// SetCommandLimits overrides the maximum command length (bytes) and argument count
// enforced by the security validator. Use this for code-generation scenarios where
// agents write files via bash heredocs or long cat commands.
func (b *BashTool) SetCommandLimits(maxBytes, maxArgs int) {
	if b != nil && b.sandbox != nil {
		b.sandbox.SetCommandLimits(maxBytes, maxArgs)
	}
}

// AllowShellMetachars enables shell pipes and metacharacters (CLI mode).
func (b *BashTool) AllowShellMetachars(allow bool) {
	if b != nil && b.sandbox != nil {
		b.sandbox.AllowShellMetachars(allow)
	}
}

func (b *BashTool) SetEnvironment(env sandboxenv.ExecutionEnvironment) {
	if b != nil {
		b.env = env
	}
}

func (b *BashTool) Name() string { return "Bash" }

func (b *BashTool) Description() string {
	return bashDescript
}

func (b *BashTool) Schema() *tool.JSONSchema { return bashSchema }

func (b *BashTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if b == nil || b.sandbox == nil {
		return nil, errors.New("bash tool is not initialised")
	}
	async, err := parseAsyncFlag(params)
	if err != nil {
		return nil, err
	}
	command, err := extractCommand(params)
	if err != nil {
		return nil, err
	}
	if err := b.sandbox.ValidateCommand(command); err != nil {
		return nil, err
	}
	ps, err := b.prepareSession(ctx)
	if err != nil {
		return nil, err
	}
	workdir, err := b.resolveWorkdir(params, ps)
	if err != nil {
		return nil, err
	}
	timeout, err := b.resolveTimeout(params)
	if err != nil {
		return nil, err
	}

	if async {
		id, err := optionalAsyncTaskID(params)
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = generateAsyncTaskID()
		}
		if err := DefaultAsyncTaskManager().startWithContext(ctx, id, command, workdir, timeout); err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"task_id": id,
			"status":  "running",
		}
		out, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal async result: %w", err)
		}
		return &tool.ToolResult{Success: true, Output: string(out), Data: payload}, nil
	}

	if isVirtualizedSandboxSession(ps) && b.env != nil {
		res, err := b.env.RunCommand(ctx, ps, sandboxenv.CommandRequest{
			Command: command,
			Workdir: workdir,
			Timeout: timeout,
		})
		if res == nil {
			res = &sandboxenv.CommandResult{}
		}
		data := map[string]interface{}{
			"workdir":     workdir,
			"duration_ms": res.Duration.Milliseconds(),
			"timeout_ms":  timeout.Milliseconds(),
		}
		output := redactSecrets(combineOutput(res.Stdout, res.Stderr))
		if err != nil {
			return &tool.ToolResult{Success: false, Output: output, Data: data}, err
		}
		return &tool.ToolResult{Success: res.ExitCode == 0, Output: output, Data: data}, nil
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Env = os.Environ()
	cmd.Dir = workdir

	spool := newBashOutputSpool(ctx, b.effectiveOutputThresholdBytes())
	cmd.Stdout = spool.StdoutWriter()
	cmd.Stderr = spool.StderrWriter()

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	rawOutput, outputFile, spoolErr := spool.Finalize()
	output := redactSecrets(rawOutput)

	data := map[string]interface{}{
		"workdir":     workdir,
		"duration_ms": duration.Milliseconds(),
		"timeout_ms":  timeout.Milliseconds(),
	}
	if outputFile != "" {
		data["output_file"] = outputFile
	}
	if spoolErr != nil {
		data["spool_error"] = spoolErr.Error()
	}

	result := &tool.ToolResult{
		Success: runErr == nil,
		Output:  output,
		Data:    data,
	}

	if runErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("command timeout after %s", timeout)
		}
		return result, fmt.Errorf("command failed: %w", runErr)
	}
	return result, nil
}
