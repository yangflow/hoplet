import type { SystemKind } from '../lib/system.ts';

// The mark of a machine's system with its presence as a dot at the bottom-right (docs/design.md#icons).
// Each solid mark is scaled about its centre by `k` so it fills the 20 px box the folder glyph fills.
// The dot is cut into the mark with a mask, so the cut shows whatever is behind the row.

const fit = (k: number, cx: number, cy: number) =>
  `translate(12 12) scale(${k}) translate(${-cx} ${-cy})`;

const MARKS: Record<SystemKind, React.ReactNode> = {
  macos: (
    <path
      fill="currentColor"
      stroke="none"
      transform={fit(1.2, 12.05, 12.1)}
      d="M16.7 12.7c0-2.2 1.8-3.2 1.9-3.3-1-1.5-2.6-1.7-3.2-1.7-1.4-.1-2.6.8-3.3.8s-1.7-.8-2.8-.7c-1.4 0-2.8.8-3.5 2.1-1.5 2.6-.4 6.4 1.1 8.5.7 1 1.6 2.2 2.7 2.2 1.1 0 1.5-.7 2.8-.7s1.7.7 2.8.7c1.2 0 1.9-1 2.6-2.1.8-1.2 1.2-2.3 1.2-2.4 0 0-2.3-.9-2.3-3.4zM14.6 6.3c.6-.7 1-1.7.9-2.7-.9 0-1.9.6-2.5 1.3-.5.6-1 1.6-.9 2.6 1 .1 1.9-.5 2.5-1.2z"
    />
  ),
  windows: (
    <path
      fill="currentColor"
      stroke="none"
      transform={fit(1.11, 12, 12)}
      d="M3 5.4 10.5 4.4v7H3zM11.5 4.25 21 3v8.4h-9.5zM3 12.6h7.5v6.9L3 18.6zM11.5 12.6H21V21l-9.5-1.3z"
    />
  ),
  linux: (
    <path
      fill="currentColor"
      stroke="none"
      fillRule="evenodd"
      transform={fit(1.135, 12, 11.75)}
      d="M12 2.5c-1.9 0-3 1.5-3 3.6 0 1.2.1 1.9-.5 3C7.6 10.7 6 12.8 6 15.4c0 1.2.4 2 1 2.5-.5.4-1.6 1-1.8 1.8-.2.8.4 1.3 1.2 1.3h2.7c.8 0 1.3-.5 1.9-.9.3.1.7.1 1 .1s.7 0 1-.1c.6.4 1.1.9 1.9.9h2.7c.8 0 1.4-.5 1.2-1.3-.2-.8-1.3-1.4-1.8-1.8.6-.5 1-1.3 1-2.5 0-2.6-1.6-4.7-2.5-6.3-.6-1.1-.5-1.8-.5-3 0-2.1-1.1-3.6-3-3.6zM12 10.2c-1.2 0-2.1 1.5-2.1 3.3S10.8 17 12 17s2.1-1.7 2.1-3.5-.9-3.3-2.1-3.3zM10.5 5.3a.6.9 0 1 0 0 1.8.6.9 0 1 0 0-1.8zM13.5 5.3a.6.9 0 1 0 0 1.8.6.9 0 1 0 0-1.8z"
    />
  ),
  other: (
    <>
      <rect width="20" height="14" x="2" y="3" rx="2" />
      <path d="M8 21h8" />
      <path d="M12 17v4" />
    </>
  ),
};

/** A machine's system mark with its presence dot; decorative, the row names the machine. */
export function MachineIcon({ kind, online }: { kind: SystemKind; online: boolean }) {
  return (
    <span className={`mch${online ? '' : ' off'}`} aria-hidden="true">
      <svg
        width="16"
        height="16"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
      >
        {MARKS[kind]}
      </svg>
      <i className="mch-d" />
    </span>
  );
}
