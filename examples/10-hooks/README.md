# Hooks Example

Demonstrates the shell-based hooks feature of saker. Hooks **fire automatically** when the agent executes a tool — no manual invocation needed.

## Run

```bash
export ANTHROPIC_API_KEY=sk-ant-...
chmod +x examples/10-hooks/scripts/*.sh
go run ./examples/10-hooks
```

## Configuration Methods

### Option 1: Code configuration (TypedHooks)

```go
typedHooks := []hooks.ShellHook{
    {
        Event:   events.PreToolUse,
        Command: "/path/to/pre_tool.sh",
    },
    {
        Event:   events.PostToolUse,
        Command: "/path/to/post_tool.sh",
        Async:   true,  // run asynchronously, does not block the main flow
    },
}

rt, _ := api.New(ctx, api.Options{
    TypedHooks: typedHooks,
})
```

### Option 2: Configuration file (.saker/settings.json)

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "scripts/pre_tool.sh",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

## Exit Code Semantics (Claude Code Spec)

| Exit code | Meaning | Behaviour |
|-----------|---------|-----------|
| 0 | Success | Parse stdout JSON output |
| 2 | Blocking error | stderr is the error message, execution is stopped |
| Other | Non-blocking | Log stderr and continue execution |

## Hook Event Types

| Hook | Trigger | Matcher target |
|------|---------|----------------|
| PreToolUse | Before tool execution | Tool name |
| PostToolUse | After tool execution | Tool name |
| SessionStart | Session start | source |
| SessionEnd | Session end | reason |
| Notification | Notification | notification_type |
| PreCompact | Before context compaction | trigger |
| SubagentStart | Subagent start | agent_type |
| SubagentStop | Subagent stop | agent_type |
| Stop | Agent stop | (no matcher) |
| UserPromptSubmit | User prompt submitted | (no matcher) |

## Payload Format (flat)

Hook scripts receive a JSON payload on stdin with all fields at the top level:

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "hooks-demo",
  "cwd": "/path/to/project",
  "tool_name": "Bash",
  "tool_input": {"command": "pwd"}
}
```

## JSON Output Format (stdout, exit 0)

Hooks can output JSON on stdout to control behaviour:

```json
{"decision": "deny", "reason": "dangerous command rejected"}
```

```json
{"hookSpecificOutput": {"permissionDecision": "ask"}}
```

```json
{"hookSpecificOutput": {"updatedInput": {"command": "ls -la"}}}
```

```json
{"continue": false, "stopReason": "cancelled by user"}
```

## ShellHook Options

| Field | Type | Description |
|-------|------|-------------|
| Async | bool | Run asynchronously, does not block the main flow |
| Once | bool | Execute only once per session |
| Timeout | Duration | Custom timeout (default 600s) |
| StatusMessage | string | Status message displayed during execution |
