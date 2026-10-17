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
