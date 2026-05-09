# Embedded Filesystem Example

This example demonstrates how to use `embed.FS` to embed the `.saker` directory into a binary.

## Features

- ✅ Embed configuration files into the binary
- ✅ Embed skills into the binary
- ✅ Filesystem priority strategy (allows runtime overrides)
- ✅ Self-contained executable with zero external dependencies

## Usage

### 1. Set API Key

```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

### 2. Run the example

```bash
go run main.go
```

### 3. Build a standalone binary

```bash
go build -o embed-demo main.go
./embed-demo
```

## How It Works

### Embedding the filesystem

```go
//go:embed .saker
var claudeFS embed.FS
```

This line embeds the entire `.saker` directory into the compiled binary.

### Passing it to the Runtime

```go
runtime, err := api.New(context.Background(), api.Options{
    ProjectRoot:  ".",
    ModelFactory: provider,
    EmbedFS:      claudeFS,  // pass the embedded filesystem
})
```

### Load priority

1. **OS filesystem first**: if `.saker/settings.json` exists locally, it takes priority
2. **Embedded FS fallback**: if no local file exists, the embedded version is used

This means you can:
- Ship default configuration at build time (embedded)
- Override at runtime by creating local files (OS filesystem)

## Runtime Override Example

Even with config embedded in the binary, you can still override it at runtime:

```bash
# Create a local override config
mkdir -p .saker
cat > .saker/settings.local.json <<EOF
{
  "permissions": {
    "allow": ["Bash(*:*)"]
  }
}
EOF

# The runtime will use the local config to override the embedded config
./embed-demo
```

## Distribution Scenarios

This feature is especially useful for:

1. **CLI tool distribution**: users only need to download a single binary, no extra config required
2. **CI/CD environments**: container images only need to include the binary, no config files to copy
3. **Enterprise deployment**: ship default config while allowing user customisation via overrides

## File Structure

```
examples/06-embed/
├── main.go                    # Example code
├── .saker/
│   ├── settings.json          # Embedded default config
│   └── skills/
│       └── demo/
│           └── SKILL.md       # Embedded skill
└── README.md                  # This document
```

## Notes

- Embedded content is fixed at compile time and cannot be modified at runtime
- Embedding increases the binary file size
- Only embed the configuration and skills that are strictly necessary
- Sensitive data (such as API keys) must not be embedded; pass them via environment variables instead
