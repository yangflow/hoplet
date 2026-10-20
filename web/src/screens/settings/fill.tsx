import type { ReactNode } from 'react';

/** A translated sentence with markup in place of its `{placeholders}`. */
export function fill(template: string, parts: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).map((piece) => {
    const part = parts[piece];
    return part === undefined ? piece : <span key={piece}>{part}</span>;
  });
}
