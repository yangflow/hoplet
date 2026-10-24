// A handoff's copy of project rules and the worktrees a clone's rules reach (ADR 0037).
import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type DeviceRules,
  type HandoffProgress,
  type PermissionRuleBody,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import {
  type Control,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import { moveMatch, planRuleCopy } from '../src/control/rule-copy.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const SESSION = 's1';
const TREE = 'b'.repeat(40);
const CLONE = '/home/b/r';
const WORKTREE = '/home/b/r-sess1234';

type Behavior = (msg: ServerMessage, device: Device) => void;

class Device {
  readonly sent: ServerMessage[] = [];
  open = true;
  readonly link: DeviceLink;
  behavior: Behavior = () => {};
  private readonly control: Control & { receive(link: DeviceLink, msg: ControlReply): void };
  constructor(control: Control & { receive(link: DeviceLink, msg: ControlReply): void }) {
    this.control = control;
    this.link = {
      send: (msg) => {
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      sendAndWait: async (msg) => {
        if (!this.open) throw new Error('closed');
        this.sent.push(msg);
        queueMicrotask(() => this.behavior(msg, this));
      },
      isOpen: () => this.open,
    };
  }
  reply(msg: Record<string, unknown>): void {
    this.control.receive(this.link, { v: PROTOCOL_VERSION, ...msg } as ControlReply);
  }
  rules(): DeviceRules[] {
    return this.sent.filter((m): m is DeviceRules => m.type === 'device.rules');
  }
}

/** What the target answers a rules push with: applied, refused, or nothing. */
type RulesAnswer = 'apply' | 'refuse' | 'silent' | 'disconnect';

async function fixture(answer: RulesAnswer = 'apply') {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-handoff-rules-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, { handoffTimeoutMs: 300, transferTimeoutMs: 3000 });
  store.append(
    { sessionId: SESSION, provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'owner' },
    [JSON.stringify({ type: 'user', cwd: '/tmp/proj', message: { role: 'user', content: 'hi' } })],
  );
  const bytes = Buffer.alloc(1000, 9);
  bytes[0] = 0x1f;
  bytes[1] = 0x8b;
  store.putSnapshotChunk({
    sessionId: SESSION,
    snapshotId: 'snap1',
    seq: 0,
    last: true,
    data: bytes.toString('base64'),
    createdAt: 1_700_000_000_000,
    baseCommit: 'a'.repeat(40),
    originUrl: 'git@example.test:o/r.git',
    treeHash: TREE,
    byteSize: bytes.length,
    sha256: createHash('sha256').update(bytes).digest('hex'),
  });
  store.upsertDevice('owner', 'Studio', '0.0.0');
  store.upsertDevice('target', 'Laptop', '0.0.0');
  const owner = new Device(control);
  const target = new Device(control);
  control.attach('owner', owner.link);
  control.attach('target', target.link);
  // Both agents said hello, so the core keeps their rules current.
  control.announceRules('owner');
  control.announceRules('target');
  const messages: HandoffProgress[] = [];
  control.subscribe((msg) => {
    if (msg.type === 'handoff.progress') messages.push(msg);
  });
  owner.behavior = (msg, d) => {
    if (msg.type === 'device.rules')
      d.reply({ type: 'device.rules.applied', revision: msg.revision });
    if (msg.type === 'handoff.prepare') {
      d.reply({
        type: 'handoff.prepared',
        sessionId: SESSION,
        handoffId: msg.handoffId,
        snapshotId: 'snap1',
        byteSize: store.getSession(SESSION)?.byteSize ?? 0,
      });
    }
  };
  let handing = false;
  target.behavior = (msg, d) => {
    if (msg.type === 'device.rules') {
      // Until the handoff reaches its rules step the target applies what it is sent.
      if (!handing || answer === 'apply') {
        d.reply({ type: 'device.rules.applied', revision: msg.revision });
      } else if (answer === 'refuse') {
        d.reply({ type: 'device.rules.applied', revision: msg.revision, error: 'unknown field' });
      } else if (answer === 'disconnect') {
        d.open = false;
        control.detach(d.link);
      }
    }
    if (msg.type === 'handoff.data' && msg.part === 'session' && msg.last) {
      d.reply({
        type: 'handoff.installed',
        sessionId: SESSION,
        handoffId: msg.handoffId,
        worktree: WORKTREE,
        branch: 'hoplet/sess1234',
        clone: CLONE,
      });
    }
    if (msg.type === 'handoff.verify') {
      void (async () => {
        const fingerprint = await store.streamFingerprint(SESSION);
        handing = true;
        d.reply({
          type: 'handoff.verified',
          sessionId: SESSION,
          handoffId: msg.handoffId,
          sessionBytes: fingerprint?.byteSize ?? 0,
          sessionSha256: fingerprint?.streamSha256 ?? '',
          treeHash: TREE,
        });
      })();
    }
  };
  const source = store.getSession(SESSION)?.projectPath ?? '';
  const rule = (
    deviceId: string,
    projectPath: string,
    action: PermissionRuleBody['action'],
    match: PermissionRuleBody['match'],
    tool = 'write',
  ) =>
    store.createPermissionRule(
      { scope: 'project', deviceId, projectPath, tool, match, action, note: '', enabled: true },
      Date.now(),
    );
  return { store, control, owner, target, messages, source, rule };
}

const projectRules = (store: Store, deviceId: string) =>
  store.listPermissionRules({ scope: 'project', deviceId });

const auditOf = (store: Store, type: string) =>
  store.listAudit({ type }, { order: 'asc', limit: 100 }).events;

describe('planning the copy', () => {
  it('moves the paths under the source project onto the target clone, the others as they are', () => {
    expect(
      moveMatch(
        {
          paths: ['/src/app/**', '/src/app', '/src/apple/x', '{workspace}/.env', '~/.ssh/**'],
          except: ['/src/app/tmp/**'],
          or: [{ paths: ['/src/app-wt/x'] }],
        },
        ['/src/app', '/src/app-wt'],
        '/dst/app/',
      ),
    ).toEqual({
      paths: ['/dst/app/**', '/dst/app', '/src/apple/x', '{workspace}/.env', '~/.ssh/**'],
      except: ['/dst/app/tmp/**'],
      or: [{ paths: ['/dst/app/x'] }],
    });
    // A target that is not an absolute `/` path cannot take a moved pattern.
    expect(moveMatch({ paths: ['/src/app/x'] }, ['/src/app'], 'C:\\dst')).toEqual({
      paths: ['/src/app/x'],
    });
  });

  it('copies what the target lacks, marks what it has, and keeps its own rules', () => {
    const entry = (
      id: string,
      deviceId: string,
      projectPath: string,
      action: 'deny' | 'ask' | 'allow',
      paths: string[],
      enabled = true,
    ) => ({
      id,
      tool: 'write',
      match: { paths },
      action,
      scope: 'project' as const,
      note: id,
      enabled,
      builtin: false,
      deviceId,
      projectPath,
      createdAt: 0,
      updatedAt: 0,
    });
    const plan = planRuleCopy(
      [
        entry('a', 'src', '/s', 'deny', ['/s/.env']),
        entry('b', 'src', '/s-wt', 'ask', ['{workspace}/x']),
        entry('c', 'src', '/s', 'allow', ['/s/off'], false),
        entry('d', 'src', '/other', 'deny', ['/other/x']),
        entry('e', 'dst', '/t', 'ask', ['{workspace}/x']),
        entry('f', 'dst', '/t', 'deny', ['/t/own']),
        entry('g', 'dst', '/t/sub', 'deny', ['/t/.env']),
      ],
      { deviceId: 'src', roots: ['/s', '/s-wt'] },
      { deviceId: 'dst', path: '/t' },
    );
    expect(plan.rules.map((r) => [r.state, r.note])).toEqual([
      ['copy', 'a'],
      ['same', 'b'],
      ['target', 'f'],
    ]);
    expect(plan.copies).toEqual([
      {
        scope: 'project',
        deviceId: 'dst',
        projectPath: '/t',
        tool: 'write',
        match: { paths: ['/t/.env'] },
        action: 'deny',
        note: 'a',
        enabled: true,
      },
    ]);
  });
});

describe('the worktrees of a clone', () => {
  it('list the clone project rules again under each worktree beside it, not one below it', async () => {
    const f = await fixture();
    const made = f.rule('target', CLONE, 'deny', { paths: ['{workspace}/.env'] });
    f.store.recordNewWorktree('s-new', 'target', {
      clone: CLONE,
      path: '/home/b/r-abcd1234',
      branch: 'hoplet/abcd1234',
    });
    f.store.recordNewWorktree('s-in', 'target', {
      clone: CLONE,
      path: `${CLONE}/.worktrees/x`,
      branch: 'hoplet/x',
    });
    f.control.syncRules(['target']);
    const last = f.target.rules().at(-1);
    expect(last?.projects.map((p) => [p.projectPath, p.rule.id])).toEqual([
      [CLONE, made.id],
      ['/home/b/r-abcd1234', made.id],
    ]);
  });
});

describe('a handoff that copies project rules', () => {
  it('copies the missing rules before the commit, waits for the target and records it', async () => {
    const f = await fixture();
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    f.rule('owner', f.source, 'ask', { command: 'git push' }, 'run');
    f.rule('target', CLONE, 'ask', { command: 'git push' }, 'run');
    f.rule('target', CLONE, 'allow', { paths: ['{workspace}/**'] });

    const preview = f.control.handoffRulesPreview(SESSION, 'target', CLONE);
    expect(preview.ok && preview.preview.rules.map((r) => r.state)).toEqual([
      'copy',
      'same',
      'target',
    ]);

    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
      copyProjectRules: true,
    });
    expect(result).toMatchObject({ ok: true, ownerDeviceId: 'target', rulesCopied: 1 });
    expect(f.messages.map((m) => m.step)).toContain('rules');
    expect(f.messages.find((m) => m.step === 'rules')?.copied).toBe(1);
    const steps = [...new Set(f.messages.map((m) => m.step))];
    expect(steps.indexOf('rules')).toBe(steps.indexOf('verify') + 1);
    expect(steps.indexOf('commit')).toBe(steps.indexOf('rules') + 1);

    // The copy lives on the target's clone with its path moved; the source keeps its rules.
    const copied = projectRules(f.store, 'target').find((r) => r.action === 'deny');
    expect(copied).toMatchObject({ projectPath: CLONE, match: { paths: [`${CLONE}/.env`] } });
    expect(projectRules(f.store, 'owner')).toHaveLength(2);

    // The target had the set with the copy, for its worktree too, before it was told to commit.
    const types = f.target.sent.map((m) => m.type);
    const withCopy = f.target
      .rules()
      .findIndex((r) =>
        r.projects.some((p) => p.rule.id === copied?.id && p.projectPath === WORKTREE),
      );
    expect(withCopy).toBeGreaterThanOrEqual(0);
    const pushAt = f.target.sent.indexOf(f.target.rules()[withCopy] as ServerMessage);
    expect(pushAt).toBeLessThan(types.indexOf('handoff.commit'));

    const change = auditOf(f.store, 'config.changed').at(-1);
    expect(change?.summary).toEqual({
      area: 'permission-rules',
      action: 'copied',
      deviceId: 'target',
      fromDeviceId: 'owner',
      sessionId: SESSION,
      count: 1,
      byAction: { deny: 1, ask: 0, allow: 0 },
    });
    expect(JSON.stringify(change)).not.toContain('.env');
    expect(auditOf(f.store, 'session.handoff').at(-1)?.summary).toMatchObject({
      outcome: 'committed',
      rulesCopied: 1,
    });
  });

  it('does not copy without the owner asking, and skips the step when the target has every rule', async () => {
    const f = await fixture();
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    const plain = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
    });
    expect(plain.ok && plain.rulesCopied).toBeUndefined();
    expect(projectRules(f.store, 'target')).toHaveLength(0);
    expect(f.messages.map((m) => m.step)).not.toContain('rules');
  });

  it('removes the copies again when the target refuses them, and nothing moves', async () => {
    const f = await fixture('refuse');
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
      copyProjectRules: true,
    });
    expect(result).toEqual({ ok: false, error: 'rejected', reason: 'rules-refused' });
    expect(f.store.getSession(SESSION)?.ownerDeviceId).toBe('owner');
    expect(projectRules(f.store, 'target')).toHaveLength(0);
    expect(f.target.sent.map((m) => m.type)).not.toContain('handoff.commit');
    expect(auditOf(f.store, 'config.changed').map((e) => e.summary.action)).toEqual(
      expect.arrayContaining(['copied', 'removed']),
    );
    expect(auditOf(f.store, 'session.handoff').at(-1)?.summary).toMatchObject({
      outcome: 'failed',
      reason: 'rules-refused',
      rulesCopied: 0,
    });
  });

  it('fails as target-offline when the target leaves before it confirms', async () => {
    const f = await fixture('disconnect');
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
      copyProjectRules: true,
    });
    expect(result).toMatchObject({ ok: false, error: 'target-offline' });
    expect(projectRules(f.store, 'target')).toHaveLength(0);
    expect(f.store.getSession(SESSION)?.ownerDeviceId).toBe('owner');
  });

  it('fails as timeout when the target never answers', async () => {
    const f = await fixture('silent');
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
      copyProjectRules: true,
    });
    expect(result).toMatchObject({ ok: false, error: 'timeout' });
    expect(projectRules(f.store, 'target')).toHaveLength(0);
  });

  it('refuses a copy past the cap of the host', async () => {
    const f = await fixture();
    f.rule('owner', f.source, 'deny', { paths: [`${f.source}/.env`] });
    for (let i = f.store.permissionRuleCount(); i < 500; i++) {
      f.rule('third', `/p${i}`, 'deny', { paths: [`/p${i}/x`] });
    }
    const result = await f.control.handoff({
      sessionId: SESSION,
      targetDeviceId: 'target',
      targetPath: CLONE,
      copyProjectRules: true,
    });
    expect(result).toEqual({ ok: false, error: 'rejected', reason: 'rules-limit' });
    expect(projectRules(f.store, 'target')).toHaveLength(0);
  });
});

describe('the preview', () => {
  it('answers session-not-found and not-found', async () => {
    const f = await fixture();
    expect(f.control.handoffRulesPreview('nope', 'target', CLONE)).toEqual({
      ok: false,
      error: 'session-not-found',
    });
    expect(f.control.handoffRulesPreview(SESSION, 'ghost', CLONE)).toEqual({
      ok: false,
      error: 'not-found',
    });
    expect(f.control.handoffRulesPreview(SESSION, 'target', CLONE)).toEqual({
      ok: true,
      preview: {
        sourceDeviceId: 'owner',
        sourcePath: f.source,
        targetDeviceId: 'target',
        targetPath: CLONE,
        rules: [],
      },
    });
  });
});
