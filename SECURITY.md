# Security Policy

Hoplet runs agents on your machines and connects your devices to them, so security reports are taken seriously.

## Reporting a vulnerability

**Do not open a public issue.** Report privately through GitHub:

1. Go to [Report a vulnerability](https://github.com/yangflow/hoplet/security/advisories/new).
2. Describe the issue, the affected component and version, and steps to reproduce.

You'll get an acknowledgement as soon as possible. Hoplet is maintained by one person, so fixes are best effort; you'll be kept informed and credited in the advisory unless you prefer otherwise.

## Scope

Especially relevant:

- The Noise handshake, pairing and device revocation
- The relay and anything that could expose session content or let a device act without pairing
- Local access to the daemon (Unix socket, loopback WebSocket)
- Credentials or tokens leaking into logs, harness environments or push payloads
- Approval bypass in Hoplet's own MCP tools

Out of scope: vulnerabilities in third-party harnesses (Claude Code, Codex, Hermes, …) themselves — report those to their maintainers.

## Supported versions

Hoplet is pre-1.0. Only the latest release receives security fixes.
