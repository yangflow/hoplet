import { type Command, CommandListSchema, safeParse } from '@hoplet/shared';
import type { StoreContext } from './layout.ts';

/** Remembers the list a session of `deviceId` offered in `cwd`, replacing the earlier one. */
export function putDirectoryCommands(
  ctx: StoreContext,
  deviceId: string,
  cwd: string,
  commands: Command[],
): void {
  ctx.db
    .prepare(
      `INSERT INTO directory_commands(device_id, cwd, commands, updated_at) VALUES(?, ?, ?, ?)
       ON CONFLICT(device_id, cwd) DO UPDATE SET commands=excluded.commands, updated_at=excluded.updated_at`,
    )
    .run(deviceId, cwd, JSON.stringify(commands), Date.now());
}

/** The list last remembered for `deviceId` and `cwd`, or null when there is none or it cannot be read. */
export function directoryCommands(
  ctx: StoreContext,
  deviceId: string,
  cwd: string,
): Command[] | null {
  const row = ctx.db
    .prepare('SELECT commands FROM directory_commands WHERE device_id = ? AND cwd = ?')
    .get(deviceId, cwd) as { commands: string } | undefined;
  if (!row) return null;
  try {
    const parsed = safeParse(CommandListSchema, JSON.parse(row.commands));
    return parsed.success ? parsed.output : null;
  } catch {
    return null;
  }
}
