import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { configIcon, Icon } from '../../../components/icons.tsx';
import { Popover } from '../../../components/Popover.tsx';
import { TextField } from '../../../components/TextField.tsx';
import { t } from '../../../lib/i18n.ts';
import { modeDescription, modeName, type Select } from '../composer/mode-menu.tsx';
import { clock, formatBytes } from './format.ts';
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
export type PlanView = { menu?: boolean; feedback?: string };

function stepsText(n: number): string {
  return n === 1 ? t('1 step') : t('{n} steps').replace('{n}', String(n));
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
  busy: boolean;
  initial?: PlanView | undefined;
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

  function closeMenu() {
    setMenu(false);
    anchor.current?.querySelector('button')?.focus();
  }
  function onMenuKey(e: KeyboardEvent) {
    const items = [
      ...(menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []),
    ];
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
      <PlanDoc id={request.requestId} plan={exit.plan} bytes={bytes} peek={typing} />
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
      {typing ? (
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
            {choices.length > 1 ? (
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

/** A decided plan: a fold with the plan, and the result line (and the feedback, when it was kept). */
export function PlanResolved(props: { plan: PlanResolution; modes: Select | undefined }) {
  const { plan, modes } = props;
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
      {plan.feedback ? <div className="pquote">{plan.feedback}</div> : null}
    </div>
  );
}
