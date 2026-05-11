# Contributing to saker

Thank you for your interest in contributing to saker. This document covers the process for reporting issues, proposing features, and submitting code changes.

## Reporting Bugs

Use [GitHub Issues](https://github.com/cinience/saker/issues) to report bugs. Before opening a new issue, search existing issues to avoid duplicates.

A good bug report includes:

- Go version (`go version`) and OS
- Minimal reproducer or steps to reproduce
- Expected behavior vs. actual behavior
- Relevant error messages or stack traces

## Suggesting Features

Open a GitHub Issue with the `enhancement` label. Describe the problem the feature solves and, if possible, sketch the proposed API or behavior. Features that align with the KISS/YAGNI principles and the existing modular architecture are more likely to be accepted.

## Development Setup

**Prerequisites**: Go 1.26 or later, Node.js 22 or later, npm, and `make`.
`golangci-lint` is needed for `make lint`.

```bash
# Clone the repository
git clone https://github.com/cinience/saker.git
cd saker

# Install Go dependencies
go mod download

# Install frontend dependencies (pnpm workspace — installs all sub-packages)
pnpm install

# Run a fast backend test loop
make test-short

# Run linter
make lint

# Build the full embedded app
make build
```

To run examples you need an Anthropic API key:

```bash
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY
source .env
go run ./examples/01-basic
```

## Code Style

**Formatting**: All code must be formatted with `gofmt`. The CI pipeline enforces this.

**Linting**: Run `golangci-lint run` before submitting. Fix all reported issues.

**Naming conventions**:
- Interfaces: noun, no `I` prefix — `Model`, `Tool`, `ToolExecutor`
- Concrete types: descriptive — `AnthropicProvider`, `BashTool`
- Options structs: use the functional options pattern
- Sentinel errors: package-level `var ErrXxx = errors.New("pkg: message")`

**Concurrency**:
- Use `sync.RWMutex` for shared mutable state
- Always respect `ctx.Done()` in loops and blocking calls
- Manage goroutine lifecycles explicitly; no naked goroutines

**Error handling**:
```go
// Wrap errors with context
return fmt.Errorf("execute tool %s: %w", name, err)

// Check for specific errors
if errors.Is(err, ErrMaxIterations) { ... }
```

**File size**: Keep files focused on a single responsibility. If a file grows large, split by concern (validators, helpers, interfaces) rather than keeping everything in one place.

## Testing

New features and bug fixes must include tests where practical. The project uses table-driven tests:

```go
func TestMyFeature(t *testing.T) {
    cases := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "foo", "bar", false},
        {"empty input", "", "", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := MyFeature(tc.input)
            if (err != nil) != tc.wantErr {
                t.Fatalf("unexpected error: %v", err)
            }
            if got != tc.want {
                t.Errorf("got %q, want %q", got, tc.want)
            }
        })
    }
}
```

Tests are co-located with source files (`*_test.go`). Integration tests live in `test/integration/`.

Run the full suite before submitting:

```bash
make test
make test-race
make coverage
make oss-check
```

## Pull Request Process

1. Fork the repository and create a branch from `main`.
2. Make your changes, including tests and any necessary documentation updates.
3. Ensure `make test`, `make test-race`, `make lint`, and `make oss-check` all pass for changes that affect those areas.
4. Open a pull request against `main`. Fill in the PR description with what changed and why.
5. A maintainer will review the PR. Address feedback by pushing additional commits to the same branch.
6. Once approved, a maintainer will merge the PR.

Keep pull requests focused. Large, unrelated changes in a single PR are harder to review and slower to merge.

## Commit Message Conventions

This project follows [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body]

[optional footer]
```

Common types:

| Type | When to use |
|------|-------------|
| `feat` | New feature |
| `fix` | Bug fix |
| `refactor` | Code change that is not a feature or fix |
| `test` | Adding or updating tests |
| `docs` | Documentation only |
| `chore` | Build, tooling, dependency updates |
| `perf` | Performance improvement |

Examples:

```
feat(tool): add glob built-in tool with regex support

fix(middleware): prevent nil pointer in AfterTool stage when result is empty

test(agent): add table-driven tests for max iteration limit
```

The summary line should be lowercase, imperative mood, and under 72 characters. Do not end with a period.

## License

By contributing, you agree that your contributions will be licensed under the Saker Source License Version 1.0 (SKL-1.0). See [LICENSE](LICENSE) for the full license text.
