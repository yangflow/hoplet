import type { ReactNode } from 'react';

// File-type badges (docs/design.md#icons): the Lucide page with a type mark, the same grid and stroke as the
// icon set, tinted per type with a soft fill. Drawn here rather than imported, so the icon module stays the
// only importer of the family.

export type FileKind = 'ts' | 'md' | 'json' | 'image' | 'text' | 'file' | 'folder';

const PAGE = (
  <>
    <path
      d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"
      fill="currentColor"
      fillOpacity=".14"
    />
    <path d="M14 2v4a2 2 0 0 0 2 2h4" />
  </>
);

const LINES = (
  <>
    <path d="M16 13H8" />
    <path d="M16 17H8" />
    <path d="M10 9H8" />
  </>
);

const art: Record<FileKind, { color: string; mark: ReactNode }> = {
  ts: {
    color: '--color-syntax-type',
    mark: (
      <>
        <path d="M10 12.5 8 15l2 2.5" />
        <path d="m14 12.5 2 2.5-2 2.5" />
      </>
    ),
  },
  md: { color: '--color-syntax-keyword', mark: LINES },
  json: {
    color: '--color-syntax-number',
    mark: (
      <>
        <path d="M10 12a1 1 0 0 0-1 1v1a1 1 0 0 1-1 1 1 1 0 0 1 1 1v1a1 1 0 0 0 1 1" />
        <path d="M14 18a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1 1 1 0 0 1-1-1v-1a1 1 0 0 0-1-1" />
      </>
    ),
  },
  image: {
    color: '--color-syntax-string',
    mark: (
      <>
        <circle cx="10" cy="12" r="2" />
        <path d="m20 17-1.296-1.296a2.41 2.41 0 0 0-3.408 0L9 22" />
      </>
    ),
  },
  text: { color: '--color-text-3', mark: LINES },
  file: { color: '--color-text-3', mark: null },
  folder: { color: '--color-text-3', mark: null },
};

const EXTENSIONS: Record<string, FileKind> = {
  ts: 'ts',
  tsx: 'ts',
  go: 'ts',
  md: 'md',
  json: 'json',
  ndjson: 'json',
  png: 'image',
  jpg: 'image',
  jpeg: 'image',
  gif: 'image',
  webp: 'image',
  svg: 'image',
  txt: 'text',
};

/** The badge kind of a file name. */
export function fileKind(name: string): FileKind {
  const dot = name.lastIndexOf('.');
  return (dot < 0 ? undefined : EXTENSIONS[name.slice(dot + 1).toLowerCase()]) ?? 'file';
}

/** The badge of a file or folder, drawn at --icon-size in its tint. */
export function FileBadge({ kind }: { kind: FileKind }) {
  const { color, mark } = art[kind];
  return (
    <span className="ft" style={{ color: `var(${color})` }} aria-hidden="true">
      <svg
        className="icon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {kind === 'folder' ? (
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
        ) : (
          <>
            {PAGE}
            {mark}
          </>
        )}
      </svg>
    </span>
  );
}
