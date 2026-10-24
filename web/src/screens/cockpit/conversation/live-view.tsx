import type { SessionContinuation, SessionHandoffRequest } from '@hoplet/shared';
import { Fragment, type ReactNode, useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { Markdown } from '../../../components/Markdown.tsx';
import { clock, formatBytes, formatCountdown, formatTimer } from '../../../lib/format.ts';
import { languageOfPath } from '../../../lib/highlight.ts';
import { t } from '../../../lib/i18n.ts';
import { type Draft, type DraftOption, draftFromCall } from '../../../lib/permission-rules.ts';
import { promptSource } from '../../../lib/provenance.ts';
import type { Select } from '../composer/mode-menu.tsx';
import type { RuntimeState } from '../model.ts';
import { catchUpPercent } from '../runtime.ts';
import { ElidedPanel } from './blocks.tsx';
import {
  ConfigChangeResults,
  type ConfigChangeView,
  PendingConfigChange,
} from './config-change-card.tsx';
import { HandoffCard, HandoffResult } from './handoff-card.tsx';
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
  PromptSourceLine,
  RowView,
  SteerActivity,
  ToolActivity,
} from './rows.tsx';
import { type DraftScope, RuleDraftDialog } from './rule-draft.tsx';
import { RuleLine } from './rule-line.tsx';
import { toolRefOf } from './tools.ts';
import { editDiff, type Turn, toolArgOf } from './turns.ts';

/** What the permission card needs to draft a rule from a request and save it. */
export type CardRules = {
  projectPath: string;
  projectName: string;
  machineName: string;
  canProject: boolean;
  /** Saves the drafted rule; resolves to null on success, else the reason. */
  onSave: (
    draft: Extract<Draft, { ok: true }>,
    option: DraftOption,
    action: 'allow' | 'deny',
    scope: DraftScope,
    note: string,
  ) => Promise<string | null>;
  /** What a development fixture opens the draft dialog with. */
  open?: { action: 'allow' | 'deny'; scope?: DraftScope; note?: string; option?: string };
};

/** The hand-off requests of the session and how to answer them. */
export type HandoffView = {
  requests: readonly SessionHandoffRequest[];
  /** The machine that holds the session now. */
  fromName: string;
  projectName: string;
  nameOf: (deviceId: string) => string;
  busy: boolean;
  decidable: boolean;
  onDecide: (requestId: string, approve: boolean) => void;
};

/** The cards of the requests nobody has answered yet. */
export function PendingHandoffs(props: { view: HandoffView; providerName: string; now: number }) {
  const { view } = props;
  return view.requests
    .filter((r) => r.state === 'pending')
    .map((r) => (
      <HandoffCard
        key={r.requestId}
        request={r}
        providerName={props.providerName}
        fromName={view.fromName}
        toName={view.nameOf(r.targetDeviceId)}
        now={props.now}
        busy={view.busy}
        decidable={view.decidable}
        onDecide={(approve) => view.onDecide(r.requestId, approve)}
      />
    ));
}

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
            toolUseId={row.toolUseId}
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
          toolUseId={row.toolUseId}
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
  /** Drafting a rule from the request; absent where no rule can be saved. */
  rules?: CardRules | undefined;
  /** The session's machine, which an unreachable request waits for (ADR 0039). */
  deviceName?: string;
}) {
  const { request, now } = props;
  const away = request.unreachable === true;
  const busy = props.busy || away;
  const rules = props.rules;
  const draft = rules
    ? draftFromCall({
        toolKind: request.toolKind,
        mcp: request.mcp,
        input: request.input,
        projectPath: rules.projectPath,
      })
    : null;
  const [drafting, setDrafting] = useState<'allow' | 'deny' | null>(rules?.open?.action ?? null);
  const input = permissionInput(request.toolKind, request.input);
  const remaining = Math.min(
    PERMISSION_OPEN_MS,
    request.receivedAt + PERMISSION_OPEN_MS - (away ? (request.pausedAt ?? now) : now),
  );
  const countdown = formatCountdown(remaining);
  const big = input.text.length > PERMISSION_ELIDE_BYTES;
  return (
    <fieldset
      className={`ask${away ? ' away' : ''}`}
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
      {request.resent && !away ? (
        <div className="ask-back" data-testid="cockpit.permission-resent">
          <span className="tag">
            {Icon.retry()}
            {t('Sent again after reconnect · asked {time}').replace(
              '{time}',
              clock(request.requestedAt ?? request.receivedAt),
            )}
          </span>
        </div>
      ) : null}
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
      {request.rule ? <RuleLine rule={request.rule} /> : null}
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
        {away ? (
          <span className="ask-note" data-testid="cockpit.permission-away">
            <span className="off-dot" />
            {t('Answer once {device} reconnects; it is never approved on its own').replace(
              '{device}',
              props.deviceName ?? '',
            )}
          </span>
        ) : draft?.ok ? (
          <>
            <span className="vr" />
            <Button
              variant="quiet"
              disabled={busy}
              title={t('Draft a rule that allows this')}
              onClick={() => setDrafting('allow')}
              data-testid="cockpit.permission-always-allow"
            >
              {Icon.rules()}
              {t('Always allow…')}
            </Button>
            <Button
              variant="quiet"
              disabled={busy}
              title={t('Draft a rule that denies this')}
              onClick={() => setDrafting('deny')}
              data-testid="cockpit.permission-always-deny"
            >
              {Icon.ban()}
              {t('Always deny…')}
            </Button>
          </>
        ) : draft ? (
          <>
            <span className="vr" />
            <span className="qno" data-testid="cockpit.permission-no-rule">
              {Icon.info()}
              <span>
                {draft.reason === 'control'
                  ? t('No rule can allow a command with a pipe. It asks each time.')
                  : t('No rule can be drafted for this tool.')}
              </span>
            </span>
          </>
        ) : null}
        {away ? (
          <span
            className="ask-meta"
            title={t('The clock stops while the machine is away')}
            data-testid="cockpit.permission-paused"
          >
            {Icon.pause()}
            {fill(t('Timer paused · {time} left'), {
              time: <span className="num">{countdown}</span>,
            })}
          </span>
        ) : (
          <span
            className="ask-meta"
            title={t('Denied without an answer in {time}').replace('{time}', countdown)}
          >
            {Icon.clock()}
            {fill(t('Auto-deny in {time}'), { time: <span className="num">{countdown}</span> })}
          </span>
        )}
      </div>
      {drafting && rules && draft?.ok ? (
        <RuleDraftDialog
          draft={draft}
          action={drafting}
          projectName={rules.projectName}
          machineName={rules.machineName}
          canProject={rules.canProject}
          onSave={async (option, scope, note) => {
            const failed = await rules.onSave(draft, option, drafting, scope, note);
            if (failed) return failed;
            setDrafting(null);
            props.onAnswer(drafting);
            return null;
          }}
          onClose={() => setDrafting(null)}
          {...(rules.open?.scope ? { initialScope: rules.open.scope } : {})}
          {...(rules.open?.note ? { initialNote: rules.open.note } : {})}
          {...(rules.open?.option ? { initialOption: rules.open.option } : {})}
        />
      ) : null}
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
  /** Drafting a rule from a permission request. */
  rules?: CardRules | undefined;
  /** The session's requests to be handed off to another machine (ADR 0034). */
  handoffs?: HandoffView | undefined;
  /** The permission strips of the children this session started (ADR 0035). */
  childAsks?: ReactNode;
  /** The session's requests to change its configuration (ADR 0036). */
  configChanges?: ConfigChangeView | undefined;
  /** The session's machine went offline during the turn (ADR 0039): when it was last heard. */
  away?: { since?: number | undefined } | undefined;
  /** The machine is back and sends what it wrote while away (ADR 0039). */
  catchUp?: { received: number; total: number } | undefined;
}) {
  const { turn, base, state, now, away, catchUp } = props;
  const frozen = props.frozen || away !== undefined;
  const failed = turn.phase === 'failed';
  const need = state === 'waiting-permission' && turn.phase === 'live';
  const live = turn.phase === 'live';
  const rawPrompt = turn.prompt ?? base?.prompt?.text ?? null;
  const from = rawPrompt ? promptSource(rawPrompt) : null;
  const prompt = from ? from.text : rawPrompt;
  const startedAt = base?.ts
    ? Math.min(Date.parse(base.ts) || turn.startedAt, turn.startedAt)
    : turn.startedAt;
  const elapsed = (turn.endedAt ?? now) - turn.startedAt;
  const showBadge = state !== 'starting' && (live || failed);
  const word = failed
    ? t('Failed')
    : away
      ? need
        ? t('Waiting for you · machine offline')
        : away.since !== undefined
          ? t('May still be running · last heard {time}').replace('{time}', clock(away.since))
          : t('May still be running')
      : frozen
        ? t('Running · paused while reconnecting')
        : need
          ? t('Waiting for permission')
          : t('Running');
  const caughtUp = catchUp
    ? t('Catching up · {received} of {total}')
        .replace('{received}', formatBytes(catchUp.received))
        .replace('{total}', formatBytes(catchUp.total))
    : null;
  const percent = catchUp ? catchUpPercent(catchUp) : 0;
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
          {showBadge && away && live ? (
            <span className="badge may" data-testid="cockpit.live-state" data-away="true">
              <span className="off-dot" />
              <span>{word}</span>
            </span>
          ) : showBadge && caughtUp && live ? (
            <span className="badge cu" data-testid="cockpit.live-state" data-catching-up="true">
              <Spinner />
              <span>{caughtUp}</span>
            </span>
          ) : showBadge ? (
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
          {from ? <PromptSourceLine source={from.source} onOpen={props.onOpenSession} /> : null}
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
        {props.handoffs
          ? props.handoffs.requests
              .filter((r) => r.state !== 'pending' && r.createdAt >= turn.startedAt)
              .reverse()
              .map((r) => (
                <HandoffResult
                  key={r.requestId}
                  request={r}
                  toName={props.handoffs?.nameOf(r.targetDeviceId) ?? r.targetDeviceId}
                  projectName={props.handoffs?.projectName ?? ''}
                />
              ))
          : null}
        {props.configChanges ? (
          <ConfigChangeResults view={props.configChanges} since={turn.startedAt} />
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
                  rules={props.rules}
                  deviceName={props.deviceName}
                />
              ),
            )
          : null}
        {live && caughtUp ? (
          <>
            <div
              className="cuprog"
              role="progressbar"
              aria-valuenow={percent}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={t('Catching up')}
            >
              <i style={{ width: `${percent}%` }} />
            </div>
            <div className="quiet-line culine" data-testid="cockpit.catching-up">
              <Spinner />
              <span>
                {t(
                  'Output written while {device} was offline is arriving; the state follows when it is in',
                ).replace('{device}', props.deviceName)}
              </span>
            </div>
          </>
        ) : null}
        {live && away ? (
          <div className="quiet-line culine" data-testid="cockpit.away-line">
            {Icon.clock()}
            <span>
              {t('Anything it does from here appears when {device} reconnects').replace(
                '{device}',
                props.deviceName,
              )}
            </span>
          </div>
        ) : null}
        {live ? props.childAsks : null}
        {live && props.handoffs ? (
          <PendingHandoffs view={props.handoffs} providerName={props.providerName} now={now} />
        ) : null}
        {live && props.configChanges ? (
          <PendingConfigChange view={props.configChanges} now={now} />
        ) : null}
      </div>
    </section>
  );
}
