/**
 * The audit export (ADR 0030): the records the filters match, in `seq` order, streamed in pages of 1000 as
 * JSON lines or CSV. At most two exports run at once; the records are read up to the head the export saw
 * when it started.
 */
import type { ServerResponse } from 'node:http';
import type { AuditEvent, AuditExportQuery, AuditFilter } from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { Store } from '../store/index.ts';
import { ApiError, RawResponse } from './http.ts';

export const AUDIT_EXPORT_PAGE = 1000;
export const AUDIT_EXPORT_CONCURRENCY = 2;

export const CSV_COLUMNS = [
  'seq',
  'at',
  'recordedAt',
  'type',
  'sessionId',
  'deviceId',
  'actorKind',
  'actorId',
  'channel',
  'ref',
  'summary',
  'prevHash',
  'hash',
] as const;

/** One CSV cell: quoted, and a text that a spreadsheet would run as a formula gets a leading `'`. */
export function csvCell(value: unknown): string {
  if (value === null || value === undefined) return '';
  let text = typeof value === 'string' ? value : JSON.stringify(value);
  if (typeof value === 'string' && /^[=+\-@\t\r]/.test(text)) text = `'${text}`;
  return /[",\r\n]/.test(text) || text.startsWith("'") ? `"${text.replaceAll('"', '""')}"` : text;
}

export function csvLine(event: AuditEvent): string {
  return `${CSV_COLUMNS.map((column) => csvCell(event[column])).join(',')}\r\n`;
}

export function jsonLine(event: AuditEvent): string {
  return `${JSON.stringify(event)}\n`;
}

/** Writes with backpressure; false once the client is gone. */
async function write(res: ServerResponse, chunk: string): Promise<boolean> {
  if (res.destroyed || res.writableEnded) return false;
  if (res.write(chunk)) return true;
  await new Promise<void>((resolve) => {
    const done = () => {
      res.off('drain', done);
      res.off('close', done);
      resolve();
    };
    res.on('drain', done);
    res.on('close', done);
  });
  return !res.destroyed;
}

/** Exports running, per store. */
const active = new WeakMap<Store, number>();

export function auditExport(store: Store, query: AuditExportQuery): RawResponse {
  const running = (): number => active.get(store) ?? 0;
  {
    if (running() >= AUDIT_EXPORT_CONCURRENCY) throw new ApiError('rate-limited');
    const { format, ...filter }: { format: 'jsonl' | 'csv' } & AuditFilter = query;
    active.set(store, running() + 1);
    let upTo: number;
    try {
      upTo = store.auditHead()?.seq ?? 0;
      const count = store.countAudit(filter, upTo);
      store.audit({
        type: 'audit.exported',
        summary: { format, count, filter: { ...filter } },
      });
      return new RawResponse(async (res) => {
        try {
          res.writeHead(200, {
            'content-type':
              format === 'csv' ? 'text/csv; charset=utf-8' : 'application/x-ndjson; charset=utf-8',
            'content-disposition': `attachment; filename="audit.${format}"`,
            'cache-control': 'no-store',
          });
          if (format === 'csv' && !(await write(res, `${CSV_COLUMNS.join(',')}\r\n`))) return;
          let after: number | undefined;
          for (;;) {
            const page = store.listAudit(filter, {
              after,
              order: 'asc',
              limit: AUDIT_EXPORT_PAGE,
              upTo,
            });
            const text = page.events
              .map((event) => (format === 'csv' ? csvLine(event) : jsonLine(event)))
              .join('');
            if (text && !(await write(res, text))) return;
            if (page.next === null) break;
            after = page.next;
            // Control work that arrived meanwhile runs before the next page.
            await new Promise<void>((resolve) => setImmediate(resolve));
          }
          res.end();
        } catch (err) {
          log('error', 'api', 'audit export failed', { error: String(err) });
          res.destroy();
        } finally {
          active.set(store, running() - 1);
        }
      });
    } catch (err) {
      active.set(store, running() - 1);
      throw err;
    }
  }
}
