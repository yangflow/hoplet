import type { InventoryRepository } from '@hoplet/shared';

/** The origin as a client may see it: `user:secret@` and a bare `token@` removed from URL forms. */
export function publicOrigin(origin: string): string {
  return origin.replace(/^([a-z][a-z0-9+.-]*:\/\/)[^@/\s]*@/i, '$1');
}

/** The repository's name: the last path segment of the origin without `.git`; `repo` when it has none. */
export function repoNameOf(origin: string): string {
  const path = publicOrigin(origin)
    .replace(/[?#].*$/, '')
    .replace(/\/+$/, '');
  const last = path.split(/[/:]/).pop() ?? '';
  const name = last.replace(/\.git$/i, '');
  return name === '' || name === '.' || name === '..' ? 'repo' : name;
}

// The project a session works in, across machines (ADR 0041): the normalized `origin` remote of the repository
// that holds its project path.

/**
 * The key of a remote URL: host and path, with the scheme, the user, the port and a trailing `.git` or slash
 * removed and the host lowercased, so `git@github.com:a/b.git` and `https://GitHub.com/a/b` are both
 * `github.com/a/b`. A local path keeps its path. Null for an empty URL.
 */
export function repositoryKey(origin: string): string | null {
  const url = origin.trim();
  if (!url) return null;
  const tidy = (host: string, path: string): string | null => {
    const rest = path
      .replace(/^\/+/, '')
      .replace(/\/+$/, '')
      .replace(/\.git$/i, '')
      .replace(/\/+$/, '');
    if (!host) return rest || null;
    return rest ? `${host.toLowerCase()}/${rest}` : host.toLowerCase();
  };
  if (url.includes('://')) {
    try {
      const parsed = new URL(url);
      if (parsed.protocol === 'file:') return tidy('', parsed.pathname);
      return tidy(parsed.hostname, decodeURIComponent(parsed.pathname));
    } catch {
      return null;
    }
  }
  // The scp form, `[user@]host:path`; a Windows drive (`C:\…`) is a path, not a host.
  const scp = /^(?:[^@/]+@)?([^:/\\]+):(?!\\)(.*)$/.exec(url);
  if (scp?.[1] && scp[1].length > 1) return tidy(scp[1], scp[2] ?? '');
  return tidy('', url);
}

/** Whether `path` is `root` or inside it. */
function within(path: string, root: string): boolean {
  const base = root.replace(/[/\\]+$/, '');
  return path === base || path.startsWith(`${base}/`) || path.startsWith(`${base}\\`);
}

/**
 * The repository key of a project path on a machine: the remote of the deepest watched project that is the path
 * or holds it, else of the one that holds `clone` (the clone a Hoplet worktree was made from). Null when none has
 * a remote.
 */
export function repositoryOf(
  repositories: readonly InventoryRepository[],
  projectPath: string,
  clone?: string,
): string | null {
  const find = (path: string): string | null => {
    let best: InventoryRepository | undefined;
    for (const repo of repositories) {
      if (!repo.origin || !within(path, repo.path)) continue;
      if (!best || repo.path.length > best.path.length) best = repo;
    }
    return best?.origin ? repositoryKey(best.origin) : null;
  };
  return find(projectPath) ?? (clone ? find(clone) : null);
}
