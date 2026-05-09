# Configuration

Saker reads configuration from environment variables, command-line flags, and
project-local `.saker/` files.

## Environment

Common variables:

```bash
ANTHROPIC_API_KEY=
OPENAI_API_KEY=
DASHSCOPE_API_KEY=
SAKER_MODEL=claude-sonnet-4-5-20250929
```

Use `.env.example` as a local template.

## Project State

`.saker/` is ignored by git and may contain:

- `settings.json`: shared project settings.
- `settings.local.json`: local credentials and machine-specific overrides.
- `skills/`: project skills.
- `memory/`: persistent memory.
- `profiles/<name>/`: isolated profile state.

## Server

Start the server:

```bash
./bin/saker --server --server-addr :10112
```

Configure web auth:

```bash
./bin/saker --auth-user admin --auth-pass '<password>'
```

Serve a frontend build from disk instead of embedded files:

```bash
./bin/saker --server --server-static ./web/out
```

## CLI

Useful flags:

```bash
./bin/saker --print "prompt"
./bin/saker --model claude-sonnet-4-5-20250929 --print "prompt"
./bin/saker --provider openai --print "prompt"
./bin/saker --profile demo --print "prompt"
./bin/saker --sandbox-backend landlock --print "prompt"
```
