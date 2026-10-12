# 0016 The source is public under Apache-2.0

- Status: accepted
- Date: 2026-10-12
- Related: docs/threat-model.md, SECURITY.md, CONTRIBUTING.md, GOVERNANCE.md

## Context

Hoplet runs on developers' machines: the agent reads every Claude Code session, starts Claude Code with tool
permissions and writes worktrees, and the core stores all session content. The product promises no outside
service and no telemetry (docs/hosting.md#network); a user can only verify that by reading the code. The core
runs TypeScript source directly (ADR 0003), so the public image already ships the core's source. Nothing is sold,
so keeping the source closed protects no revenue. The organization's other products are private,
and their names must stay secret.

## Decision

The source is public under the Apache License 2.0 with a `NOTICE`, in `yangflow/hoplet` under the maintainer's
personal account. It starts from one commit of the prepared tree; the earlier private history, issues and pull
requests are not published. Contributions come under the same license with a Developer Certificate of Origin sign-off, without a
contributor licence agreement. Features are proposed as issues before code and triaged like the maintainer's own
(`GOVERNANCE.md`).

## Cost

Anyone can read the code, so no protection may rely on it being unknown (docs/threat-model.md). The history
behind the decisions before publication is not public; the ADRs and documents carry the reasons that remain true.
Without a contributor licence agreement, code contributed by others cannot later be relicensed without their
consent. Issues and pull requests from strangers take the maintainer's time.

## Revisit triggers

A paid plan or a hosted service that needs terms beyond Apache-2.0; contributions that need a contributor licence
agreement; the maintainer can no longer review contributions.
