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
      d="M16.7 12.7c0-2.2 1.8-3.2 1.9-3.3-1-1.5-2.6-1.7-3.2-1.7-1.4-.1-2.6.8-3.3.8s-1.7-.8-2.8-.7c-1.4 0-2.8.8-3.5 2.1-1.5 2.6-.4 6.4 1.1 8.5.7 1 1.600 2.200 2.700 2.200 1.100 0 1.500-.7 2.800-.7s1.700.7 2.800.7c1.200 0 1.900-1 2.600-2.100.8-1.200 1.200-2.300 1.200-2.400 0 0-2.300-.9-2.300-3.400zM14.600 6.300c.6-.7 1-1.700.9-2.700-.9 0-1.900.6-2.500 1.300-.5.600-1 1.600-.9 2.600 1 .1 1.900-.5 2.500-1.200z"
    />
  ),
  windows: (
    <path
      fill="currentColor"
      stroke="none"
      transform={fit(1.11, 12, 12)}
      d="M3 5.400 10.500 4.400v7H3zM11.500 4.250 21 3v8.400h-9.500zM3 12.600h7.500v6.900L3 18.600zM11.500 12.600H21V21l-9.500-1.300z"
    />
  ),
  linux: (
    <path
      fill="currentColor"
      stroke="none"
      fillRule="evenodd"
      transform={fit(1.135, 12, 11.75)}
      d="M12 2.500c-1.900 0-3 1.500-3 3.600 0 1.200.1 1.900-.5 3C7.600 10.700 6 12.800 6 15.400c0 1.200.4 2 1 2.500-.5.400-1.600 1-1.800 1.800-.2.800.4 1.300 1.200 1.300h2.700c.8 0 1.300-.5 1.900-.9.300.1.700.1 1 .1s.7 0 1-.1c.6.400 1.100.9 1.900.9h2.700c.8 0 1.400-.5 1.200-1.300-.2-.8-1.300-1.400-1.800-1.800.6-.5 1-1.300 1-2.500 0-2.600-1.600-4.700-2.500-6.300-.6-1.100-.5-1.800-.5-3 0-2.100-1.100-3.600-3-3.600zM12 10.200c-1.200 0-2.100 1.500-2.100 3.300S10.800 17 12 17s2.100-1.700 2.100-3.500-.9-3.300-2.100-3.300zM10.500 5.300a.6.9 0 1 0 0 1.800.6.9 0 1 0 0-1.800zM13.500 5.300a.6.9 0 1 0 0 1.800.6.9 0 1 0 0-1.800z"
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
