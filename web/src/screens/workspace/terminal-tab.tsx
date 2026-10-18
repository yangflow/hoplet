import { type ReactNode, useEffect } from 'react';
import { Button } from '../../components/Button.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';
import { closedKindOf, type Gate } from '../../lib/terminal.ts';
import { CodeText } from './code-text.tsx';
import { type TerminalPhase, useTerminal } from './use-terminal.ts';
import { XtermView } from './xterm-view.tsx';

/** Where the documentation of the machine's terminal switch lives. */
const HOSTING_URL = 'https://github.com/yangflow/hoplet/blob/main/docs/hosting.md#terminal';

const named = (text: string, owner: string) => t(text).replace(/\{name\}/g, owner);

/** A centred message with one action, the grammar of every state of this tab (design/screens/inspector-tools.html). */
function Message(props: {
  icon: ReactNode;
  title: string;
  text: ReactNode;
  children?: ReactNode;
  testid: string;
  status?: boolean;
}) {
  return (
    <div className="ws-es" {...(props.status ? { role: 'status' } : {})} data-testid={props.testid}>
      <span className="ws-tile">{props.icon}</span>
      <b className="ws-es-t">{props.title}</b>
      <p>{props.text}</p>
      {props.children}
    </div>
  );
}

/** What each blocked gate says, and why the disabled button is disabled. */
const BLOCKED: Record<
  Exclude<Gate, 'ready' | 'off'>,
  { icon: () => ReactNode; title: string; text: string; code: string; why: string }
> = {
  'turn-running': {
    icon: () => <Spinner />,
    title: 'A turn is running',
    text: 'A turn is running or waiting for a permission. You can open a terminal when it ends.',
    code: 'turn-running',
    why: 'Available when the turn ends',
  },
  external: {
    icon: () => Icon.eye(),
    title: 'This session is external',
    text: 'Claude Code is running it outside Hoplet and Hoplet only reads it. A terminal needs a session Hoplet can drive.',
    code: 'external',
    why: 'Not available for external sessions',
  },
  'handoff-running': {
    icon: () => Icon.handoff(),
    title: 'A handoff is running',
    text: 'The terminal opens when the handoff has finished.',
    code: 'handoff-running',
    why: 'Available when the handoff ends',
  },
  'owner-offline': {
    icon: () => Icon.laptop(),
    title: '{name} is offline',
    text: 'A terminal needs its machine online. It opens when the machine is back.',
    code: 'owner-offline',
    why: 'Available when {name} is back',
  },
};

function OpenButton(props: {
  disabled: boolean;
  title?: string;
  primary?: boolean;
  onClick?: () => void;
  testid: string;
}) {
  return (
    <Button
      variant={props.primary ? 'primary' : 'default'}
      icon={Icon.terminal()}
      disabled={props.disabled}
      {...(props.title ? { title: props.title } : {})}
      onClick={props.onClick}
      data-testid={props.testid}
    >
      {t('Open terminal')}
    </Button>
  );
}

/** The transcript a development state shows in place of the live emulator, so every capture is the same. */
const TRANSCRIPT = (
  <>
    <span className="pr">mark@mbp hoplet %</span> pnpm test{'\n'}
    <span className="d">&gt; hoplet@0.5.0 test</span>
    {'\n'}
    <span className="d">&gt; vitest run</span>
    {'\n\n'} <span className="ok">✓</span> shared/schema.test.ts <span className="d">(24)</span>
    {'\n '}
    <span className="ok">✓</span> core/store.test.ts <span className="d">(61)</span>
    {'\n '}
    <span className="ok">✓</span> core/ingest.test.ts <span className="d">(38)</span>
    {'\n\n'} Test Files 3 passed <span className="d">(3)</span>
    {'\n'} Tests 123 passed <span className="d">(123)</span>
    {'\n\n'}
    <span className="pr">mark@mbp hoplet %</span> <span className="caret" />
  </>
);

/**
 * The inspector's Terminal tab (ADR 0026): a shell on the session's machine. The tab only follows what the core
 * and the owner say; `gate` is whether one may open now.
 */
export function TerminalTab(props: {
  sessionId: string;
  gate: Gate;
  owner: string;
  dir: string;
  /** Tells whether a terminal is open, for the live dot on the tab. */
  onLive?: (live: boolean) => void;
  /** A development state: nothing is fetched and the emulator is replaced by a fixed transcript. */
  fixture?: TerminalPhase;
}) {
  const { gate, owner, dir } = props;
  const term = useTerminal(props.sessionId, gate, props.fixture);
  const { phase } = term;
  const live = phase.kind === 'open';
  const { onLive } = props;
  useEffect(() => {
    onLive?.(live);
    return () => onLive?.(false);
  }, [live, onLive]);

  if (phase.kind === 'open') {
    return (
      <div className="ws-tool" data-testid="workspace.terminal">
        <div className="ws-rz" aria-hidden="true" title={t('Drag to resize')}>
          <i />
        </div>
        <div className="ws-tbar">
          {Icon.terminal()}
          <span className="ws-path" title={dir}>
            {dir}
          </span>
          <span className="ws-grow" />
          <span className="ws-meta" title={t('Columns × rows')}>
            {phase.cols} × {phase.rows}
          </span>
          <Button
            variant="quiet"
            className="sm"
            icon={Icon.close()}
            title={t('Close terminal')}
            onClick={term.close}
            data-testid="workspace.terminal-close"
          >
            {t('Close')}
          </Button>
        </div>
        <div className="ws-pad">
          {props.fixture ? (
            <div
              className="ws-tm"
              role="log"
              aria-label={t('Terminal')}
              data-testid="workspace.terminal-view"
            >
              {TRANSCRIPT}
            </div>
          ) : (
            <XtermView setSink={term.setSink} onKeys={term.sendKeys} onSize={term.sendSize} />
          )}
        </div>
        <div className="ws-tfoot">
          {Icon.info()}
          <span>
            {t(
              'Nothing typed or printed is stored · closes after 60 minutes without input, or when a turn starts',
            )}
          </span>
        </div>
      </div>
    );
  }

  if (phase.kind === 'checking')
    return (
      <Message
        icon={<Spinner />}
        title={t('Looking for a terminal')}
        text={named('Asking {name} whether this session has one open.', owner)}
        status
        testid="workspace.terminal-checking"
      />
    );

  if (phase.kind === 'closed') {
    const kind = closedKindOf(phase.reason);
    const copy = {
      turn: {
        title: 'A turn started, so the terminal closed',
        text: 'A prompt is not delayed by the terminal. Output is not kept.',
        code: 'turn-started',
      },
      idle: {
        title: 'Closed after 60 minutes without input',
        text: 'Output alone does not keep a terminal open. Output is not kept.',
        code: 'idle',
      },
      offline: {
        title: '{name} went offline, so the terminal closed',
        text: 'A terminal never outlives its machine’s connection. Output is not kept.',
        code: 'owner-offline',
      },
      generic: { title: 'The terminal closed', text: 'Output is not kept.', code: phase.reason },
    }[kind];
    const why =
      gate === 'ready'
        ? t('Open a new shell in the session directory')
        : gate === 'off'
          ? named('Terminal is off on {name}', owner)
          : named(BLOCKED[gate].why, owner);
    return (
      <Message
        icon={Icon.clock()}
        title={named(copy.title, owner)}
        text={t(copy.text)}
        testid={`workspace.terminal-closed-${kind}`}
      >
        <div className="ws-acts">
          <Button
            variant={gate === 'ready' ? 'primary' : 'default'}
            disabled={gate !== 'ready' || term.opening}
            title={why}
            onClick={term.open}
            data-testid="workspace.terminal-again"
          >
            {t('Open again')}
          </Button>
          <span className="ws-meta">
            {phase.exitCode !== undefined && kind === 'generic'
              ? phase.reason === 'exit'
                ? `exit ${phase.exitCode}`
                : `${copy.code} · exit ${phase.exitCode}`
              : copy.code}
          </span>
        </div>
      </Message>
    );
  }

  if (phase.kind === 'busy')
    return (
      <Message
        icon={Icon.terminal()}
        title={named('Three terminals are open on {name}', owner)}
        text={t('That is the limit. Close one in its session to open another here.')}
        testid="workspace.terminal-busy"
      >
        <div className="ws-acts">
          <OpenButton
            disabled
            title={t('Three terminals are open on this machine')}
            testid="workspace.terminal-open"
          />
          <span className="ws-meta">terminal-busy</span>
        </div>
      </Message>
    );

  if (gate === 'off' || phase.kind === 'off')
    return (
      <Message
        icon={Icon.terminal()}
        title={named('Terminal is off on {name}', owner)}
        text={
          <CodeText
            text={named(
              'Set `"terminal": true` in the agent’s `config.json` on {name} and restart the agent. Only the machine’s operator can turn it on; Hoplet cannot.',
              owner,
            )}
          />
        }
        testid="workspace.terminal-off"
      >
        <a
          className="ws-lnk"
          href={HOSTING_URL}
          target="_blank"
          rel="noopener noreferrer"
          title={t('Hosting: Terminal (opens the documentation)')}
        >
          {t('Hosting: Terminal')}
          {Icon.external()}
        </a>
      </Message>
    );

  if (gate !== 'ready') {
    const blocked = BLOCKED[gate];
    return (
      <Message
        icon={blocked.icon()}
        title={named(blocked.title, owner)}
        text={t(blocked.text)}
        testid={`workspace.terminal-blocked-${gate}`}
      >
        <div className="ws-acts">
          <OpenButton disabled title={named(blocked.why, owner)} testid="workspace.terminal-open" />
          <span className="ws-meta">{blocked.code}</span>
        </div>
      </Message>
    );
  }

  return (
    <Message
      icon={Icon.terminal()}
      title={t('Open a terminal in {dir}').replace('{dir}', dir)}
      text={named(
        'A shell on {name}, in this session’s directory. Nothing typed or printed is stored.',
        owner,
      )}
      testid="workspace.terminal-ready"
    >
      {phase.kind === 'failed' ? (
        <p className="ws-err" role="alert" data-testid="workspace.terminal-error">
          {phase.text}
        </p>
      ) : null}
      <div className="ws-acts">
        <OpenButton
          primary
          disabled={term.opening}
          onClick={term.open}
          testid="workspace.terminal-open"
        />
        <span className="ws-meta">{t('Up to 3 open per machine')}</span>
      </div>
    </Message>
  );
}
