// Which requests an action link may not approve (ADR 0021): the page shows them and sends the user to the web UI.

import path from 'node:path';

/** A word `rm` or `sudo`: not part of a longer name, a flag or a file name. */
const DANGEROUS_WORD = /(?<![\w.-])(?:rm|sudo)(?![\w.-])/;
/** `git` and later `push` in one command, whatever options sit between them. */
const GIT_PUSH = /(?<![\w.-])git(?![\w.-])[^;&|\n]*(?<![\w.-])push(?![\w.-])/;

const FILE_TOOLS = new Set(['Write', 'Edit', 'MultiEdit', 'NotebookEdit']);

function inside(root: string, file: string): boolean {
  const flavor = /^[A-Za-z]:[\\/]/.test(root) || root.includes('\\') ? path.win32 : path.posix;
  if (!flavor.isAbsolute(root)) return false;
  const target = flavor.resolve(root, file);
  const rel = flavor.relative(flavor.resolve(root), target);
  return rel === '' || (!rel.startsWith('..') && !flavor.isAbsolute(rel));
}

/**
 * Whether a request can only be approved in the web UI: a Bash command with `rm`, `git push` or `sudo`, a
 * Write or Edit of a file outside the session's workspace, and every MCP tool. A request whose shape is not
 * known counts too: the safe answer to a doubt is the web UI.
 */
export function webUiOnly(tool: string, input: unknown, projectPath: string): boolean {
  if (tool.startsWith('mcp__')) return true;
  const fields =
    typeof input === 'object' && input !== null ? (input as Record<string, unknown>) : {};
  if (tool === 'Bash') {
    const command = fields.command;
    if (typeof command !== 'string') return true;
    return DANGEROUS_WORD.test(command) || GIT_PUSH.test(command);
  }
  if (FILE_TOOLS.has(tool)) {
    const file = fields.file_path ?? fields.notebook_path;
    if (typeof file !== 'string' || file === '') return true;
    return !inside(projectPath, file);
  }
  return false;
}
