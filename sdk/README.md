# @hoplet/sdk

A typed TypeScript client of the Hoplet core: the REST API and the browser WebSocket
([`docs/api.md`](../docs/api.md)). It uses the global `fetch` and `WebSocket` (Node 22+, browsers, Deno, Bun) and
has one dependency, `valibot`; the schemas of `shared/` are bundled into the build.

```ts
import { Hoplet } from '@hoplet/sdk';

const hoplet = new Hoplet({ url: process.env.HOPLET_URL, token: process.env.HOPLET_TOKEN });

const { sessionId, baseline } = await hoplet.createSession({
  deviceId,
  projectPath: '/work/app',
  provider: 'claude',
  text: 'Fix the failing test',
});
const turn = await hoplet.waitForTurn(sessionId, { since: baseline, timeout: 10 * 60_000 });
if (turn.result !== 'ok') console.error(turn.result, turn.error);
```

## Options

`new Hoplet({ url, token, fetch?, WebSocket? })`. `url` is the core's address, `token` a host token or a scoped API
token (docs/api.md#api-tokens). `fetch` and `WebSocket` replace the globals, for tests or for a runtime without
them. The SDK reads no environment variables; pass `HOPLET_URL` and `HOPLET_TOKEN` yourself.

## Calls

- `listSessions({ deviceId?, provider?, state? })`, `getSession(id)`.
- `createSession(input)` and `sendPrompt(id, text | input)` choose the `promptId` (or take yours) and repeat a
  lost request with the same one, so a retry never sends the prompt twice. Both return `{ promptId, baseline }`.
- `waitForTurn(id, { since?, startTimeout?, timeout?, untilBlocked?, drain?, signal? })` resolves with
  `{ result, state, error?, lineCount }` where `result` is `ok`, `failed`, `ended`, `blocked` or `timeout`. A
  timeout is a result, not an exception; the turn may still run. Without `since` a session that is not busy
  returns at once. It reads the state again on every event of the session and polls as a safety net.
- `readSession(id, { start?, limit?, direction? })` is an async generator of pages (at most 500 lines each); no
  call returns a whole session. `readBlock(id, line, block)` fetches one elided block.
- `answerPermission`, `interrupt`, `getQueue`, `listDevices`, `listProjects`, `health`.
- Host-token calls: `handoff`, `cancelHandoff`, `mintDevice`, `auditEvents`, `auditVerify`, `auditExport`,
  `listTokens`, `createToken`, `revokeToken`. A scoped token gets `forbidden`.
- `subscribe({ sessionIds? })` returns an `EventStream` (`for await`, `subscribe(id)`, `unsubscribe(id)`,
  `close()`). It mints a ticket for each connection and reconnects with a new one. After every (re)connection it
  yields `{ type: 'sdk.connected', reconnected }`: read the state again when `reconnected` is true.

## Errors

Every failure is a `HopletError` with `status`, `code` and `reason`. `code` is a stable code of the API
(`unauthorized`, `forbidden`, `session-not-found`, ...; an unknown one passes through) or one of the SDK's own:
`network-error` (no answer), `invalid-response` (not the documented shape), `protocol-version-mismatch` (the
core speaks another `protocolVersion`; both numbers are in the message) and `aborted` (the `signal` fired).

## Build

`pnpm --filter @hoplet/sdk build` writes `sdk/dist/` (one ESM file and the declarations).
