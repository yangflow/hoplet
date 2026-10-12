import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';

const [dir, point, mode, seedArg, countArg] = process.argv.slice(2);
if (!dir || !point || !mode) throw new Error('missing fixture arguments');
const input = { sessionId: 'recovery', projectDir: 'p', filePath: '/synthetic', deviceId: 'd' };
const line = (i: number) => JSON.stringify({ n: i, text: '原始\r bytes' });
const { Store } = await import('../../src/store/index.ts');
const recoveryOnly = ['undo-truncated', 'undo-removed', 'undo-startup-failed'].includes(point);
const store = recoveryOnly ? undefined : new Store(dir);
const seed = Number(seedArg);
const count = Number(countArg);
if (seed)
  store?.append(
    input,
    Array.from({ length: seed }, (_, i) => line(i)),
  );
function fail(): never {
  if (mode === 'kill') process.kill(process.pid, 'SIGKILL');
  throw new Error('injected interruption');
}
const originalWrite = fs.writeSync;
let writes = 0;
fs.writeSync = ((fd: number, buffer: Buffer, ...args: unknown[]) => {
  writes++;
  if (point === 'partial-append') {
    originalWrite(fd, buffer.subarray(0, 3));
    return fail();
  }
  if (point === 'short-write') {
    const offset = typeof args[0] === 'number' ? args[0] : 0;
    return originalWrite(fd, buffer, offset, Math.min(3, buffer.length - offset));
  }
  if (point === 'zero-write') return 0;
  const result = Reflect.apply(originalWrite, fs, [fd, buffer, ...args]);
  if (point === 'after-append' || (point === 'second-append' && writes === 2)) fail();
  return result;
}) as typeof fs.writeSync;
const originalWriteFile = fs.writeFileSync;
fs.writeFileSync = ((
  file: fs.PathOrFileDescriptor,
  data: string | NodeJS.ArrayBufferView,
  ...args: unknown[]
) => {
  const gzip = String(file).includes('.ndjson.gz');
  if (gzip && point === 'partial-gzip') {
    originalWriteFile(file, Buffer.from(data as Uint8Array).subarray(0, 5));
    fail();
  }
  Reflect.apply(originalWriteFile, fs, [file, data, ...args]);
  if (gzip && point === 'gzip-written') fail();
}) as typeof fs.writeFileSync;
const originalRename = fs.renameSync;
fs.renameSync = ((from, to) => {
  originalRename(from, to);
  if (String(to).endsWith('.ndjson.gz') && point === 'gzip-renamed') fail();
}) as typeof fs.renameSync;
const originalUnlink = fs.unlinkSync;
fs.unlinkSync = ((file) => {
  originalUnlink(file);
  if (String(file).endsWith('.ndjson') && (point === 'plain-unlinked' || point === 'undo-removed'))
    fail();
}) as typeof fs.unlinkSync;
const originalPrepare = DatabaseSync.prototype.prepare;
DatabaseSync.prototype.prepare = function (sql) {
  const stmt = originalPrepare.call(this, sql);
  if (sql.includes('UPDATE sessions SET project_path')) {
    const run = stmt.run.bind(stmt);
    stmt.run = (...args) => {
      if (point === 'before-index' || point === 'undo-failed') fail();
      const result = Reflect.apply(run, stmt, args);
      if (point === 'after-index') fail();
      return result;
    };
  }
  return stmt;
};
const originalExec = DatabaseSync.prototype.exec;
DatabaseSync.prototype.exec = function (sql) {
  originalExec.call(this, sql);
  if (sql === 'COMMIT' && point === 'after-commit') fail();
};
const originalTruncate = fs.ftruncateSync;
fs.ftruncateSync = (fd, length) => {
  if (point === 'undo-failed' || point === 'undo-startup-failed')
    throw new Error('injected undo failure');
  originalTruncate(fd, length);
  if (point === 'undo-truncated') fail();
};
const originalSync = fs.fsyncSync;
fs.fsyncSync = (fd) => {
  originalSync(fd);
  if (point === 'append-synced' && fs.fstatSync(fd).isFile()) fail();
};
syncBuiltinESMExports();
try {
  if (recoveryOnly) {
    new Store(dir);
    process.exit(0);
  }
  if (!store) throw new Error('missing store');
  store.append(
    input,
    Array.from({ length: count }, (_, i) => line(seed + i)),
  );
  await store.idle();
} catch (err) {
  if (point === 'undo-failed' && store) {
    try {
      store.getSession('recovery');
    } catch {
      fs.writeFileSync(path.join(dir, 'closed'), 'yes');
    }
    process.exit(0);
  }
  if (mode !== 'throw' || !String(err).includes('injected interruption')) throw err;
  // Function failures must also permit a retry without restarting.
  if (
    store &&
    (point === 'after-append' || point === 'partial-append' || point === 'before-index')
  ) {
    fs.writeSync = originalWrite;
    DatabaseSync.prototype.prepare = originalPrepare;
    DatabaseSync.prototype.exec = originalExec;
    syncBuiltinESMExports();
    store.append(
      input,
      Array.from({ length: count }, (_, i) => line(seed + i)),
    );
    fs.writeFileSync(path.join(dir, 'retried'), 'yes');
  }
}

if (store)
  fs.writeFileSync(
    path.join(dir, 'visible.json'),
    JSON.stringify({
      meta: store.getSession('recovery'),
      page: await store.readLines('recovery', 0, 2000),
    }),
  );
