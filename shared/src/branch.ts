/** The longest branch name a new session's worktree accepts. */
export const MAX_BRANCH_NAME_LENGTH = 200;

/**
 * Why `name` cannot be the branch of a new session's worktree, or null when it can. The rules are those of
 * `git check-ref-format --branch`, and a leading `-` is refused so the name can never be read as an option. The
 * agent applies the same rules again before it runs git (agent/internal/workspace/newworktree.go).
 */
export function branchNameProblem(name: string): string | null {
  if (name.length === 0) return 'empty';
  if (name.length > MAX_BRANCH_NAME_LENGTH) return 'too-long';
  if (name.startsWith('-')) return 'leading-dash';
  if (name === '@' || name === 'HEAD') return 'reserved';
  // biome-ignore lint/suspicious/noControlCharactersInRegex: control characters are what this refuses.
  if (/[\u0000- \u007f~^:?*[\\]/.test(name)) return 'forbidden-character';
  if (name.includes('..') || name.includes('@{')) return 'forbidden-sequence';
  if (name.startsWith('/') || name.endsWith('/') || name.includes('//')) return 'slash';
  if (name.endsWith('.')) return 'trailing-dot';
  for (const part of name.split('/')) {
    if (part.startsWith('.') || part.endsWith('.lock')) return 'component';
  }
  return null;
}

/**
 * The `reason` of an `invalid-request` for a new session's worktree. The agent refuses `runtime.start` with the same
 * text (agent/internal/workspace/newworktree.go); the core passes it on as it is.
 */
export const WORKTREE_REASONS = [
  /** The project is not inside a git repository. */
  'worktree-not-a-git-repository',
  /** The repository has no commit to branch from. */
  'worktree-no-commit',
  /** The branch name breaks the rules of `branchNameProblem`. */
  'worktree-branch-invalid',
  /** The repository has the branch already. */
  'worktree-branch-exists',
  /** The directory the worktree would take exists already. */
  'worktree-path-exists',
] as const;
export type WorktreeReason = (typeof WORKTREE_REASONS)[number];

/** Whether `reason` is one of the stable worktree reasons. */
export function isWorktreeReason(reason: string | undefined): reason is WorktreeReason {
  return (WORKTREE_REASONS as readonly string[]).includes(reason ?? '');
}
