package client

import (
	"context"
	"fmt"
	"time"

	"github.com/cinience/saker/pkg/runtime/subagents"
)

// ACPAgentConfig describes how to launch an external ACP agent.
type ACPAgentConfig struct {
	Command string
	Args    []string
	Env     []string
	Timeout time.Duration
}

// ACPRunnerConfig maps target names to their ACP agent configurations.
type ACPRunnerConfig struct {
	Agents map[string]ACPAgentConfig
}

// acpRunner implements subagents.Runner, routing ACP targets to external
// agent processes and falling back to an inner runner for everything else.
type acpRunner struct {
	config   ACPRunnerConfig
	fallback subagents.Runner
}

// NewACPRunner wraps a fallback Runner with ACP target routing.
// Targets matching config.Agents keys are executed via ACP; all others
// are delegated to the fallback runner.
func NewACPRunner(config ACPRunnerConfig, fallback subagents.Runner) subagents.Runner {
	if len(config.Agents) == 0 {
		return fallback
	}
	return &acpRunner{config: config, fallback: fallback}
}

func (r *acpRunner) RunSubagent(ctx context.Context, req subagents.RunRequest) (subagents.Result, error) {
	agentCfg, ok := r.config.Agents[req.Target]
	if !ok {
		if r.fallback != nil {
			return r.fallback.RunSubagent(ctx, req)
		}
		return subagents.Result{
			Subagent: req.Target,
			Error:    fmt.Sprintf("unknown ACP target %q and no fallback runner", req.Target),
		}, fmt.Errorf("acp: unknown target %q", req.Target)
	}

	timeout := agentCfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cwd := ""
	if v, ok := req.Metadata["cwd"]; ok {
		if s, ok := v.(string); ok {
			cwd = s
		}
	}

	acpClient, err := Dial(runCtx, DialOptions{
		Command: agentCfg.Command,
		Args:    agentCfg.Args,
		Cwd:     cwd,
		Env:     agentCfg.Env,
		Timeout: 30 * time.Second, // handshake timeout
	})
	if err != nil {
		return subagents.Result{
			Subagent: req.Target,
			Error:    err.Error(),
			Metadata: map[string]any{"runtime": "acp"},
		}, fmt.Errorf("acp dial %q: %w", req.Target, err)
	}
	defer acpClient.Close()

	result, err := acpClient.Run(runCtx, RunOptions{
		Cwd:  cwd,
		Task: req.Instruction,
	})
	if err != nil {
		return subagents.Result{
			Subagent: req.Target,
			Error:    err.Error(),
			Metadata: map[string]any{"runtime": "acp"},
		}, fmt.Errorf("acp run %q: %w", req.Target, err)
	}

	meta := map[string]any{"runtime": "acp"}
	if result.Usage != nil {
		meta["usage"] = result.Usage
	}

	return subagents.Result{
		Subagent: req.Target,
		Output:   result.Output,
		Metadata: meta,
	}, nil
}
