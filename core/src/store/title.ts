const TITLE_MAX = 96;

/** Turn a raw prompt into a one-line title: reject commands and paste tags, cut at a sentence when possible. */
export function summarizeTitle(text: string): string | null {
  const oneLine = text.replace(/\s+/g, ' ').trim();
  if (!oneLine) return null;
  if (oneLine.startsWith('/') || oneLine.startsWith('<')) return null; // command or pasted-content tag
  if (oneLine.length <= TITLE_MAX) return oneLine;
  const window = oneLine.slice(0, TITLE_MAX);
  const cut = Math.max(
    window.lastIndexOf('。'),
    window.lastIndexOf('.'),
    window.lastIndexOf('!'),
    window.lastIndexOf('?'),
    window.lastIndexOf('！'),
    window.lastIndexOf('？'),
  );
  if (cut >= Math.floor(TITLE_MAX / 3)) return window.slice(0, cut + 1);
  return `${window.trimEnd()}…`;
}

/**
 * What the index reads from appended lines, without changing them: the project path (the first `cwd`), the
 * title, and the git branch (the last `gitBranch`, so a checkout during the session shows).
 */
export function extractMeta(lines: string[]): {
  projectPath: string;
  title: string;
  aiTitle: string;
  gitBranch: string;
} {
  let projectPath = '';
  let gitBranch = '';
  let title = '';
  let fallback = '';
  let aiTitle = '';
  for (const raw of lines) {
    let o: Record<string, unknown>;
    try {
      o = JSON.parse(raw) as Record<string, unknown>;
    } catch {
      continue;
    }
    if (!projectPath && typeof o.cwd === 'string' && o.cwd) projectPath = o.cwd;
    if (typeof o.gitBranch === 'string' && o.gitBranch) gitBranch = o.gitBranch;
    if (o.type === 'ai-title' && typeof o.aiTitle === 'string') {
      const t = o.aiTitle.replace(/\s+/g, ' ').trim();
      if (t) {
        aiTitle = t.length <= TITLE_MAX ? t : `${t.slice(0, TITLE_MAX).trimEnd()}…`;
      }
      continue;
    }
    if (o.type === 'user' && o.isMeta !== true && o.isSidechain !== true) {
      const content = (o.message as Record<string, unknown> | undefined)?.content;
      let text = '';
      if (typeof content === 'string') text = content;
      else if (Array.isArray(content)) {
        const t = content.find((b) => (b as { type?: string })?.type === 'text') as
          | { text?: string }
          | undefined;
        text = t?.text ?? '';
      }
      const candidate = summarizeTitle(text);
      // Earliest substantive prompt; first non-command short one as fallback (no ai-title yet).
      if (candidate && !title) {
        if (candidate.length >= 8) title = candidate;
        else if (!fallback) fallback = candidate;
      }
    }
  }
  return { projectPath, title: title || fallback, aiTitle, gitBranch };
}
