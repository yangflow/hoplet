import type { Budget, BudgetListResponse, BudgetScopeKind, DeviceInfo } from '@hoplet/shared';
import { type KeyboardEvent, type ReactNode, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { Select } from '../../components/Select.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import {
  amountText,
  type BudgetDraft,
  type BudgetStatus,
  barKind,
  barPercent,
  draftOf,
  endDate,
  leftOf,
  newDraft,
  nextDate,
  PERIOD_WORD,
  problemOf,
  projectLabel,
  resetText,
  SCOPE_LABEL,
  scopeName,
  statusOf,
  toBody,
} from '../../lib/budgets.ts';
import { t } from '../../lib/i18n.ts';
import type { BudgetsState } from '../../lib/use-budgets.ts';
import { useDevices } from '../../lib/use-devices.ts';

/** What the development fixtures hand to Settings > Usage > Budgets besides the list itself. */
export type BudgetsFixture = {
  list: BudgetListResponse;
  /** The ID of the budget whose form starts open, or `new`. */
  editing?: string;
  /** A toast that starts on screen. */
  toast?: string;
  /** The project paths the form offers, instead of those of the sessions. */
  projects?: string[];
  devices?: DeviceInfo[];
};

const SCOPE_ICON: Record<BudgetScopeKind, () => ReactNode> = {
  global: () => Icon.globe(),
  project: () => Icon.folder(),
  device: () => Icon.laptop(),
};

/** `Over by $0.60` and its kin: `{amount}` in a sentence is replaced by the amount in the budget's unit. */
export function fill(text: string, values: Record<string, string>): string {
  return text.replace(/\{(\w+)\}/g, (all, key: string) => values[key] ?? all);
}

function unitSuffix(b: Pick<Budget, 'unit'>): string {
  return b.unit === 'tokens' ? ` ${t('tokens')}` : '';
}

function StatusLine(props: {
  budget: Budget;
  status: BudgetStatus;
  onAllow: () => void;
  onStop: () => void;
  busy: boolean;
}) {
  const { budget: b, status } = props;
  const reset = <span>{fill(t('Resets {when}'), { when: resetText(b) })}</span>;
  switch (status) {
    case 'allowed':
      return (
        <>
          <span className="st1 ok">
            {Icon.check()}
            {fill(t('Allowed until {end}. Pausing applies again from {next}.'), {
              end: endDate(b),
              next: nextDate(b),
            })}
          </span>
          <Button
            variant="quiet"
            disabled={props.busy}
            onClick={props.onStop}
            data-testid="settings.budget.stop-allowing"
          >
            {t('Stop allowing')}
          </Button>
        </>
      );
    case 'paused':
      return (
        <>
          <span className="st1 stop">
            {Icon.pause()}
            {t('Over the limit. New prompts are refused and the queue is paused.')}
          </span>
          <Button
            disabled={props.busy}
            onClick={props.onAllow}
            title={t(
              'Prompts go through until the period ends; the pause applies again next period',
            )}
            data-testid="settings.budget.allow"
          >
            {fill(t('Allow until {end}'), { end: endDate(b) })}
          </Button>
        </>
      );
    case 'over':
      return (
        <>
          <span className="st1 warn">
            {Icon.alert()}
            {fill(t('Over by {amount}. Nothing is paused.'), {
              amount: amountText(b.unit, leftOf(b)),
            })}
          </span>
          {reset}
        </>
      );
    case 'near':
      return (
        <>
          <span className="st1 warn">
            {Icon.alert()}
            {fill(t('Past the alert line. {amount} left.'), {
              amount: `${amountText(b.unit, leftOf(b))}${unitSuffix(b)}`,
            })}
          </span>
          {reset}
        </>
      );
    default:
      return (
        <>
          <span className="st1">
            {fill(t('Alert at {percent}%'), { percent: String(b.alertPercent) })}
          </span>
          {reset}
        </>
      );
  }
}

function BudgetRow(props: {
  budget: Budget;
  busy: boolean;
  onEdit: () => void;
  onAllow: () => void;
  onStop: () => void;
}) {
  const { budget: b } = props;
  const status = statusOf(b);
  const title = `${scopeName(b)} · ${PERIOD_WORD[b.period]()}`;
  const percent = barPercent(b);
  const mode = b.onLimit === 'pause' ? t('Pauses at the limit') : t('Warn only');
  return (
    <div className="bg" data-testid={`settings.budget.${b.id}`}>
      <div className="bgh">
        <span className="bgp">{SCOPE_ICON[b.scope.kind]()}</span>
        <div className="tx">
          <span className="nm">{title}</span>
          <span className="meta">
            {mode}
            {b.unit === 'cost' ? ` · ${t('estimated cost')}` : ''}
          </span>
        </div>
        <span className="bgv">
          {amountText(b.unit, b.spent)}{' '}
          <span className="bgu">
            {t('of')} {amountText(b.unit, b.amount)}
            {unitSuffix(b)}
          </span>
        </span>
        <IconButton
          label={t('Edit budget')}
          icon={Icon.pen()}
          onClick={props.onEdit}
          data-testid="settings.budget.edit"
        />
      </div>
      <div className="bgb">
        <div
          className={`pbar ${barKind(status)}`.trim()}
          role="progressbar"
          aria-valuenow={percent}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-label={title}
        >
          <span className="pf" style={{ width: `${percent}%` }} />
          <span
            className="pt"
            style={{ left: `${b.alertPercent}%` }}
            title={fill(t('Alert line, {percent}%'), { percent: String(b.alertPercent) })}
          />
        </div>
        <span className="pc">{Math.round(b.percent)}%</span>
      </div>
      <div className="bgm">
        <StatusLine
          budget={b}
          status={status}
          busy={props.busy}
          onAllow={props.onAllow}
          onStop={props.onStop}
        />
      </div>
    </div>
  );
}

function Field(props: { label: string; children: ReactNode }) {
  return (
    <div className="field-group">
      <span className="field-label" aria-hidden="true">
        {props.label}
      </span>
      {props.children}
    </div>
  );
}

function BudgetForm(props: {
  draft: BudgetDraft;
  projects: readonly string[];
  devices: readonly Pick<DeviceInfo, 'deviceId' | 'name'>[];
  saving: boolean;
  problem: string | null;
  onChange: (draft: BudgetDraft) => void;
  onClose: () => void;
  onSave: () => void;
  onDelete: () => void;
}) {
  const { draft, devices } = props;
  const bad = problemOf(draft);
  const [touched, setTouched] = useState(false);
  const first = useRef<HTMLDivElement>(null);
  useEffect(() => {
    first.current?.querySelector<HTMLElement>('[role="radio"][aria-checked="true"]')?.focus();
  }, []);
  const set = (change: Partial<BudgetDraft>) => props.onChange({ ...draft, ...change });
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || e.defaultPrevented || e.nativeEvent.isComposing) return;
    e.preventDefault();
    props.onClose();
  };
  const projects =
    props.projects.includes(draft.target) || draft.scope !== 'project' || draft.target === ''
      ? props.projects
      : [...props.projects, draft.target];
  const machines =
    draft.scope !== 'device' ||
    draft.target === '' ||
    devices.some((d) => d.deviceId === draft.target)
      ? devices
      : [...devices, { deviceId: draft.target, name: draft.target }];
  const heading = draft.id === null ? t('Add a budget') : t('Edit budget');
  const shown = touched ? bad : null;
  const tokens = draft.unit === 'tokens';
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      className="mcard"
      role="group"
      aria-label={heading}
      onKeyDown={onKeyDown}
      data-testid="settings.budget.form"
    >
      <div className="mcard-h">
        <b>{heading}</b>
        <IconButton label={t('Close')} hint="Esc" icon={Icon.close()} onClick={props.onClose} />
      </div>
      <div className="fgrid" ref={first}>
        <Field label={t('Scope')}>
          <Segmented<BudgetScopeKind>
            label={t('Scope')}
            value={draft.scope}
            options={(['global', 'project', 'device'] as const).map((k) => ({
              value: k,
              label: SCOPE_LABEL[k](),
            }))}
            onChange={(scope) => set({ scope, target: '' })}
            data-testid="settings.budget.scope"
          />
        </Field>
        {draft.scope === 'project' ? (
          <Field label={t('Project')}>
            <Select
              label={t('Project')}
              value={draft.target}
              placeholder={t('Choose a project')}
              options={projects.map((p) => ({
                value: p,
                label: projectLabel(p),
                leading: Icon.folder(),
                detail: p,
              }))}
              onChange={(target) => set({ target })}
              data-testid="settings.budget.project"
            />
          </Field>
        ) : draft.scope === 'device' ? (
          <Field label={t('Machine')}>
            <Select
              label={t('Machine')}
              value={draft.target}
              placeholder={t('Choose a machine')}
              options={machines.map((d) => ({
                value: d.deviceId,
                label: d.name,
                leading: Icon.laptop(),
              }))}
              onChange={(target) => set({ target })}
              data-testid="settings.budget.machine"
            />
          </Field>
        ) : (
          <div />
        )}
        <Field label={t('Period')}>
          <Segmented
            label={t('Period')}
            value={draft.period}
            options={[
              { value: 'day', label: t('Day') },
              { value: 'week', label: t('Week') },
              { value: 'month', label: t('Month') },
            ]}
            onChange={(period) => set({ period })}
            data-testid="settings.budget.period"
          />
        </Field>
        <Field label={t('Unit')}>
          <Segmented
            label={t('Unit')}
            value={draft.unit}
            options={[
              { value: 'cost', label: t('Estimated cost') },
              { value: 'tokens', label: t('Tokens') },
            ]}
            onChange={(unit) => set({ unit, limit: '' })}
            data-testid="settings.budget.unit"
          />
        </Field>
        <div className="field-group">
          <label className="field-label" htmlFor="budget-limit">
            {t('Limit')}
          </label>
          <span className={`th-in${shown === 'limit' ? ' bad' : ''}`}>
            {tokens ? null : <span className="px">$</span>}
            <TextField
              id="budget-limit"
              label={t('Limit')}
              bare
              inputMode="decimal"
              value={draft.limit}
              aria-invalid={shown === 'limit'}
              onChange={(e) => set({ limit: e.target.value })}
              onBlur={() => setTouched(true)}
              data-testid="settings.budget.limit"
            />
            {tokens ? <span className="sx">{t('M tokens')}</span> : null}
          </span>
        </div>
        <div className="field-group">
          <label className="field-label" htmlFor="budget-alert">
            {t('Alert at')}
          </label>
          <span className={`th-in${shown === 'alert' ? ' bad' : ''}`}>
            <TextField
              id="budget-alert"
              label={t('Alert at')}
              bare
              inputMode="numeric"
              value={draft.alert}
              aria-invalid={shown === 'alert'}
              onChange={(e) => set({ alert: e.target.value })}
              onBlur={() => setTouched(true)}
              data-testid="settings.budget.alert"
            />
            <span className="sx">%</span>
          </span>
        </div>
      </div>
      <div className="field-group" role="radiogroup" aria-label={t('At the limit')}>
        <span className="field-label" aria-hidden="true">
          {t('At the limit')}
        </span>
        <LimitOption
          on={draft.onLimit === 'warn'}
          title={t('Warn only')}
          text={t('Hoplet tells you and keeps going. This is the default.')}
          onSelect={() => set({ onLimit: 'warn' })}
          testId="settings.budget.on-limit.warn"
        />
        <LimitOption
          on={draft.onLimit === 'pause'}
          title={t('Pause')}
          text={t(
            'New prompts are refused and the auto-send queue pauses. A running turn is not interrupted. You can allow it until the end of the period.',
          )}
          onSelect={() => set({ onLimit: 'pause' })}
          testId="settings.budget.on-limit.pause"
        />
      </div>
      <p className="rmeta" style={{ margin: 0 }}>
        {t('At the alert line Hoplet sends a Budget notification, once per period.')}
      </p>
      {shown ? (
        <p className="rmeta bgerr" role="alert" style={{ margin: 0 }}>
          {shown === 'target'
            ? draft.scope === 'project'
              ? t('Choose a project.')
              : t('Choose a machine.')
            : shown === 'limit'
              ? t('Enter a limit above zero.')
              : t('Enter an alert line from 1 to 100.')}
        </p>
      ) : null}
      {props.problem ? (
        <p className="rmeta bgerr" role="alert" style={{ margin: 0 }}>
          {props.problem}
        </p>
      ) : null}
      <div className="mcard-f">
        {draft.id !== null ? (
          <Button
            variant="quiet"
            className="del"
            icon={Icon.trash()}
            disabled={props.saving}
            onClick={props.onDelete}
            data-testid="settings.budget.delete"
          >
            {t('Delete')}
          </Button>
        ) : null}
        <Button variant="quiet" onClick={props.onClose}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={props.saving}
          onClick={() => {
            setTouched(true);
            if (bad === null) props.onSave();
          }}
          data-testid="settings.budget.save"
        >
          {t('Save')}
        </Button>
      </div>
    </div>
  );
}

function LimitOption(props: {
  on: boolean;
  title: string;
  text: string;
  onSelect: () => void;
  testId: string;
}) {
  return (
    // biome-ignore lint/a11y/useSemanticElements: a radio drawn as the design's round mark
    <button
      type="button"
      role="radio"
      aria-checked={props.on}
      className="opt"
      onClick={props.onSelect}
      data-testid={props.testId}
    >
      <span className={`radio${props.on ? ' on' : ''}`} />
      <span className="tx">
        <b>{props.title}</b>
        <span>{props.text}</span>
      </span>
    </button>
  );
}

/** Settings > Usage > Budgets: what each budget has used, and where it can be set, changed and allowed past its limit. */
export function BudgetsTab(props: {
  budgets: BudgetsState;
  fixture?: BudgetsFixture | undefined;
  /** The project paths of the sessions, for the form's project list. */
  projectPaths: readonly string[];
}) {
  const { budgets, fixture } = props;
  const list = budgets.data?.budgets ?? [];
  const devices = useDevices(fixture?.devices);
  const [editing, setEditing] = useState<BudgetDraft | null>(() => {
    if (fixture?.editing === undefined) return null;
    if (fixture.editing === 'new') return newDraft();
    const b = fixture.list.budgets.find((x) => x.id === fixture.editing);
    return b ? draftOf(b) : null;
  });
  const [toast, say] = useToast(fixture?.toast ? { text: fixture.toast } : null);

  if (budgets.loading && !budgets.data) {
    return (
      <p className="rmeta" role="status">
        {t('Loading…')}
      </p>
    );
  }
  if (!budgets.data) {
    return (
      <p className="rmeta" role="alert" data-testid="settings.budgets.error">
        {budgets.error}
      </p>
    );
  }

  const projects = [
    ...new Set([
      ...(fixture?.projects ?? props.projectPaths),
      ...list.flatMap((b) => (b.scope.kind === 'project' ? [b.scope.value] : [])),
    ]),
  ].sort((a, b) => projectLabel(a).localeCompare(projectLabel(b)));

  // The budget being changed is its form, not also a row.
  const rows = list.filter((b) => b.id !== editing?.id);
  const close = () => setEditing(null);
  const save = async () => {
    if (!editing) return;
    const body = toBody(editing, devices);
    if (!body) return;
    if (await budgets.save(editing.id, body)) close();
  };
  const remove = async () => {
    if (editing?.id && (await budgets.remove(editing.id))) close();
  };
  const allow = async (b: Budget) => {
    if (await budgets.allow(b.id)) {
      say(fill(t('{name} is allowed until {end}'), { name: b.name, end: endDate(b) }));
    }
  };

  const form = editing ? (
    <>
      <BudgetForm
        draft={editing}
        projects={projects}
        devices={devices}
        saving={budgets.saving}
        problem={budgets.problem}
        onChange={setEditing}
        onClose={close}
        onSave={() => void save()}
        onDelete={() => void remove()}
      />
      <div className="bgform-gap" />
    </>
  ) : null;

  return (
    <>
      {list.length === 0 && !editing ? (
        <div className="mp-list">
          <div className="uempty" data-testid="settings.budgets.empty">
            <span className="tile">{Icon.activity()}</span>
            <b>{t('No budgets')}</b>
            <p>
              {t(
                'A budget watches one scope over a day, a week or a month, and tells you at the alert line. It can also pause new prompts at the limit.',
              )}
            </p>
            <div className="acts">
              <Button
                variant="primary"
                icon={Icon.plus()}
                onClick={() => setEditing(newDraft())}
                data-testid="settings.budgets.add"
              >
                {t('Add a budget')}
              </Button>
            </div>
          </div>
        </div>
      ) : (
        <>
          {form}
          {rows.length > 0 ? (
            <div className="mp-list" data-testid="settings.budgets.list">
              {rows.map((b) => (
                <BudgetRow
                  key={b.id}
                  budget={b}
                  busy={budgets.saving}
                  onEdit={() => setEditing(draftOf(b))}
                  onAllow={() => void allow(b)}
                  onStop={() => void budgets.stopAllowing(b.id)}
                />
              ))}
            </div>
          ) : null}
          <div className="bgadd">
            <p className="rmeta">
              {t(
                'Counted in estimated cost, a budget leaves out models without a price. Count in tokens to include them.',
              )}
            </p>
            <Button
              icon={Icon.plus()}
              onClick={() => setEditing(newDraft())}
              data-testid="settings.budgets.add"
            >
              {t('Add a budget')}
            </Button>
          </div>
        </>
      )}
      {budgets.problem && !editing ? (
        <p className="rmeta bgerr" role="alert">
          {budgets.problem}
        </p>
      ) : null}
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </>
  );
}
