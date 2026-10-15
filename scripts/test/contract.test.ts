// The contract between shared/src/wire/ and agent/internal/sync/protocol.go (docs/architecture.md#wire-protocol).
// The Go file is read as text: `const Type<Struct> = "<wire type>"` names a message, `type <Struct> struct`
// lists its fields by their `json:"name[,omitempty]"` tags, and `omitempty` marks an optional field.
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  ClientMessageSchema,
  ERROR_CODES,
  isSessionId,
  PROTOCOL_VERSION,
  SESSION_ID_PATTERN,
  WIRE_MESSAGES,
  WIRE_OBJECTS,
  wireTypes,
} from '../../shared/src/index.ts';

const root = path.resolve(import.meta.dirname, '../..');
const protocolGo = readFileSync(path.join(root, 'agent/internal/sync/protocol.go'), 'utf8');

type Fields = Record<string, { optional: boolean }>;

type Contract = {
  version: number;
  /** wire type -> fields of the message; `webOnly` messages have no Go struct */
  messages: Record<string, { fields: Fields; webOnly: boolean }>;
  /** nested objects by Go struct name */
  objects: Record<string, Fields>;
  errorCodes: string[];
};

type GoContract = {
  version: number | null;
  /** wire type -> Go struct name */
  types: Record<string, string>;
  structs: Record<string, Fields>;
  /** values of the `Code…` constants */
  codes: string[];
};

type Entries = Record<string, { type: string }>;

function fieldsOf(entries: Entries): Fields {
  return Object.fromEntries(
    Object.entries(entries).map(([name, schema]) => [
      name,
      { optional: schema.type === 'optional' },
    ]),
  );
}

function describeShared(): Contract {
  const messages: Contract['messages'] = {};
  for (const [type, spec] of Object.entries(WIRE_MESSAGES)) {
    messages[type] = {
      fields: fieldsOf(spec.schema.entries as Entries),
      webOnly: spec.direction === 'core-to-web' || spec.direction === 'web-to-core',
    };
  }
  const objects: Contract['objects'] = {};
  for (const [name, schema] of Object.entries(WIRE_OBJECTS)) {
    objects[name] = fieldsOf(schema.entries as Entries);
  }
  return { version: PROTOCOL_VERSION, messages, objects, errorCodes: [...ERROR_CODES] };
}

function parseGo(source: string): GoContract {
  const version = /\bconst ProtocolVersion = (\d+)/.exec(source)?.[1];
  const types: Record<string, string> = {};
  for (const m of source.matchAll(/^\s*Type(\w+)\s*=\s*"([^"]+)"/gm)) {
    types[m[2] as string] = m[1] as string;
  }
  const structs: Record<string, Fields> = {};
  for (const m of source.matchAll(/^type (\w+) struct \{\n([\s\S]*?)\n\}/gm)) {
    const fields: Fields = {};
    for (const line of (m[2] as string).split('\n')) {
      const tag = /`json:"([^",]+)(,omitempty)?"`/.exec(line);
      if (tag) fields[tag[1] as string] = { optional: tag[2] !== undefined };
    }
    structs[m[1] as string] = fields;
  }
  const codes = [...source.matchAll(/^\s*(?:const\s+)?Code\w+\s*=\s*"([^"]+)"/gm)].map(
    (m) => m[1] as string,
  );
  return { version: version === undefined ? null : Number(version), types, structs, codes };
}

function compareFields(label: string, ts: Fields, go: Fields | undefined, out: string[]): void {
  if (!go) {
    out.push(`${label}: no Go struct`);
    return;
  }
  for (const [name, field] of Object.entries(ts)) {
    const other = go[name];
    if (!other) out.push(`${label}: field "${name}" is missing in Go`);
    else if (other.optional !== field.optional) {
      out.push(
        `${label}: field "${name}" is ${field.optional ? 'optional' : 'required'} in shared but ${
          other.optional ? 'omitempty' : 'not omitempty'
        } in Go`,
      );
    }
  }
  for (const name of Object.keys(go)) {
    if (!(name in ts)) out.push(`${label}: field "${name}" is missing in shared`);
  }
}

/** Every difference between the two sides; empty when they agree. */
function compareContract(ts: Contract, go: GoContract): string[] {
  const out: string[] = [];
  if (go.version !== ts.version) {
    out.push(`protocol version: shared ${ts.version}, Go ${go.version}`);
  }
  const known = new Set<string>();
  for (const [type, message] of Object.entries(ts.messages)) {
    const struct = go.types[type];
    if (message.webOnly) {
      if (struct) out.push(`message "${type}" is for the web UI only but Go declares ${struct}`);
      continue;
    }
    if (!struct) {
      out.push(`message "${type}" has no Type constant in Go`);
      continue;
    }
    known.add(struct);
    compareFields(`message "${type}"`, message.fields, go.structs[struct], out);
  }
  for (const [type, struct] of Object.entries(go.types)) {
    if (!(type in ts.messages)) out.push(`Go message "${type}" (${struct}) is not in shared`);
  }
  for (const [name, fields] of Object.entries(ts.objects)) {
    known.add(name);
    compareFields(`object ${name}`, fields, go.structs[name], out);
  }
  for (const name of Object.keys(go.structs)) {
    if (!known.has(name)) out.push(`Go struct ${name} is neither a message nor a shared object`);
  }
  for (const code of go.codes) {
    if (!ts.errorCodes.includes(code)) out.push(`Go error code "${code}" is not in shared`);
  }
  return out;
}

const shared = describeShared();
const go = parseGo(protocolGo);

function fieldsOfMessage(contract: Contract, type: string): Fields {
  const message = contract.messages[type];
  if (!message) throw new Error(`no message ${type}`);
  return message.fields;
}

function fieldsOfObject(contract: Contract, name: string): Fields {
  const fields = contract.objects[name];
  if (!fields) throw new Error(`no object ${name}`);
  return fields;
}

function clone<T>(value: T): T {
  return structuredClone(value);
}

describe('wire contract', () => {
  it('parses what it compares', () => {
    expect(Object.keys(go.types).length).toBeGreaterThan(25);
    expect(go.structs.SessionAppend).toEqual({
      v: { optional: false },
      type: { optional: false },
      sessionId: { optional: false },
      projectDir: { optional: false },
      filePath: { optional: false },
      byteStart: { optional: false },
      encoding: { optional: false },
      lines: { optional: false },
      title: { optional: true },
    });
    expect(go.types['device.hello']).toBe('DeviceHello');
    expect(go.codes).toContain('protocol-version-mismatch');
  });

  it('agrees: shared/src/wire/ and agent/internal/sync/protocol.go declare the same messages', () => {
    expect(compareContract(shared, go)).toEqual([]);
  });

  it('declares the new-session messages and capabilities on both sides', () => {
    expect(go.types['runtime.start']).toBe('RuntimeStart');
    expect(go.types['runtime.started']).toBe('RuntimeStarted');
    expect(go.types['runtime.start.rejected']).toBe('RuntimeStartRejected');
    expect(go.structs.RuntimeStart).toEqual({
      v: { optional: false },
      type: { optional: false },
      requestId: { optional: false },
      projectPath: { optional: false },
      provider: { optional: false },
      options: { optional: false },
      text: { optional: false },
      attachments: { optional: true },
      promptId: { optional: false },
    });
    expect(go.types['attachment.put']).toBe('AttachmentPut');
    expect(go.structs.RuntimePrompt?.attachments).toEqual({ optional: true });
    expect(go.structs.RuntimeStartOptions).toEqual({
      model: { optional: true },
      thinking: { optional: true },
      fast: { optional: true },
    });
    expect(go.structs.RuntimeCapabilities).toEqual({
      permissions: { optional: false },
      interrupt: { optional: false },
      thinking: { optional: false },
      fast: { optional: false },
      models: { optional: false },
      images: { optional: true },
      setModel: { optional: true },
    });
    expect(go.structs.RuntimePrompt?.model).toEqual({ optional: true });
    expect(go.structs.RuntimeState?.model).toEqual({ optional: true });
    expect(go.structs.DeviceHello?.providers).toEqual({ optional: true });
    expect(shared.messages['runtime.start']).toMatchObject({ webOnly: false });
    expect(go.version).toBe(PROTOCOL_VERSION);
  });

  it('fails when Go adds a field', () => {
    const edited = protocolGo.replace(
      'type SessionAck struct {',
      'type SessionAck struct {\n\tExtra string `json:"extra"`',
    );
    expect(compareContract(shared, parseGo(edited))).toEqual([
      'message "session.ack": field "extra" is missing in shared',
    ]);
  });

  it('fails when Go renames a field', () => {
    const edited = protocolGo.replace('`json:"byteStart"`', '`json:"byteOffset"`');
    expect(compareContract(shared, parseGo(edited))).toEqual([
      'message "session.append": field "byteStart" is missing in Go',
      'message "session.append": field "byteOffset" is missing in shared',
    ]);
  });

  it('fails when Go changes whether a field is optional', () => {
    const edited = protocolGo.replace('`json:"title,omitempty"`', '`json:"title"`');
    expect(compareContract(shared, parseGo(edited))).toContain(
      'message "session.append": field "title" is optional in shared but not omitempty in Go',
    );
  });

  it('fails when Go renames or drops a message type', () => {
    const renamed = protocolGo.replace(
      /(TypeSessionAck\s*=\s*)"session\.ack"/,
      '$1"session.acked"',
    );
    expect(renamed).not.toBe(protocolGo);
    expect(compareContract(shared, parseGo(renamed))).toEqual([
      'message "session.ack" has no Type constant in Go',
      'Go message "session.acked" (SessionAck) is not in shared',
      'Go struct SessionAck is neither a message nor a shared object',
    ]);
  });

  it('fails when Go declares a struct that shared does not know', () => {
    const edited = `${protocolGo}\ntype Stray struct {\n\tA string \`json:"a"\`\n}\n`;
    expect(compareContract(shared, parseGo(edited))).toEqual([
      'Go struct Stray is neither a message nor a shared object',
    ]);
  });

  it('fails when Go changes the protocol version or an error code', () => {
    const version = protocolGo.replace(
      `const ProtocolVersion = ${PROTOCOL_VERSION}`,
      `const ProtocolVersion = ${PROTOCOL_VERSION + 1}`,
    );
    expect(compareContract(shared, parseGo(version))).toEqual([
      `protocol version: shared ${PROTOCOL_VERSION}, Go ${PROTOCOL_VERSION + 1}`,
    ]);
    const code = protocolGo.replace('"protocol-version-mismatch"', '"protocol-mismatch"');
    expect(compareContract(shared, parseGo(code))).toEqual([
      'Go error code "protocol-mismatch" is not in shared',
    ]);
  });

  it('fails when shared adds, renames or removes a field, a message or an object', () => {
    const added = clone(shared);
    fieldsOfMessage(added, 'session.ack').extra = { optional: false };
    expect(compareContract(added, go)).toEqual([
      'message "session.ack": field "extra" is missing in Go',
    ]);

    const renamed = clone(shared);
    const fields = fieldsOfMessage(renamed, 'session.resync');
    fields.offset = { optional: false };
    delete fields.byteSize;
    expect(compareContract(renamed, go)).toEqual([
      'message "session.resync": field "offset" is missing in Go',
      'message "session.resync": field "byteSize" is missing in shared',
    ]);

    const message = clone(shared);
    message.messages['session.stop'] = { fields: {}, webOnly: false };
    expect(compareContract(message, go)).toEqual([
      'message "session.stop" has no Type constant in Go',
    ]);

    const object = clone(shared);
    fieldsOfObject(object, 'RuntimeCapabilities').extra = { optional: false };
    expect(compareContract(object, go)).toEqual([
      'object RuntimeCapabilities: field "extra" is missing in Go',
    ]);

    const optional = clone(shared);
    fieldsOfMessage(optional, 'session.rejected').ownerDeviceId = { optional: false };
    expect(compareContract(optional, go)).toEqual([
      'message "session.rejected": field "ownerDeviceId" is required in shared but omitempty in Go',
    ]);
  });

  it('keeps the browser-only messages out of Go and the agent messages in the client union', () => {
    expect(wireTypes('core-to-web', 'web-to-core')).toEqual([
      'session.appended',
      'device.presence',
      'subscribe',
      'unsubscribe',
      'session.runtime',
      'session.output',
      'session.permission',
      'handoff.progress',
    ]);
    const inUnion = ClientMessageSchema.options.map((option) => option.entries.type.literal).sort();
    expect(inUnion).toEqual([...wireTypes('device-to-core')].sort());
  });
});

describe('lanes', () => {
  it('agrees with the Go sender on which messages the agent sends on the bulk lane', () => {
    const lanesGo = readFileSync(path.join(root, 'agent/internal/sync/lanes.go'), 'utf8');
    const cases = /func laneOf[\s\S]*?case ([\w, ]+):\s*\n\s*return LaneBulk/.exec(lanesGo)?.[1];
    const goBulk = (cases ?? '')
      .split(',')
      .map((name) => Object.entries(go.types).find(([, struct]) => struct === name.trim())?.[0])
      .sort();
    const sharedBulk = Object.entries(WIRE_MESSAGES)
      .filter(([, spec]) => spec.lane === 'bulk' && spec.direction === 'device-to-core')
      .map(([type]) => type)
      .sort();
    expect(goBulk).toEqual(sharedBulk);
  });

  it('puts the sync stream, the workspace transfer, file and diff answers and prompt images on the bulk lane, the rest on control', () => {
    const bulk = Object.entries(WIRE_MESSAGES)
      .filter(([, spec]) => spec.lane === 'bulk')
      .map(([type]) => type)
      .sort();
    expect(bulk).toEqual([
      'attachment.put',
      'handoff.data',
      'session.ack',
      'session.append',
      'session.rejected',
      'session.resync',
      'session.workspace.diff',
      'session.workspace.file',
      'workspace.snapshot',
    ]);
    for (const spec of Object.values(WIRE_MESSAGES)) {
      expect(['control', 'bulk']).toContain(spec.lane);
    }
    for (const type of [
      'runtime.prompt',
      'runtime.start',
      'runtime.started',
      'runtime.start.rejected',
      'runtime.state',
      'runtime.output',
      'session.workspace.list',
      'session.workspace',
      'session.workspace.rejected',
      'session.workspace.file.read',
      'session.workspace.diff.read',
      'device.projects.list',
      'device.projects',
      'device.dirs.list',
      'device.dirs',
      'device.dirs.rejected',
      'handoff.prepare',
      'handoff.install',
      'handoff.verify',
      'handoff.commit',
      'handoff.abort',
      'handoff.prepared',
      'handoff.verified',
    ] as const) {
      expect(WIRE_MESSAGES[type].lane, type).toBe('control');
    }
  });
});

describe('session identifiers', () => {
  const cases = JSON.parse(
    readFileSync(path.join(root, 'shared/session-id-cases.json'), 'utf8'),
  ) as {
    valid: string[];
    invalid: string[];
  };

  it('accepts and refuses the shared cases in TypeScript', () => {
    expect(cases.valid.length).toBeGreaterThan(0);
    expect(cases.invalid.length).toBeGreaterThan(0);
    for (const id of cases.valid) expect(isSessionId(id), JSON.stringify(id)).toBe(true);
    for (const id of cases.invalid) expect(isSessionId(id), JSON.stringify(id)).toBe(false);
    for (const value of [undefined, null, 1, true, {}, []]) expect(isSessionId(value)).toBe(false);
    expect(SESSION_ID_PATTERN.source).toBe('^[A-Za-z0-9_-]+$');
  });

  it('accepts and refuses the same cases in Go (provider.ValidSessionID)', () => {
    const output = execFileSync(
      'go',
      ['test', '-count=1', '-run', '^TestSessionIDCases$', '-v', './internal/provider'],
      { cwd: path.join(root, 'agent'), encoding: 'utf8' },
    );
    expect(output).toContain('--- PASS: TestSessionIDCases');
  }, 120_000);
});
