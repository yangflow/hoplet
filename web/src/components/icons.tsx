import {
  Activity,
  Archive,
  ArchiveRestore,
  ArrowRightLeft,
  ArrowRightToLine,
  ArrowUp,
  ArrowUpRight,
  Asterisk,
  Ban,
  Bell,
  BellOff,
  BookOpen,
  Check,
  ChevronRight,
  CircleAlert,
  CircleDashed,
  Clock,
  Contrast,
  Copy,
  CornerDownRight,
  Download,
  Ellipsis,
  Eye,
  File,
  FilePen,
  FilePlus,
  Folder,
  FolderOpen,
  GitBranch,
  Globe,
  GripVertical,
  Hand,
  Image,
  Info,
  Laptop,
  Layers,
  LayoutTemplate,
  Lightbulb,
  ListChecks,
  LoaderCircle,
  Lock,
  Mic,
  Minus,
  Moon,
  PanelLeft,
  PanelRight,
  Paperclip,
  Pause,
  Pencil,
  Pin,
  PinOff,
  Play,
  Plug,
  Plus,
  RotateCw,
  Search,
  Send,
  Settings,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Square,
  SquarePen,
  Terminal,
  TextWrap,
  Trash2,
  Upload,
  Webhook,
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
  activity: draw(Activity),
  close: draw(X),
  check: draw(Check),
  alert: draw(CircleAlert),
  info: draw(Info),
  retry: draw(RotateCw),
  copy: draw(Copy),
  gear: draw(Settings),
  laptop: draw(Laptop),
  bell: draw(Bell),
  bellOff: draw(BellOff),
  moon: draw(Moon),
  paperPlane: draw(Send),
  webhook: draw(Webhook),
  contrast: draw(Contrast),
  ellipsis: draw(Ellipsis),
  wrap: draw(TextWrap),
  shield: draw(Shield),
  shieldAlert: draw(ShieldAlert),
  rules: draw(ShieldCheck),
  ban: draw(Ban),
  any: draw(Asterisk),
  filePen: draw(FilePen),
  listChecks: draw(ListChecks),
  sparkles: draw(Sparkles),
  pin: draw(Pin),
  unpin: draw(PinOff),
  archive: draw(Archive),
  restore: draw(ArchiveRestore),
  grip: draw(GripVertical),
  trash: draw(Trash2),
  waiting: draw(CircleDashed),
  skipped: draw(Minus),
  queue: draw(CornerDownRight),
  steer: draw(ArrowRightToLine),
  play: draw(Play),
  pause: draw(Pause),
  template: draw(LayoutTemplate),
  upload: draw(Upload),
  download: draw(Download),
};

/** The shared icon vocabulary of config values (`CONFIG_ICONS`) mapped to this client's glyphs. */
const CONFIG_GLYPHS: Record<string, () => ReactNode> = {
  shield: () => Icon.shield(),
  'shield-alert': () => Icon.shieldAlert(),
  'file-pen': () => Icon.filePen(),
  'list-checks': () => Icon.listChecks(),
  sparkles: () => Icon.sparkles(),
};

/** The glyph of a config value's declared icon name; null when it declares none or the name is unknown. */
export function configIcon(name: string | undefined) {
  return name ? (CONFIG_GLYPHS[name]?.() ?? null) : null;
}

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
