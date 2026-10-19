import type { DeviceCloneOffer, SnapshotChanges } from '@hoplet/shared';
import type { ReactNode } from 'react';
import { useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { Select } from '../../components/Select.tsx';
import { TextField } from '../../components/TextField.tsx';
import { clock, formatBytes } from '../../lib/format.ts';
import { t } from '../../lib/i18n.ts';
import { systemOf } from '../../lib/system.ts';
import { CloneInto, destinationText } from './clone-into.tsx';
import {
  CLONE_UNREADABLE,
  failureCode,
  failureDetail,
  failureText,
  type Handoff,
  type HandoffRun,
  originLabel,
  type Step,
  stepsOf,
} from './handoff.ts';
import { formatCount } from './model.ts';

const stepLabel = (step: Step) =>
  ({
    clone: t('Clone'),
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
  const [choosing, setChoosing] = useState(handoff.dialogFixture !== undefined);
  const nameOf = (id: string) => handoff.deviceName(id) || t('the other machine');
  const run = handoff.run;
  if (run) return <RunCard run={run} target={nameOf(run.targetDeviceId)} {...props} />;

  const target = nameOf(handoff.targetId);
  const named = (text: string) => t(text).replace('{target}', target).replace('{owner}', owner);
  const clone = handoff.clone;
  const needsPath = clone.state === 'none';
  const offer = clone.state === 'offer' ? clone.offer : null;
  // An offer starts a clone; one the target cannot take (no access, a taken folder) starts nothing.
  const canStart =
    handoff.targetId !== '' &&
    !props.busy &&
    clone.state !== 'checking' &&
    (offer ? offer.reason === 'ok' : true) &&
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
              leading: <MachineIcon kind={systemOf(d.system?.os)} online={d.connected} />,
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
          ) : clone.state === 'found' ? null : offer ? (
            <CloneOffer
              offer={offer}
              target={target}
              onChange={() => setChoosing(true)}
              named={named}
            />
          ) : (
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
          {offer?.reason === 'no-access' ? (
            <Button
              variant="primary"
              icon={Icon.handoff()}
              onClick={handoff.recheck}
              data-testid="workspace.handoff-retry-check"
            >
              {t('Try again')}
            </Button>
          ) : (
            <Button
              variant="primary"
              icon={Icon.handoff()}
              onClick={handoff.start}
              disabled={!canStart}
              data-testid="workspace.handoff-run"
            >
              {offer ? named('Clone and hand off to {target}') : named('Hand off to {target}')}
            </Button>
          )}
          {props.busy ? (
            <p className="ws-ho-why">
              <Glyph>{Icon.lock()}</Glyph>
              <span>{t('Available when the turn ends')}</span>
            </p>
          ) : offer?.reason === 'ok' ? (
            <p className="ws-ho-why" data-testid="workspace.handoff-clone-cost">
              <Glyph>{Icon.info()}</Glyph>
              <span>
                {named(
                  '{target} clones it first. {owner} keeps running until the clone has finished; then the hand off starts.',
                )}
              </span>
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
      {choosing && offer ? (
        <CloneInto
          deviceId={handoff.targetId}
          deviceName={target}
          system={handoff.otherDevices.find((d) => d.deviceId === handoff.targetId)?.system ?? null}
          current={offer.cloneInto}
          check={handoff.chooseDestination}
          onChosen={() => setChoosing(false)}
          onClose={() => setChoosing(false)}
          fixture={handoff.dialogFixture}
        />
      ) : null}
    </Card>
  );
}

/** The target has no clone and the core offers one: where it would go, or why it cannot. */
function CloneOffer(props: {
  offer: DeviceCloneOffer;
  target: string;
  onChange: () => void;
  named: (text: string) => string;
}) {
  const { offer, named } = props;
  const origin = originLabel(offer.origin);
  const taken =
    offer.reason === 'destination-taken'
      ? destinationText(offer.detail, {
          name: offer.repoName,
          folder: offer.cloneInto,
          device: props.target,
        })
      : null;
  return (
    <>
      {offer.reason === 'no-access' ? (
        <p className="ws-ho-note bad" role="alert" data-testid="workspace.handoff-no-access">
          <Glyph>{Icon.alert()}</Glyph>
          <span>
            {t(CLONE_UNREADABLE)
              .replaceAll('{target}', props.target)
              .replace('{origin}', origin)
              .replace('{host}', origin.split('/')[0] ?? origin)}
          </span>
        </p>
      ) : null}
      <p className="ws-ho-l" data-testid="workspace.handoff-no-clone">
        <WithMono
          text={named('{target} has no clone of {origin}.')}
          slot="{origin}"
          value={origin}
        />
      </p>
      <div className="ws-pick" data-testid="workspace.handoff-suggest">
        <span className="ws-pick-i" aria-hidden="true">
          {Icon.folder()}
        </span>
        <span className="ws-pick-v path-tail" title={offer.cloneInto}>
          <bdi>{offer.cloneInto}</bdi>
        </span>
        <button
          type="button"
          className="ws-pick-change"
          onClick={props.onChange}
          data-testid="workspace.handoff-change"
        >
          {t('Change')}
        </button>
      </div>
      {taken ? (
        <p className="ws-ho-note bad" role="alert" data-testid="workspace.handoff-taken">
          <Glyph>{Icon.alert()}</Glyph>
          <span>{taken}</span>
        </p>
      ) : null}
    </>
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
  const steps = stepsOf(run);
  const at = run.current ? steps.indexOf(run.current) + 1 : 0;
  const detail = failureDetail(run);
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
          ? t('{n} of {total}').replace('{n}', String(at)).replace('{total}', String(steps.length))
          : undefined
      }
      onClose={run.outcome === 'running' ? undefined : props.onClose}
      busy={run.outcome === 'running'}
    >
      <ol className="ws-steps" aria-label={t('Steps')}>
        {steps.map((step) => (
          <StepRow key={step} step={step} steps={steps} run={run} target={target} />
        ))}
      </ol>
      {run.outcome === 'running' ? (
        <>
          <p className="ws-ho-why">
            <Glyph>{Icon.lock()}</Glyph>
            <span>
              {run.current === 'clone'
                ? named(
                    '{owner} keeps running until the clone has finished. Nothing moves until commit.',
                  )
                : t('Nothing moves until commit. A failure leaves owner and cursor unchanged.')}
            </span>
          </p>
          {run.current === 'clone' && run.handoffId ? (
            <Button
              onClick={props.handoff.cancel}
              disabled={props.handoff.cancelling}
              data-testid="workspace.handoff-cancel"
            >
              {t('Cancel clone')}
            </Button>
          ) : null}
        </>
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
          {detail ? (
            <p className="ws-ho-detail ws-mono" data-testid="workspace.handoff-failed-detail">
              {detail}
            </p>
          ) : null}
          <Button
            variant="primary"
            icon={Icon.handoff()}
            onClick={props.handoff.start}
            data-testid="workspace.handoff-retry"
          >
            {run.failedStep === 'clone' ? t('Try again') : named('Hand off to {target}')}
          </Button>
        </>
      )}
    </Card>
  );
}

/** One step: its mark, its name and what it did (who, how much, how long). */
function StepRow(props: { step: Step; steps: readonly Step[]; run: HandoffRun; target: string }) {
  const { step, steps, run, target } = props;
  const times = run.steps[step];
  const index = steps.indexOf(step);
  const failedAt = run.outcome === 'failed' ? steps.indexOf(run.failedStep ?? 'prepare') : -1;
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
          : times?.end !== undefined || (run.current && index < steps.indexOf(run.current))
            ? 'ok'
            : 'todo';
  const took =
    times?.at !== undefined && times.end !== undefined ? formatSeconds(times.end - times.at) : '';
  const join = (...parts: string[]) => parts.filter(Boolean).join(' · ');
  let meta = '';
  if (state === 'bad') meta = failureCode(run);
  else if (state === 'ok')
    meta = {
      clone: join(run.cloned !== undefined ? formatBytes(run.cloned) : '', took),
      prepare:
        run.snapshotAt !== undefined
          ? t('Snapshot {time}').replace('{time}', clock(run.snapshotAt))
          : join(run.ownerName, took),
      transfer: join(run.total !== undefined ? formatBytes(run.total) : '', took),
      install: join(target, took),
      verify: t('SHA-256 and tree match'),
      commit: clock(times?.end ?? times?.at),
    }[step];
  else if (state === 'now' && (step === 'transfer' || step === 'clone') && run.sent !== undefined)
    meta = run.total !== undefined ? formatProgress(run.sent, run.total) : formatBytes(run.sent);
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
      {state === 'now' && (step === 'transfer' || step === 'clone') && run.total ? (
        <span
          className="ws-bar"
          role="progressbar"
          aria-label={stepLabel(step)}
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
