import type { ReactNode } from 'react';

/** A translated sentence with markup in place of its `{placeholders}`; a part is keyed by its name, with or without the braces. */
export function fill(template: string, parts: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).map((piece) => {
    const part = parts[piece] ?? parts[piece.slice(1, -1)];
    return part === undefined ? piece : <span key={piece}>{part}</span>;
  });
}
