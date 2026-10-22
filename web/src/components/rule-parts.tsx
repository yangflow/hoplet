import type { PermissionAction } from '@hoplet/shared';
import { type KeyboardEvent, type ReactNode, useState } from 'react';
import { t } from '../lib/i18n.ts';
import { type MatchPart, matchParts, type RuleKind, toolsOf } from '../lib/permission-rules.ts';
import { Icon } from './icons.tsx';
import './rules.css';

/** The words of the three actions. */
export function actionName(action: PermissionAction): string {
  return action === 'allow' ? t('Allow') : action === 'ask' ? t('Ask') : t('Deny');
}

/** The icon of an action. */
export function actionIcon(action: PermissionAction): ReactNode {
  return action === 'allow' ? Icon.check() : action === 'ask' ? Icon.hand() : Icon.ban();
}

/** The action of a rule as a badge. */
export function ActionBadge(props: { action: PermissionAction }) {
  const { action } = props;
  return (
    <span className={`qa ${action === 'ask' ? 'qask' : action}`} data-action={action}>
      {actionIcon(action)}
      {actionName(action)}
    </span>
  );
}

export function kindName(kind: RuleKind): string {
  switch (kind) {
    case 'run':
      return t('Run');
    case 'read':
      return t('Read');
    case 'write':
      return t('Write');
    case 'fetch':
      return t('Fetch');
    case 'mcp':
      return t('MCP');
    case 'any':
      return t('Any');
  }
}

/** The tool of a kind in a sentence of its own: the tooltip of a row. */
export function kindTitle(kind: RuleKind): string {
  switch (kind) {
    case 'run':
      return t('Run a command');
    case 'read':
      return t('Read files');
    case 'write':
      return t('Write files');
    case 'fetch':
      return t('Fetch from the web');
    case 'mcp':
      return t('MCP tool');
    case 'any':
      return t('Any tool');
  }
}

export function kindIcon(kind: RuleKind): ReactNode {
  switch (kind) {
    case 'run':
      return Icon.terminal();
    case 'read':
      return Icon.file();
    case 'write':
      return Icon.write();
    case 'fetch':
      return Icon.globe();
    case 'mcp':
      return Icon.plug();
    case 'any':
      return Icon.any();
  }
}

/** The icons of the tools a rule names, with their names when there is more than one. */
export function KindMark(props: { tool: string }) {
  const kinds = toolsOf(props.tool);
  return (
    <span className="qk" title={kinds.map(kindTitle).join(', ')}>
      {kinds.map((k) => (
        <span key={k} style={{ display: 'inline-flex' }}>
          {kindIcon(k)}
        </span>
      ))}
      <span>{kinds.map(kindName).join('/')}</span>
    </span>
  );
}

function PartView(props: { part: MatchPart }) {
  const { part } = props;
  if (part.type === 'word') {
    return <span className="qw">{t(part.text)}</span>;
  }
  if (part.type === 'chip')
    return <span className={`qc${part.except ? ' x' : ''}`}>{part.text}</span>;
  return <span className={part.mono ? 'mono' : undefined}>{part.text}</span>;
}

/** What a rule matches: the command and its chips, the paths, the domains, the MCP tool. */
export function MatchView(props: {
  rule: { tool: string; match: Parameters<typeof matchParts>[0]['match'] };
}) {
  const parts = matchParts(props.rule);
  return (
    <>
      {parts.map((p, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list of parts, in order
        <PartView key={i} part={p} />
      ))}
    </>
  );
}

/**
 * Words typed into a field and kept as chips: Enter or a comma adds one, Backspace on an empty field takes the
 * last one back.
 */
export function ChipInput(props: {
  label: string;
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
  bad?: boolean;
  'data-testid'?: string;
}) {
  const { value, onChange } = props;
  const [text, setText] = useState('');
  const add = (raw: string) => {
    const words = raw
      .split(',')
      .map((w) => w.trim())
      .filter((w) => w !== '' && !value.includes(w));
    if (words.length > 0) onChange([...value, ...words]);
    setText('');
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === 'Enter' || e.key === ',') {
      if (text.trim() === '' && e.key === 'Enter') return;
      e.preventDefault();
      add(text);
    } else if (e.key === 'Backspace' && text === '' && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };
  return (
    <div className={`qchipin${props.bad ? ' bad' : ''}`} data-testid={props['data-testid']}>
      {value.map((chip) => (
        <span key={chip} className="qc">
          {chip}
          <button
            type="button"
            title={t('Remove')}
            aria-label={`${t('Remove')} ${chip}`}
            onClick={() => onChange(value.filter((c) => c !== chip))}
          >
            {Icon.close()}
          </button>
        </span>
      ))}
      <input
        aria-label={props.label}
        aria-invalid={props.bad ? true : undefined}
        value={text}
        placeholder={value.length === 0 ? props.placeholder : undefined}
        spellCheck={false}
        autoCapitalize="off"
        autoComplete="off"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKeyDown}
        onBlur={() => add(text)}
      />
    </div>
  );
}
