# 0005 Per-device tokens

- Status: accepted
- Date: 2026-10-08
- Related: docs/threat-model.md#trust-boundaries, docs/spec.md#add-a-machine

## Context

Two kinds of clients reach the core: the operator (web UI, host CLI) and agents. An agent receives control
messages that run Claude Code and write files on its machine, and it appends to sessions it owns. A single
shared secret would let any leaked agent configuration act as the operator and as every other agent.

## Decision

The host token (`HOPLET_TOKEN`) authorizes the REST API and the browser WebSocket. It cannot identify as an
agent. Each agent has its own device token, minted by the operator through `POST /api/devices`; the core
stores only its SHA-256 hash and returns the plaintext once. A device token authorizes only the agent
WebSocket for that `device_id`, and `device.hello` must name the same `device_id`.

## Cost

Joining a machine needs a mint step and a configuration file on that machine. A lost device token is replaced
by minting again for the same `device_id`.

## Revisit triggers

Hoplet gains users other than the single operator, which requires roles instead of one host token.
