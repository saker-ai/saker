# Security Policy

## Supported Versions

We release patches for security vulnerabilities for the following versions:

| Version | Supported |
| ------- | --------- |
| Latest release | Yes |
| Previous release | Yes |
| Older versions | No |

## Reporting a Vulnerability

**Do not report security vulnerabilities through public GitHub issues.**

Instead, please report them privately via:

- Email: **cinience@hotmail.com** with the subject line `[Security] Saker vulnerability report`
- GitHub: Use the [private vulnerability reporting](https://github.com/saker-ai/saker/security/advisories/new) feature

Please include the following information:

1. Type of vulnerability (e.g., injection, privilege escalation, sandbox escape)
2. Affected component and version
3. Step-by-step instructions to reproduce
4. Potential impact and attack scenario
5. Any suggested mitigation

We will respond within **48 hours** with an initial assessment and expected timeline for a fix. Critical vulnerabilities (sandbox escapes, credential leaks) will be prioritized for immediate patching.

## Security Model

Saker can execute tools, read files, call external model providers, and run generated workflows. Treat it as a powerful local automation tool.

- Project runtime state is stored under `.saker/` and ignored by git.
- Web credentials should be configured with `--auth-user` and `--auth-pass` before exposing the server beyond localhost.
- API keys should stay in environment variables or local settings files.
- The CLI supports multiple sandbox backends (`host`, `landlock`, `gvisor`, `govm`). Use `landlock` or a virtualized backend for untrusted projects when available.

## What We Consider a Security Issue

- Sandbox escape or privilege escalation in any backend
- Exposure of API keys, credentials, or secrets through logs, outputs, or API responses
- Authentication bypass in the embedded web server
- Command injection through tool execution or model output
- Path traversal that allows access outside the project directory