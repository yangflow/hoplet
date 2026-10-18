import type { SessionContinuation } from '@hoplet/shared';
import {
  type KeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { Button } from '../../../components/Button.tsx';
import { configIcon, Icon, Spinner } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { TextField } from '../../../components/TextField.tsx';
import { t } from '../../../lib/i18n.ts';
import { modeDescription, modeName, type Select } from '../composer/mode-menu.tsx';
import { displayTitle } from '../group.ts';
import { clock, formatBytes } from './format.ts';
import { type FreshPhase, useFreshPlan } from './fresh-plan.ts';
import {
  type OpenPermission,
  PLAN_COLLAPSE_BYTES,
  type PlanResolution,
  planBytes,
  planModes,
  planSteps,
} from './live-turn.ts';
import { Markdown } from './markdown.tsx';
import { ActCells, Meta } from './rows.tsx';
import { useView } from './view-context.tsx';

/** What a development fixture opens the card with. */
export type PlanView = {
  menu?: boolean;
  feedback?: string;
  /** Opens the card in a step of "Clear context and approve…", with the note of a failure. */
  fresh?: { phase: FreshPhase; error?: string };
};

function stepsText(n: number): string {
  return n === 1 ? t('1 step') : t('{n} steps').replace('{n}', String(n));
}

/** A template whose tokens are shown in the monospace face: `{project}` becomes the name in a `.mono` span. */
function withMono(template: string, tokens: Record<string, string>): ReactNode[] {
  return template.split(/(\{[a-z]+\})/).map((part, i) =>
    part in tokens ? (
      // biome-ignore lint/suspicious/noArrayIndexKey: the parts are a fixed split of one string
      <span key={i} className="mono">
        {tokens[part]}
      </span>
    ) : (
      part
    ),
  );
}

/** The facts under a plan's title: its steps when it has numbered ones, and its size. */
function planFacts(steps: number, bytes: number): string[] {
  return [...(steps > 0 ? [stepsText(steps)] : []), formatBytes(bytes)];
}

/** The plan as a document: collapsed to its first lines when long, opened in place by its size. */
function PlanDoc(props: { id: string; plan: string; bytes: number; peek?: boolean }) {
  const view = useView();
  const long = props.bytes > PLAN_COLLAPSE_BYTES;
  const open = view.isOpen(`plan:${props.id}`);
  const cut = (long && !open) || props.peek === true;
  return (
    <div
      className={`pdoc${cut ? ' cut' : ''}${props.peek ? ' peek' : ''}${long && open ? ' open' : ''}`}
      data-testid="cockpit.plan-doc"
    >
      <div className="pb" tabIndex={long && open ? 0 : undefined}>
        <Markdown text={props.plan} base={0} />
      </div>
      {long && !props.peek ? (
        <button
          type="button"
          className={open ? 'wfold' : 'wopen'}
          onClick={() => view.setOpen(`plan:${props.id}`, !open)}
          aria-expanded={open}
          data-testid="cockpit.plan-open"
        >
          {open ? t('Collapse') : t('Open {size}').replace('{size}', formatBytes(props.bytes))}
        </button>
      ) : null}
    </div>
  );
}

/**
 * A plan awaiting a decision (ADR 0019): the plan as Markdown, one primary button that approves into a mode
 * (its chevron lists the modes the driver allows) and Keep planning, which opens a field for what should change.
 */
export function PlanCard(props: {
  request: OpenPermission;
  providerName: string;
  /** The session's mode option: names, descriptions and icons of the modes. */
  modes: Select | undefined;
  /** Deciding needs the machine and the connection. */
  decidable: boolean;
  /** The owner's name, for the offline note. */
  deviceName: string;
  /** The session's project directory, named in the confirmation of a new session. */
  projectPath: string;
  busy: boolean;
  initial?: PlanView | undefined;
  /** Approves into a new session in this mode; rejects with the reason it did not start. Without it the card does not offer one. */
  onFresh?: ((mode: string, promptId: string) => Promise<void>) | undefined;
  onApprove: (mode: string) => void;
  onKeep: (feedback: string) => void;
}) {
  const { request, modes, busy } = props;
  const exit = request.planExit;
  const choices = exit ? planModes(exit, modes) : [];
  const [chosen, setChosen] = useState(choices[0] ?? '');
  const [menu, setMenu] = useState(props.initial?.menu ?? false);
  const [feedback, setFeedback] = useState<string | null>(props.initial?.feedback ?? null);
  const menuRef = useRef<HTMLDivElement>(null);
  const anchor = useRef<HTMLSpanElement>(null);
  const field = useRef<HTMLDivElement>(null);
  const focusMenu = useRef(false);
  const typing = feedback !== null;
  const exitFresh = exit?.freshContext === true && props.onFresh !== undefined;
  const { onFresh } = props;
  const fresh = useFreshPlan({
    start: useCallback(
      (promptId) => onFresh?.(chosen, promptId) ?? Promise.resolve(),
      [onFresh, chosen],
    ),
    device: props.deviceName,
    initial: props.initial?.fresh,
  });
  const phase = fresh.phase;
  const confirmBox = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (phase === 'confirm' && !props.initial?.fresh)
      confirmBox.current?.querySelector('button')?.focus();
  }, [phase, props.initial?.fresh]);
  useEffect(() => {
    if (typing && !props.initial?.feedback) field.current?.querySelector('textarea')?.focus();
  }, [typing, props.initial?.feedback]);
  useEffect(() => {
    if (!menu || !focusMenu.current) return;
    focusMenu.current = false;
    menuRef.current
      ?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]')
      ?.focus();
  }, [menu]);
  if (!exit) return null;

  const bytes = planBytes(exit.plan);
  const facts = planFacts(planSteps(exit.plan), bytes);
  const label = (value: string) => {
    const v = modes?.options.find((o) => o.value === value);
    return modes ? modeName(modes, value, v?.name ?? value) : value;
  };
  const off = !props.decidable || busy;
  const projectName = props.projectPath.split('/').filter(Boolean).pop() ?? props.projectPath;
  /** Anything but the plain card: the plan folds to its first lines. */
  const freshStep = phase !== 'idle';
  const fileLine = exit.planFile
    ? withMono(
        t('It starts from the plan and its file, {file}, and nothing else of this conversation'),
        { '{file}': exit.planFile },
      )
    : t('It starts from the plan and nothing else of this conversation');

  function closeMenu() {
    setMenu(false);
    anchor.current?.querySelector('button')?.focus();
  }
  function onMenuKey(e: KeyboardEvent) {
    const items = [...(menuRef.current?.querySelectorAll<HTMLElement>('[role^="menuitem"]') ?? [])];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const go = (i: number) => items[(i + items.length) % items.length]?.focus();
    if (e.key === 'ArrowDown') go(at + 1);
    else if (e.key === 'ArrowUp') go(at < 0 ? -1 : at - 1);
    else if (e.key === 'Home') go(0);
    else if (e.key === 'End') go(-1);
    else if (e.key === 'Escape') closeMenu();
    else if (e.key === 'Tab') setMenu(false);
    else return;
    if (e.key !== 'Tab') {
      e.preventDefault();
      e.stopPropagation();
    }
  }
  const send = () => {
    const text = (feedback ?? '').trim();
    if (text !== '' && !off) props.onKeep(text);
  };

  return (
    <fieldset
      className="ask plan"
      title={t('Request {id}').replace('{id}', request.requestId.slice(0, 4))}
      data-testid="cockpit.plan"
    >
      <legend className="sr-only">
        {t('{provider} has a plan').replace('{provider}', props.providerName)}
      </legend>
      <div className="ask-h">
        {Icon.listChecks()}
        <span className="q">
          {t('{provider} has a plan').replace('{provider}', props.providerName)}
        </span>
        <span className="pmeta">{facts.join(' · ')}</span>
      </div>
      <PlanDoc id={request.requestId} plan={exit.plan} bytes={bytes} peek={typing || freshStep} />
      {typing ? (
        <div className="pfield" ref={field}>
          <TextField
            multiline
            showLabel
            label={t('What should change?')}
            value={feedback}
            rows={3}
            maxLength={10_000}
            disabled={!props.decidable}
            onChange={(e) => setFeedback(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                send();
              }
            }}
            data-testid="cockpit.plan-feedback"
          />
        </div>
      ) : null}
      {phase === 'confirm' ? (
        <div ref={confirmBox} data-testid="cockpit.plan-fresh-confirm">
          <div className="pconf">
            <b>{t('Start a new session with this plan?')}</b>
            <div className="pcr">
              {Icon.laptop()}
              <span>
                {withMono(
                  t('A new session on {device} in {project}, in {mode}')
                    .replace('{device}', props.deviceName)
                    .replace('{mode}', label(chosen)),
                  { '{project}': projectName },
                )}
              </span>
            </div>
            <div className="pcr">
              {Icon.listChecks()}
              <span>{fileLine}</span>
            </div>
            <div className="pcr">
              {Icon.archive()}
              <span>
                {t('This session stops and keeps its history, with a link to the new one')}
              </span>
            </div>
          </div>
          <div className="ask-f">
            <Button
              variant="primary"
              disabled={off || chosen === ''}
              onClick={() => void fresh.start()}
              data-testid="cockpit.plan-fresh-start"
            >
              {t('Start new session · {mode}').replace('{mode}', label(chosen))}
            </Button>
            <Button disabled={busy} onClick={fresh.back} data-testid="cockpit.plan-fresh-back">
              {t('Back')}
            </Button>
          </div>
        </div>
      ) : phase === 'starting' ? (
        <div className="ask-f" data-testid="cockpit.plan-fresh-starting">
          <Button variant="primary" disabled aria-busy="true">
            <Spinner />
            {t('Starting new session…')}
          </Button>
          <Button disabled>{t('Keep planning')}</Button>
          <span className="ask-meta">
            {Icon.info()}
            <span>{t('This session waits until the new one is up')}</span>
          </span>
        </div>
      ) : phase === 'failed' ? (
        <>
          <div className="pnote bad" role="alert" data-testid="cockpit.plan-fresh-error">
            {Icon.alert()}
            <span>{fresh.error}</span>
          </div>
          <div className="ask-f">
            <Button
              variant="primary"
              disabled={off}
              icon={Icon.retry()}
              onClick={() => void fresh.start()}
              data-testid="cockpit.plan-fresh-retry"
            >
              {t('Try again')}
            </Button>
            <Button
              disabled={off || chosen === ''}
              onClick={() => {
                fresh.back();
                props.onApprove(chosen);
              }}
              data-testid="cockpit.plan-fresh-here"
            >
              {t('Approve here · {mode}').replace('{mode}', label(chosen))}
            </Button>
            <Button
              disabled={off}
              onClick={() => {
                fresh.back();
                setFeedback('');
              }}
            >
              {t('Keep planning')}
            </Button>
          </div>
        </>
      ) : typing ? (
        <div className="ask-f">
          <Button
            variant="primary"
            disabled={off || (feedback ?? '').trim() === ''}
            onClick={send}
            data-testid="cockpit.plan-send"
          >
            {t('Send feedback')}
          </Button>
          <Button disabled={busy} onClick={() => setFeedback(null)} data-testid="cockpit.plan-back">
            {t('Back to plan')}
          </Button>
          <span className="ask-meta">
            {Icon.info()}
            <span>{t('{provider} keeps planning').replace('{provider}', props.providerName)}</span>
          </span>
        </div>
      ) : (
        <div className="ask-f">
          <span className="split">
            <Button
              variant="primary"
              disabled={off || chosen === ''}
              onClick={() => props.onApprove(chosen)}
              data-testid="cockpit.plan-approve"
            >
              {t('Approve · {mode}').replace('{mode}', label(chosen))}
            </Button>
            {choices.length > 1 || exitFresh ? (
              <span className="model-menu anc" ref={anchor}>
                <Button
                  variant="primary"
                  disabled={off}
                  aria-label={t('Mode after approval')}
                  title={t('Mode after approval')}
                  aria-haspopup="menu"
                  aria-expanded={menu}
                  onClick={() => {
                    focusMenu.current = true;
                    setMenu((m) => !m);
                  }}
                  data-testid="cockpit.plan-modes"
                >
                  {Icon.down()}
                </Button>
                <Popover
                  open={menu}
                  onClose={() => setMenu(false)}
                  className="popover-above popover-wide"
                >
                  <div
                    ref={menuRef}
                    role="menu"
                    aria-label={t('Mode after approval')}
                    tabIndex={-1}
                    onKeyDown={onMenuKey}
                    data-testid="cockpit.plan-mode-menu"
                  >
                    <div className="popover-label">{t('After approval, switch to')}</div>
                    {choices.map((value) => {
                      const v = modes?.options.find((o) => o.value === value);
                      const description = modes
                        ? modeDescription(modes, value, v?.description)
                        : '';
                      return (
                        <button
                          key={value}
                          type="button"
                          role="menuitemradio"
                          aria-checked={value === chosen}
                          tabIndex={-1}
                          className="option mode-row"
                          onClick={() => {
                            setChosen(value);
                            closeMenu();
                          }}
                          data-testid="cockpit.plan-mode-option"
                        >
                          <span className="mode-check">
                            {value === chosen ? Icon.check() : null}
                          </span>
                          <span className="mode-text">
                            <span className="mode-name">{label(value)}</span>
                            {description ? <span className="mode-desc">{description}</span> : null}
                          </span>
                        </button>
                      );
                    })}
                    {exitFresh ? (
                      <>
                        <div className="popover-sep" aria-hidden="true" />
                        <button
                          type="button"
                          role="menuitem"
                          tabIndex={-1}
                          className="option mode-row"
                          onClick={() => {
                            closeMenu();
                            fresh.ask();
                          }}
                          data-testid="cockpit.plan-fresh-option"
                        >
                          <span className="mode-check">{Icon.compose()}</span>
                          <span className="mode-text">
                            <span className="mode-name">{t('Clear context and approve…')}</span>
                            <span className="mode-desc">
                              {t('Start a new session with only the plan')}
                            </span>
                          </span>
                        </button>
                      </>
                    ) : null}
                  </div>
                </Popover>
              </span>
            ) : null}
          </span>
          <Button disabled={off} onClick={() => setFeedback('')} data-testid="cockpit.plan-keep">
            {t('Keep planning')}
          </Button>
          <span className="ask-meta">
            {Icon.info()}
            <span>{t('Approving leaves Plan mode')}</span>
          </span>
        </div>
      )}
      {!props.decidable ? (
        <div className="quiet-line pnote" data-testid="cockpit.plan-offline">
          {Icon.clock()}
          <span>
            {t(
              '{device} is offline. The plan stays here; deciding needs the machine online.',
            ).replace('{device}', props.deviceName)}
          </span>
        </div>
      ) : null}
    </fieldset>
  );
}

/** The other session of a plan approved into a new session, as a chip that opens it; an empty title shows the placeholder. */
export function SessionChip(props: {
  link: SessionContinuation;
  /** What opening it does, for the tooltip and the screen reader. */
  label: string;
  onOpen?: ((sessionId: string) => void) | undefined;
}) {
  const title = displayTitle({ title: props.link.title });
  return (
    <button
      type="button"
      className="mchip lnk"
      title={props.label}
      aria-label={`${props.label}: ${title}`}
      onClick={() => props.onOpen?.(props.link.sessionId)}
      data-testid="cockpit.session-chip"
    >
      {title}
      {Icon.external()}
    </button>
  );
}

/** The line at the head of a session that began from a plan: where the plan came from. */
export function PlanOrigin(props: {
  from: SessionContinuation;
  onOpen?: ((sessionId: string) => void) | undefined;
}) {
  return (
    <div className="pres from" data-testid="cockpit.plan-origin">
      {Icon.back()}
      <span>{t('From the plan in')}</span>
      <SessionChip
        link={props.from}
        label={t('Open the session this plan came from')}
        onOpen={props.onOpen}
      />
    </div>
  );
}

/** A decided plan: a fold with the plan, and the result line (and the feedback, when it was kept). */
export function PlanResolved(props: {
  plan: PlanResolution;
  modes: Select | undefined;
  /** The session this plan went on in, once the core has recorded it (ADR 0025). */
  continuedIn?: SessionContinuation | undefined;
  onOpenSession?: ((sessionId: string) => void) | undefined;
}) {
  const { plan, modes } = props;
  // Another window may have approved it into a new session: the session's link says so.
  const fresh =
    plan.fresh === true || (plan.outcome === 'elsewhere' && props.continuedIn !== undefined);
  const view = useView();
  const key = `planres:${plan.requestId}`;
  const open = view.isOpen(key);
  const chip = (value: string) => {
    const v = modes?.options.find((o) => o.value === value);
    return (
      <span className="mchip">
        {configIcon(v?.icon)}
        {modes ? modeName(modes, value, v?.name ?? value) : value}
      </span>
    );
  };
  return (
    <div data-testid="cockpit.plan-resolved" data-outcome={plan.outcome}>
      <details
        className="act-d"
        open={open}
        onToggle={(e) => view.setOpen(key, e.currentTarget.open)}
      >
        <summary className="act">
          <ActCells
            icon={Icon.listChecks()}
            verb={t('Planned')}
            object={plan.title}
            meta={<Meta parts={planFacts(plan.steps, plan.bytes)} />}
          />
        </summary>
        {open ? (
          <div className="pdoc inline">
            <div className="pb">
              <Markdown text={plan.plan} />
            </div>
          </div>
        ) : null}
      </details>
      {fresh ? (
        <div className="pres ok" data-testid="cockpit.plan-fresh-done">
          {Icon.check()}
          <span>
            <b>{t('Approved in a new session')}</b>
          </span>
          {props.continuedIn ? (
            <SessionChip
              link={props.continuedIn}
              label={t('Open the new session')}
              onOpen={props.onOpenSession}
            />
          ) : null}
          <span className="meta">{clock(plan.at)}</span>
        </div>
      ) : (
        <div className={`pres${plan.outcome === 'kept' ? '' : ' ok'}`}>
          {plan.outcome === 'kept'
            ? Icon.back()
            : plan.outcome === 'approved'
              ? Icon.check()
              : Icon.info()}
          <span>
            {plan.outcome === 'approved' ? (
              <>
                <b>{t('Plan approved')}</b>
                {plan.mode ? ` · ${t('switched to')}` : ''}
              </>
            ) : plan.outcome === 'kept' ? (
              <>
                <b>{t('Kept planning')}</b>
                {plan.mode ? ` · ${t('still in')}` : ''}
              </>
            ) : (
              <b>{t('Answered in another window')}</b>
            )}
          </span>
          {plan.mode ? chip(plan.mode) : null}
          <span className="meta">{clock(plan.at)}</span>
        </div>
      )}
      {plan.feedback ? <div className="pquote">{plan.feedback}</div> : null}
    </div>
  );
}
