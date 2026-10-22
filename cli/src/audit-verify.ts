/**
 * Offline verification of an audit export (`hoplet audit verify --file`). It shares no code with the core: the
 * hash is SHA-256 of the previous hash and the canonical JSON (keys sorted, no whitespace, integers only) of
 * the other fields, named as the database names them (docs/spec.md#audit-log).
 */
import { createHash } from 'node:crypto';

const GENESIS = '0'.repeat(64);

export type FileBreak = { seq: number; reason: string; message: string };
export type FileVerdict = {
  ok: boolean;
  checked: number;
  head: { seq: number; hash: string } | null;
  firstBreak: FileBreak | null;
};

function canonical(value: unknown): string {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') {
    return JSON.stringify(value);
  }
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value)) throw new Error('not an integer');
    return String(value);
  }
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) =>
      a < b ? -1 : a > b ? 1 : 0,
    );
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(',')}}`;
  }
  throw new Error('not canonical');
}

type Line = Record<string, unknown> & { seq: number; prevHash: string; hash: string };

function hashOf(r: Line): string {
  return createHash('sha256')
    .update(r.prevHash)
    .update(
      canonical({
        seq: r.seq,
        at: r.at,
        recorded_at: r.recordedAt,
        type: r.type,
        session_id: r.sessionId,
        device_id: r.deviceId,
        actor_kind: r.actorKind,
        actor_id: r.actorId,
        channel: r.channel,
        ref: r.ref,
        summary: r.summary,
      }),
    )
    .digest('hex');
}

/** Walks the JSON lines of an export; `expected` is a head written down elsewhere. */
export function verifyAuditFile(
  text: string,
  expected?: { seq: number; hash: string },
): FileVerdict {
  const lines: Line[] = [];
  const rows = text.split('\n').filter((line) => line.trim() !== '');
  for (const [index, raw] of rows.entries()) {
    try {
      const parsed = JSON.parse(raw) as Line;
      if (typeof parsed.seq !== 'number' || typeof parsed.hash !== 'string') {
        throw new Error('shape');
      }
      lines.push(parsed);
    } catch {
      return {
        ok: false,
        checked: index,
        head: null,
        firstBreak: {
          seq: lines.at(-1)?.seq ?? 0,
          reason: 'unreadable',
          message: `line ${index + 1} is not an audit record`,
        },
      };
    }
  }
  const checkpoints = lines
    .filter((r) => r.type === 'audit.checkpoint')
    .map((r) => r.summary as { removedThrough?: unknown; removedHash?: unknown });
  let broken: FileBreak | null = null;
  let checked = 0;
  let previous: Line | null = null;
  for (const r of lines) {
    checked++;
    if (!previous) {
      const starts =
        r.prevHash === GENESIS
          ? r.seq === 1
          : checkpoints.some((c) => c.removedThrough === r.seq - 1 && c.removedHash === r.prevHash);
      if (!starts) {
        broken = {
          seq: r.seq,
          reason: 'removed-outside-retention',
          message: `records before seq ${r.seq} were removed outside retention`,
        };
        break;
      }
    } else if (r.seq !== previous.seq + 1) {
      broken = {
        seq: r.seq,
        reason: 'gap',
        message: `records from seq ${previous.seq + 1} to ${r.seq - 1} are missing`,
      };
      break;
    } else if (r.prevHash !== previous.hash) {
      broken = {
        seq: r.seq,
        reason: 'prev-hash-mismatch',
        message: `record ${r.seq} does not follow the hash of record ${previous.seq}`,
      };
      break;
    }
    let matches = false;
    try {
      matches = hashOf(r) === r.hash;
    } catch {
      matches = false;
    }
    if (!matches) {
      broken = {
        seq: r.seq,
        reason: 'hash-mismatch',
        message: `record ${r.seq} does not match its hash`,
      };
      break;
    }
    previous = r;
  }
  if (!broken && expected) {
    const stored = lines.find((r) => r.seq === expected.seq);
    if (!stored) {
      broken = {
        seq: expected.seq,
        reason: 'head-missing',
        message: `no record has seq ${expected.seq}`,
      };
    } else if (stored.hash !== expected.hash) {
      broken = {
        seq: expected.seq,
        reason: 'head-mismatch',
        message: `record ${expected.seq} does not have the hash given`,
      };
    }
  }
  const last = lines.at(-1);
  return {
    ok: broken === null,
    checked,
    head: last ? { seq: last.seq, hash: last.hash } : null,
    firstBreak: broken,
  };
}
