import type { SnapshotChanges } from '@hoplet/shared';
import type { ReactNode } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Select } from '../../components/Select.tsx';
import { TextField } from '../../components/TextField.tsx';
import { t } from '../../lib/i18n.ts';
import { clock, formatBytes } from '../cockpit/conversation/format.ts';
import {
  failureCode,
  failureText,
  type Handoff,
  type HandoffRun,
  originLabel,
  STEPS,
  type Step,
} from './handoff.ts';
import { formatCount } from './model.ts';

const stepLabel = (step: Step) =>
  ({
    prepare: t('Prepare'),
    transfer: t('Transfer'),
    install: t('Install'),
    verify: t('Verify'),
    commit: t('Commit'),
  })[step];

/** A step's time as the design writes it: `0.8 s`, `12 s`. */
export function formatSeconds(ms: number): string {
  const s = Math.max(0, ms) / 1000;
  return `${s < 10 ? s.toFixed(1) : Math.round(s)} s`;
}

/** Bytes sent of a total, the unit once when both share it: `6.2 / 10.4 MiB`. */
export function formatProgress(sent: number, total: number): string {
  const a = formatBytes(sent);
  const b = formatBytes(total);
  const unit = (s: string) => s.slice(s.lastIndexOf(' '));
  return unit(a) === unit(b) ? `${a.slice(0, a.lastIndexOf(' '))} / ${b}` : `${a} / ${b}`;
}

/**
 * The handoff panel at the foot of the inspector (docs/spec.md#workspace-inspector): the target, its clone
 * or the path of one, and the button; while a handoff runs and after it ended, its steps and result.
 */
export function HandoffPanel(props: {
  handoff: Handoff;
  online: boolean;
  owner: string;
  /** The stored snapshot an offline handoff moves; null while the owner is online. */
  snapshot: SnapshotChanges | null;
  /** The session is in a turn: the core refuses a handoff until it ends. */
  busy: boolean;
  onClose: () => void;
}) {
  const { handoff, owner } = props;
  const nameOf = (id: string) => handoff.deviceName(id) || t('the other machine');
  const run = handoff.run;
  if (run) return <RunCard run={run} target={nameOf(run.targetDeviceId)} {...props} />;

  const target = nameOf(handoff.targetId);
  const named = (text: string) => t(text).replace('{target}', target).replace('{owner}', owner);
  const clone = handoff.clone;
  const needsPath = clone.state === 'none';
  const canStart =
    handoff.targetId !== '' &&
    !props.busy &&
    clone.state !== 'checking' &&
    (!needsPath || handoff.targetPath.trim() !== '');

  return (
    <Card title={t('Hand off')} onClose={props.onClose} focus>
      <p className="ws-ho-lede">
        {props.online
          ? t('Move this session and its working tree to another machine.')
          : named('{owner} is offline. The session moves from its last snapshot.')}
      </p>
      {props.snapshot ? (
        <p className="ws-ho-note snap" data-testid="workspace.handoff-snapshot">
          <Glyph>{Icon.clock()}</Glyph>
          <span>
            {t('Snapshot {time} · {n} files · {size}')
              .replace('{time}', clock(props.snapshot.snapshotAt))
              .replace('{n}', formatCount(props.snapshot.changes.length))
              .replace('{size}', formatBytes(props.snapshot.byteSize))}
          </span>
        </p>
      ) : null}
      {handoff.otherDevices.length === 0 ? (
        <p className="ws-ho-why">
          <Glyph>{Icon.info()}</Glyph>
          <span>{t('Connect another agent to hand off this session.')}</span>
        </p>
      ) : (
        <>
          <Select
            label={t('Machine')}
            value={handoff.targetId}
            options={handoff.otherDevices.map((d) => ({
              value: d.deviceId,
              label: d.name || d.deviceId,
              leading: <span className="dot" />,
            }))}
            onChange={handoff.setTargetId}
            detail={t('Connected machines')}
            data-testid="workspace.handoff-target"
          />
          {clone.state === 'checking' ? (
            <p className="ws-ho-why" role="status">
              <Glyph>
                <Spinner />
              </Glyph>
              <span>{named('Looking for a clone on {target}')}</span>
            </p>
          ) : clone.state === 'found' ? null : (
            <>
              <p className="ws-ho-l" data-testid="workspace.handoff-no-clone">
                {clone.state === 'unknown' ? (
                  named(
                    '{target} did not say whether it has a clone. Give the path of one, or hand off and let it look.',
                  )
                ) : handoff.origin ? (
                  <WithMono
                    text={named('{target} has no clone of {origin}.')}
                    slot="{origin}"
                    value={originLabel(handoff.origin)}
                  />
                ) : (
                  named('The repository has no origin. Give the path of a clone on {target}.')
                )}
              </p>
              <TextField
                label={named('Clone on {target}')}
                showLabel
                mono
                value={handoff.targetPath}
                placeholder="/Users/me/src/repository"
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => handoff.setTargetPath(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && canStart) handoff.start();
                }}
                data-testid="workspace.handoff-path"
              />
            </>
          )}
          <Button
            variant="primary"
            icon={Icon.handoff()}
            onClick={handoff.start}
            disabled={!canStart}
            data-testid="workspace.handoff-run"
          >
            {named('Hand off to {target}')}
          </Button>
          {props.busy ? (
            <p className="ws-ho-why">
              <Glyph>{Icon.lock()}</Glyph>
              <span>{t('Available when the turn ends')}</span>
            </p>
          ) : clone.state === 'found' ? (
            <p className="ws-ho-why" data-testid="workspace.handoff-clone">
              <Glyph>{Icon.info()}</Glyph>
              <span>
                <WithMono
                  text={named(
                    '{target} adds a worktree next to {path} and continues the session there. Nothing moves until it verifies the copy.',
                  )}
                  slot="{path}"
                  value={clone.path}
                />
              </span>
            </p>
          ) : null}
        </>
      )}
    </Card>
  );
}

/** A handoff under way, or how it ended. */
function RunCard(props: {
  run: HandoffRun;
  target: string;
  handoff: Handoff;
  onClose: () => void;
}) {
  const { run, target } = props;
  const named = (text: string) =>
    t(text).replace('{target}', target).replace('{owner}', run.ownerName);
  const at = run.current ? STEPS.indexOf(run.current) + 1 : 0;
  const title =
    run.outcome === 'running'
      ? named('Handing off to {target}')
      : run.outcome === 'done'
        ? named('Handed off to {target}')
        : t('Hand off failed');
  return (
    <Card
      title={title}
      meta={
        run.outcome === 'running' && at > 0
          ? t('{n} of {total}').replace('{n}', String(at)).replace('{total}', String(STEPS.length))
          : undefined
      }
      onClose={run.outcome === 'running' ? undefined : props.onClose}
      busy={run.outcome === 'running'}
    >
      <ol className="ws-steps" aria-label={t('Steps')}>
        {STEPS.map((step) => (
          <StepRow key={step} step={step} run={run} target={target} />
        ))}
      </ol>
      {run.outcome === 'running' ? (
        <p className="ws-ho-why">
          <Glyph>{Icon.lock()}</Glyph>
          <span>
            {t('Nothing moves until commit. A failure leaves owner and cursor unchanged.')}
          </span>
        </p>
      ) : run.outcome === 'done' ? (
        <p className="ws-ho-note ok" role="status" data-testid="workspace.handoff-done">
          <Glyph>{Icon.check()}</Glyph>
          <span>
            {run.worktree ? (
              <WithMono
                text={t('Worktree {path} on branch {branch}')}
                slot="{path}"
                value={run.worktree}
                then={{ slot: '{branch}', value: run.branch ?? '' }}
              />
            ) : (
              named('{target} owns the session now.')
            )}
          </span>
        </p>
      ) : (
        <>
          <p className="ws-ho-note bad" role="alert" data-testid="workspace.handoff-failed">
            <Glyph>{Icon.alert()}</Glyph>
            <span>{failureText(run, target)}</span>
          </p>
          <Button
            variant="primary"
            icon={Icon.handoff()}
            onClick={props.handoff.start}
            data-testid="workspace.handoff-retry"
          >
            {named('Hand off to {target}')}
          </Button>
        </>
      )}
    </Card>
  );
}

/** One step: its mark, its name and what it did (who, how much, how long). */
function StepRow(props: { step: Step; run: HandoffRun; target: string }) {
  const { step, run, target } = props;
  const times = run.steps[step];
  const index = STEPS.indexOf(step);
  const failedAt = run.outcome === 'failed' ? STEPS.indexOf(run.failedStep ?? 'prepare') : -1;
  const state =
    run.outcome === 'done'
      ? 'ok'
      : run.outcome === 'failed'
        ? run.failedStep === step
          ? 'bad'
          : run.failedStep && index < failedAt
            ? 'ok'
            : 'todo'
        : run.current === step
          ? 'now'
          : times?.end !== undefined || (run.current && index < STEPS.indexOf(run.current))
            ? 'ok'
            : 'todo';
  const took =
    times?.at !== undefined && times.end !== undefined ? formatSeconds(times.end - times.at) : '';
  const join = (...parts: string[]) => parts.filter(Boolean).join(' · ');
  let meta = '';
  if (state === 'bad') meta = failureCode(run);
  else if (state === 'ok')
    meta = {
      prepare:
        run.snapshotAt !== undefined
          ? t('Snapshot {time}').replace('{time}', clock(run.snapshotAt))
          : join(run.ownerName, took),
      transfer: join(run.total !== undefined ? formatBytes(run.total) : '', took),
      install: join(target, took),
      verify: t('SHA-256 and tree match'),
      commit: clock(times?.end ?? times?.at),
    }[step];
  else if (state === 'now' && step === 'transfer' && run.total !== undefined)
    meta = formatProgress(run.sent ?? 0, run.total);
  const mark =
    state === 'ok' ? (
      Icon.check()
    ) : state === 'bad' ? (
      Icon.close()
    ) : state === 'now' ? (
      <Spinner />
    ) : (
      <span className="ws-pend" />
    );
  const words = { ok: t('done'), bad: t('failed'), now: t('under way'), todo: t('not started') }[
    state
  ];
  return (
    <li className={`ws-step ${state}`} aria-current={state === 'now' ? 'step' : undefined}>
      <span className="ws-step-i" aria-hidden="true">
        {mark}
      </span>
      <span className="ws-step-l">
        {stepLabel(step)}
        <span className="sr-only">, {words}</span>
      </span>
      <span className="ws-step-m">{meta}</span>
      {state === 'now' && step === 'transfer' && run.total ? (
        <span
          className="ws-bar"
          role="progressbar"
          aria-label={t('Transfer')}
          aria-valuemin={0}
          aria-valuemax={run.total}
          aria-valuenow={run.sent ?? 0}
          aria-valuetext={meta}
        >
          <i style={{ width: `${Math.min(100, ((run.sent ?? 0) / run.total) * 100)}%` }} />
        </span>
      ) : null}
    </li>
  );
}

/** The raised card the panel is, with its title, an optional count and the close button. */
function Card(props: {
  title: string;
  meta?: string | undefined;
  onClose?: (() => void) | undefined;
  focus?: boolean;
  busy?: boolean;
  children: ReactNode;
}) {
  return (
    <section
      className={`ws-ho${props.focus ? ' focus' : ''}`}
      aria-label={t('Hand off')}
      aria-busy={props.busy || undefined}
      data-testid="workspace.handoff-panel"
    >
      <header className="ws-ho-h">
        <h2>{props.title}</h2>
        {props.meta ? <span className="ws-ho-meta">{props.meta}</span> : null}
        {props.onClose ? (
          <IconButton
            label={t('Close')}
            icon={Icon.close()}
            onClick={props.onClose}
            data-testid="workspace.handoff-close"
          />
        ) : null}
      </header>
      {props.children}
    </section>
  );
}

/** A sentence with values in mono type where it names their slots. */
function WithMono(props: {
  text: string;
  slot: string;
  value: string;
  then?: { slot: string; value: string };
}) {
  const [before = '', rest = ''] = props.text.split(props.slot);
  if (!props.then)
    return (
      <>
        {before}
        <span className="ws-mono">{props.value}</span>
        {rest}
      </>
    );
  const [middle = '', after = ''] = rest.split(props.then.slot);
  return (
    <>
      {before}
      <span className="ws-mono">{props.value}</span>
      {middle}
      <span className="ws-mono">{props.then.value}</span>
      {after}
    </>
  );
}

/** The glyph that leads a line of the panel, in the line's colour. */
function Glyph(props: { children: ReactNode }) {
  return (
    <span className="ws-ho-g" aria-hidden="true">
      {props.children}
    </span>
  );
}
