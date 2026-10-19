import type { SessionContinuation } from '@hoplet/shared';
import { Fragment, type ReactNode } from 'react';
import { Button } from '../../../components/Button.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { Markdown } from '../../../components/Markdown.tsx';
import { clock, formatCountdown, formatTimer } from '../../../lib/format.ts';
import { languageOfPath } from '../../../lib/highlight.ts';
import { t } from '../../../lib/i18n.ts';
import type { Select } from '../composer/mode-menu.tsx';
import type { RuntimeState } from '../model.ts';
import { ElidedPanel } from './blocks.tsx';
import {
  ELIDED_PREVIEW_CHARS,
  type LiveItem,
  type LiveRow,
  type LiveTurn,
  liveItems,
  type OpenPermission,
  PERMISSION_ELIDE_BYTES,
  PERMISSION_OPEN_MS,
  permissionInput,
} from './live-turn.ts';
import { linesOf, OutputWell } from './OutputWell.tsx';
import { PlanCard, PlanResolved, type PlanView } from './plan-card.tsx';
import {
  ActCells,
  EditActivity,
  Meta,
  NoteActivity,
  RowView,
  SteerActivity,
  ToolActivity,
} from './rows.tsx';
import { toolRefOf } from './tools.ts';
import { editDiff, type Turn, toolArgOf } from './turns.ts';

/** A `{name}` template with elements in place of its placeholders. */
function fill(template: string, parts: Record<string, ReactNode>): ReactNode[] {
  return template.split(/(\{\w+\})/).map((piece, i) => {
    const hole = /^\{(\w+)\}$/.exec(piece);
    // biome-ignore lint/suspicious/noArrayIndexKey: pieces have no identity
    return <Fragment key={i}>{hole ? (parts[hole[1] ?? ''] ?? piece) : piece}</Fragment>;
  });
}

function LiveRowView(props: { row: LiveRow; now: number; caret: boolean }) {
  const { row, now, caret } = props;
  switch (row.kind) {
    case 'text':
      return (
        <div className="text" data-testid="cockpit.live-text">
          <Markdown text={row.text} caret={caret} />
        </div>
      );
    case 'thinking':
      return (
        <div className="act think" data-testid="cockpit.live-thinking">
          <ActCells
            icon={Icon.thinking()}
            verb={t('Thought')}
            object=""
            dim
            meta={
              <Meta
                parts={
                  row.tokens > 0
                    ? [t('{n} tokens').replace('{n}', row.tokens.toLocaleString())]
                    : []
                }
              />
            }
          />
        </div>
      );
    case 'tool': {
      const running = row.result === undefined && row.endedAt === undefined;
      const seconds = Math.max(0, Math.floor((now - row.startedAt) / 1000));
      const edit = row.toolKind === 'edit' ? editDiff(row.input) : null;
      const path = toolArgOf(row.input);
      if (edit && path) {
        return (
          <EditActivity
            id={`live:${row.id}`}
            tool={toolRefOf(row)}
            path={path}
            added={edit.added}
            removed={edit.removed}
            rows={edit.rows}
            more={edit.more}
            language={languageOfPath(path)}
            failed={row.result?.failed === true}
            running={running}
            seconds={seconds}
            defaultOpen={false}
            testid="cockpit.live-tool"
          />
        );
      }
      return (
        <ToolActivity
          id={`live:${row.id}`}
          facts={{
            ...toolRefOf(row),
            input: row.input,
            startedAt: row.startedAt,
            ...(row.endedAt !== undefined ? { endedAt: row.endedAt } : {}),
            ...(row.result ? { result: row.result } : {}),
          }}
          arg={path}
          running={running}
          seconds={seconds}
          testid="cockpit.live-tool"
        />
      );
    }
    case 'result':
      return (
        <NoteActivity
          id={`live:${row.id}`}
          family="result"
          label={t('Result')}
          text={row.text}
          bytes={row.bytes}
          failed={row.failed}
          testid="cockpit.live-result"
        />
      );
  }
}

/** A permission request: the tool, its input, a countdown to the automatic denial, and the answers. */
export function PermissionCard(props: {
  request: OpenPermission;
  /** The display name of the session's provider. */
  providerName: string;
  now: number;
  busy: boolean;
  onAnswer: (decision: 'allow' | 'deny') => void;
}) {
  const { request, now, busy } = props;
  const input = permissionInput(request.toolKind, request.input);
  const remaining = Math.min(PERMISSION_OPEN_MS, request.receivedAt + PERMISSION_OPEN_MS - now);
  const countdown = formatCountdown(remaining);
  const big = input.text.length > PERMISSION_ELIDE_BYTES;
  return (
    <fieldset
      className="ask"
      title={t('Request {id}').replace('{id}', request.requestId.slice(0, 4))}
      data-testid="cockpit.permission"
    >
      <legend className="sr-only">
        {t('{provider} asks to use {tool}')
          .replace('{provider}', props.providerName)
          .replace('{tool}', request.tool)}
      </legend>
      <div className="ask-h">
        {Icon.hand()}
        <span className="q">
          {fill(t('{provider} asks to use {tool}'), {
            provider: props.providerName,
            tool: <b>{request.tool}</b>,
          })}
        </span>
      </div>
      {big ? (
        <ElidedPanel
          id={`perm:${request.requestId}`}
          bytes={input.text.length}
          preview={input.text.slice(0, ELIDED_PREVIEW_CHARS)}
          load={() => Promise.resolve(input.text)}
          solo
        />
      ) : (
        <OutputWell
          solo
          lines={
            input.command
              ? input.text
                  .split('\n')
                  .map((text, i) => ({ text, ...(i === 0 ? { kind: 'cmd' as const } : {}) }))
              : linesOf(input.text)
          }
          label={t('Request')}
          data-testid="cockpit.permission-input"
        />
      )}
      <div className="ask-f">
        <Button
          variant="primary"
          disabled={busy}
          onClick={() => props.onAnswer('allow')}
          data-testid="cockpit.permission-allow"
        >
          {t('Allow')}
        </Button>
        <Button
          disabled={busy}
          onClick={() => props.onAnswer('deny')}
          data-testid="cockpit.permission-deny"
        >
          {t('Deny')}
        </Button>
        <span
          className="ask-meta"
          title={t('Denied without an answer in {time}').replace('{time}', countdown)}
        >
          {Icon.clock()}
          {fill(t('Auto-deny in {time}'), { time: <span className="num">{countdown}</span> })}
        </span>
      </div>
    </fieldset>
  );
}

/**
 * The body of a turn: prose is a block of its own, and each run of activity rows between prose is one group,
 * as in the design, so the margins of groups and prose add.
 */
function Segments<T>(props: {
  items: T[];
  isText: (item: T) => boolean;
  keyOf: (item: T) => string | number;
  render: (item: T) => ReactNode;
}) {
  const segments: { text: boolean; items: T[] }[] = [];
  for (const item of props.items) {
    const text = props.isText(item);
    const last = segments[segments.length - 1];
    if (last && !text && !last.text) last.items.push(item);
    else segments.push({ text, items: [item] });
  }
  return segments.map((segment) =>
    segment.text ? (
      segment.items.map((item) => <Fragment key={props.keyOf(item)}>{props.render(item)}</Fragment>)
    ) : (
      <div className="acts" key={props.keyOf(segment.items[0] as T)}>
        {segment.items.map((item) => (
          <Fragment key={props.keyOf(item)}>{props.render(item)}</Fragment>
        ))}
      </div>
    ),
  );
}

/** The live turn: raised, with an accent edge (amber when it waits for permission). */
export function LiveTurnView(props: {
  turn: LiveTurn;
  /** The stored turn the live turn continues, when this window opened on a running turn. */
  base?: Turn | undefined;
  state: RuntimeState;
  now: number;
  /** This window lost the core: every live motion stops and greys. */
  frozen: boolean;
  permissions: OpenPermission[];
  /** The display name of the session's provider. */
  providerName: string;
  answering: boolean;
  notice: string | null;
  canInterrupt: boolean;
  onInterrupt: () => void;
  onAnswer: (
    requestId: string,
    decision: 'allow' | 'deny',
    plan?: { mode?: string; feedback?: string },
  ) => void;
  /** The session's mode option, which names the modes of a plan's approval. */
  modes: Select | undefined;
  /** Deciding is possible: the owner is online and this window is connected. */
  decidable: boolean;
  deviceName: string;
  projectPath: string;
  /** Approves a plan into a new session (ADR 0025); rejects with the reason it did not start. */
  onFresh?: ((requestId: string, mode: string, promptId: string) => Promise<void>) | undefined;
  /** Where this session's plan went on, once the core has recorded it. */
  continuedIn?: SessionContinuation | undefined;
  onOpenSession?: ((sessionId: string) => void) | undefined;
  /** What a development fixture opens the plan card with. */
  planView?: PlanView | undefined;
  /** Messages steered into this turn that the model has not taken in yet. */
  steering?: { text: string; taken?: boolean }[];
}) {
  const { turn, base, state, now, frozen } = props;
  const failed = turn.phase === 'failed';
  const need = state === 'waiting-permission' && turn.phase === 'live';
  const live = turn.phase === 'live';
  const prompt = turn.prompt ?? base?.prompt?.text ?? null;
  const startedAt = base?.ts
    ? Math.min(Date.parse(base.ts) || turn.startedAt, turn.startedAt)
    : turn.startedAt;
  const elapsed = (turn.endedAt ?? now) - turn.startedAt;
  const showBadge = state !== 'starting' && (live || failed);
  const word = failed
    ? t('Failed')
    : frozen
      ? t('Running · paused while reconnecting')
      : need
        ? t('Waiting for permission')
        : t('Running');
  const lastText = [...turn.rows].reverse().find((r) => r.kind === 'text');
  const cls = `turn live${need ? ' need' : ''}${failed ? ' fail' : ''}${frozen ? ' frozen' : ''}`;
  return (
    <section
      className={cls}
      data-testid="cockpit.live-turn"
      data-state={state}
      aria-label={t('Live turn')}
    >
      <header className="th" data-testid="cockpit.live-head">
        <div className={`tp${prompt ? '' : ' none'}`}>{prompt ?? t('Turn in progress')}</div>
        <div className="rt">
          {showBadge ? (
            <span
              className="badge"
              role="img"
              title={word}
              aria-label={word}
              data-testid="cockpit.live-state"
            >
              {failed ? <span className="bad-i">{Icon.alert()}</span> : <span className="sdot" />}
              <span>{formatTimer(elapsed)}</span>
            </span>
          ) : null}
          {live && state !== 'starting' && props.canInterrupt ? (
            <IconButton
              label={t('Interrupt')}
              hint="Esc"
              icon={Icon.stop()}
              bordered
              disabled={frozen}
              onClick={props.onInterrupt}
              data-testid="cockpit.interrupt"
            />
          ) : null}
        </div>
        <div className="meta">
          <span className="num">{clock(startedAt)}</span>
        </div>
      </header>
      <div className="tsep" />
      <div className="tb">
        {base ? (
          <Segments
            items={base.rows}
            isText={(row) => row.kind === 'text'}
            keyOf={(row) => row.key}
            render={(row) => <RowView row={row} />}
          />
        ) : null}
        {state === 'starting' && live ? (
          <div className="quiet-line" data-testid="cockpit.live-starting">
            <Spinner />
            <span>{t('Starting {provider}').replace('{provider}', props.providerName)}</span>
          </div>
        ) : null}
        <Segments<LiveItem>
          items={liveItems(turn)}
          isText={(item) => item.row?.kind === 'text'}
          keyOf={(item) => item.key}
          render={(item) =>
            item.plan ? (
              <PlanResolved
                plan={item.plan}
                modes={props.modes}
                continuedIn={props.continuedIn}
                onOpenSession={props.onOpenSession}
              />
            ) : (
              <LiveRowView
                row={item.row}
                now={now}
                caret={
                  live &&
                  state === 'running' &&
                  item.row === lastText &&
                  item.row === turn.rows[turn.rows.length - 1]
                }
              />
            )
          }
        />
        {live
          ? (props.steering ?? []).map((steer, i) => (
              <SteerActivity
                // biome-ignore lint/suspicious/noArrayIndexKey: steers are an append-only list
                key={i}
                text={steer.text}
                pending={!steer.taken}
                testid={steer.taken ? 'cockpit.steer-taken' : 'cockpit.steer-pending'}
              />
            ))
          : null}
        {failed ? (
          <div role="alert" data-testid="cockpit.live-error">
            <OutputWell solo error lines={linesOf(turn.error ?? t('Failed'))} label={t('Error')} />
          </div>
        ) : null}
        {props.notice ? (
          <div className="quiet-line" role="status" data-testid="cockpit.permission-notice">
            {Icon.info()}
            <span>{props.notice}</span>
          </div>
        ) : null}
        {live
          ? props.permissions.map((request) =>
              request.planExit ? (
                <PlanCard
                  key={request.requestId}
                  request={request}
                  providerName={props.providerName}
                  modes={props.modes}
                  decidable={props.decidable}
                  deviceName={props.deviceName}
                  projectPath={props.projectPath}
                  busy={props.answering}
                  initial={props.planView}
                  onFresh={
                    props.onFresh
                      ? (mode, promptId) =>
                          props.onFresh?.(request.requestId, mode, promptId) ?? Promise.resolve()
                      : undefined
                  }
                  onApprove={(mode) => props.onAnswer(request.requestId, 'allow', { mode })}
                  onKeep={(feedback) => props.onAnswer(request.requestId, 'deny', { feedback })}
                />
              ) : (
                <PermissionCard
                  key={request.requestId}
                  request={request}
                  providerName={props.providerName}
                  now={now}
                  busy={props.answering}
                  onAnswer={(decision) => props.onAnswer(request.requestId, decision)}
                />
              ),
            )
          : null}
      </div>
    </section>
  );
}
