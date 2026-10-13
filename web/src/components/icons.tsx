import {
  ArrowRightLeft,
  ArrowUp,
  ArrowUpRight,
  BookOpen,
  Check,
  ChevronRight,
  CircleAlert,
  Clock,
  Contrast,
  Copy,
  Ellipsis,
  Eye,
  File,
  FilePlus,
  Folder,
  FolderOpen,
  GitBranch,
  Globe,
  Hand,
  Image,
  Info,
  Laptop,
  Layers,
  Lightbulb,
  LoaderCircle,
  Lock,
  Mic,
  PanelLeft,
  PanelRight,
  Paperclip,
  Pencil,
  Plug,
  Plus,
  RotateCw,
  Search,
  Settings,
  Square,
  SquarePen,
  Terminal,
  TextWrap,
  X,
  Zap,
} from 'lucide-react';
import type { ReactNode } from 'react';

// The only module that imports the icon family (docs/design.md#icons): Lucide, bundled and never fetched.
// Every glyph is drawn at --icon-size with --icon-stroke by the `.icon` rule (components.css), round caps and
// joins; a chevron and an arrow are turned by a class instead of adding glyphs. Decorative unless labelled.

type Turn = '' | ' icon-r90' | ' icon-r180';

const draw =
  (Glyph: typeof Search, turn: Turn = '', extra = '') =>
  (): ReactNode => <Glyph className={`icon${turn}${extra}`} />;

export const Icon = {
  search: draw(Search),
  sidebar: draw(PanelLeft),
  panel: draw(PanelRight),
  compose: draw(SquarePen),
  plus: draw(Plus),
  chevron: draw(ChevronRight),
  down: draw(ChevronRight, ' icon-r90'),
  back: draw(ChevronRight, ' icon-r180'),
  send: draw(ArrowUp),
  arrowUp: draw(ArrowUp),
  arrowDown: draw(ArrowUp, ' icon-r180'),
  external: draw(ArrowUpRight),
  handoff: draw(ArrowRightLeft),
  file: draw(File),
  folder: draw(Folder),
  folderOpen: draw(FolderOpen),
  read: draw(BookOpen),
  pen: draw(Pencil),
  write: draw(FilePlus),
  terminal: draw(Terminal),
  plug: draw(Plug),
  globe: draw(Globe),
  thinking: draw(Lightbulb),
  fast: draw(Zap),
  branch: draw(GitBranch),
  image: draw(Image),
  tools: draw(Layers),
  eye: draw(Eye),
  stop: draw(Square),
  stopSolid: draw(Square, '', ' icon-fill'),
  lock: draw(Lock),
  clip: draw(Paperclip),
  mic: draw(Mic),
  hand: draw(Hand),
  clock: draw(Clock),
  close: draw(X),
  check: draw(Check),
  alert: draw(CircleAlert),
  info: draw(Info),
  retry: draw(RotateCw),
  copy: draw(Copy),
  gear: draw(Settings),
  laptop: draw(Laptop),
  contrast: draw(Contrast),
  ellipsis: draw(Ellipsis),
  wrap: draw(TextWrap),
};

export type IconName = keyof typeof Icon;

/** The spinner of a starting, running or loading thing: the loader glyph, turning (never in reduced motion). */
export function Spinner() {
  return <LoaderCircle className="icon spin" />;
}

/** The Hoplet h: a lowercase h whose arch drops onto a dot, the app icon's form, drawn in the colour it sits in. */
export function HopletMark(props: { size?: number; width?: number }) {
  const { size = 14, width = 3 } = props;
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={width}
      strokeLinecap="round"
      aria-hidden="true"
    >
      <path d="M8.75 4.5V17M8.75 12.5c0-1.9 1.4-2.9 3.25-2.9S15.25 10.6 15.25 12.5V14" />
      <circle cx="15.25" cy="19" r="1.9" fill="currentColor" stroke="none" />
    </svg>
  );
}
