import type {
  DeviceInfo,
  TokenAllowEntry,
  TokenCreateResponse,
  TokenGrant,
  TokenInfo,
  TokenListResponse,
} from '@hoplet/shared';
import { MAX_ACTIVE_TOKENS, MAX_TOKEN_ALLOW, MAX_TOKEN_DAYS } from '@hoplet/shared';
import { type ReactNode, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { fill } from '../../components/fill.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import { copyText } from '../../lib/clipboard.ts';
import { t } from '../../lib/i18n.ts';
import {
  answersOf,
  baseName,
  capabilityText,
  checkForm,
  dateText,
  daysOf,
  dayText,
  EXPIRY_CHOICES,
  type ExpiryChoice,
  emptyForm,
  expiringSoon,
  expiryOf,
  holdsMachine,
  holdsPartOf,
  holdsProject,
  MAX_TOKEN_NAME,
  PRESETS,
  type Preset,
  presetHint,
  presetName,
  presetOf,
  relativeText,
  removeEntry,
  type TokenForm,
  toggleMachine,
  toggleProject,
  toRequest,
} from '../../lib/tokens.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { type TokensState, useTokens } from '../../lib/use-tokens.ts';
import { useProjectChoices } from './rules.tsx';
import './tokens.css';

/** What the development fixtures hand to Settings > API tokens. */
export type TokensFixture = {
  list: TokenListResponse;
  devices: DeviceInfo[];
  /** The directories each machine offers, by device ID. */
  projects: Record<string, string[]>;
  /** The moment the screen counts times from. */
  now: number;
  /** The address the usage examples name. */
  origin?: string;
  /** The ID of the token whose detail starts open. */
  open?: string;
  /** The form that starts open. */
  form?: TokenForm;
  /** The machine and project list of the form starts open. */
  picker?: boolean;
  /** The form has been tried with what it holds. */
  tried?: boolean;
  /** The token whose one-time reveal starts open; its secret is a placeholder. */
  issued?: TokenCreateResponse;
  /** The "Copy" button has been pressed. */
  copied?: boolean;
  /** The ID of the token whose revoke confirmation starts open. */
  confirm?: string;
  /** A refusal the form starts with. */
  problem?: string;
  loading?: boolean;
  error?: string;
};

const mono = (s: string) => <span className="mono">{s}</span>;

// -- Where a token reaches -------------------------------------------------------------------------------------------

type Place = { machine: string; paths: string[] | null };

function machineName(devices: readonly DeviceInfo[], deviceId: string): string {
  return devices.find((d) => d.deviceId === deviceId)?.name ?? deviceId;
}

/** The allow-list as one place per machine: its projects, or null for the whole machine. */
function placesOf(allow: readonly TokenAllowEntry[], devices: readonly DeviceInfo[]): Place[] {
  const places: Place[] = [];
  for (const entry of allow) {
    const machine = machineName(devices, entry.deviceId);
    const at = places.find((p) => p.machine === machine);
    if (entry.path === undefined) {
      if (at) at.paths = null;
      else places.push({ machine, paths: null });
    } else if (at) {
      if (at.paths) at.paths.push(baseName(entry.path));
    } else {
      places.push({ machine, paths: [baseName(entry.path)] });
    }
  }
  return places;
}

function PlaceView(props: { place: Place }) {
  const { place } = props;
  return (
    <>
      {mono(place.machine)}
      {place.paths ? (
        <>
          {' › '}
          {place.paths.map((p, i) => (
            <span key={p}>
              {i > 0 ? ', ' : ''}
              {mono(p)}
            </span>
          ))}
        </>
      ) : (
        <> · {t('every project')}</>
      )}
    </>
  );
}

function placeText(place: Place): string {
  return place.paths ? `${place.machine} › ${place.paths.join(', ')}` : place.machine;
}

function scopeText(grant: TokenGrant, devices: readonly DeviceInfo[]): string {
  return grant.allow
    ? placesOf(grant.allow, devices).map(placeText).join(' · ')
    : t('All machines');
}

function Scope(props: { grant: TokenGrant; devices: readonly DeviceInfo[] }) {
  const { grant, devices } = props;
  if (!grant.allow) return <>{t('All machines')}</>;
  return (
    <>
      {placesOf(grant.allow, devices).map((p, i) => (
        <span key={p.machine}>
          {i > 0 ? ' · ' : ''}
          <PlaceView place={p} />
        </span>
      ))}
    </>
  );
}

// -- A row -----------------------------------------------------------------------------------------------------------

function AbilityTags(props: { grant: TokenGrant }) {
  return (
    <>
      <span className="tag">{presetName(presetOf(props.grant))}</span>
      {answersOf(props.grant) ? (
        <span className="tag att">
          {Icon.alert()}
          {t('answers permissions')}
        </span>
      ) : null}
    </>
  );
}

function StateTag(props: { token: TokenInfo }) {
  if (props.token.state === 'revoked') return <span className="tag bad">{t('Revoked')}</span>;
  if (props.token.state === 'expired') return <span className="tag bad">{t('Expired')}</span>;
  return null;
}

function usedText(token: TokenInfo, now: number): string {
  return token.lastUsedAt === null
    ? t('Never used')
    : t('Last used {when}').replace('{when}', relativeText(token.lastUsedAt, now));
}

function When(props: { token: TokenInfo; now: number }) {
  const { token, now } = props;
  const used = usedText(token, now);
  if (token.state === 'revoked') {
    return (
      <>
        <b>
          {t('Revoked {when}').replace(
            '{when}',
            relativeText(token.revokedAt ?? token.expiresAt, now),
          )}
        </b>
        {used}
      </>
    );
  }
  if (token.state === 'expired') {
    return (
      <>
        <b>{t('Expired {when}').replace('{when}', relativeText(token.expiresAt, now))}</b>
        {used}
      </>
    );
  }
  return (
    <>
      <b className={expiringSoon(token, now) ? 'warn' : undefined}>
        {t('Expires {when}').replace('{when}', relativeText(token.expiresAt, now))}
      </b>
      {used}
    </>
  );
}

function Detail(props: {
  token: TokenInfo;
  devices: readonly DeviceInfo[];
  now: number;
  onRevoke: () => void;
}) {
  const { token, devices, now } = props;
  return (
    <div className="tkd" data-testid={`settings.tokens.detail.${token.id}`}>
      <div className="hkv">
        <span className="k">{t('Abilities')}</span>
        <span className="v">
          {token.grant.capabilities.map((c) => (
            <span className="l2" key={c}>
              {mono(c)} · {capabilityText(c)}
            </span>
          ))}
        </span>
        <span className="k">{t('Machines and projects')}</span>
        <span className="v">
          {token.grant.allow ? (
            placesOf(token.grant.allow, devices).map((p) => (
              <span className="l2" key={p.machine}>
                <PlaceView place={p} />
              </span>
            ))
          ) : (
            <span className="l2">{t('All machines, including ones added later')}</span>
          )}
        </span>
        <span className="k">{t('Created')}</span>
        <span className="v">{dateText(token.createdAt)}</span>
        <span className="k">{t('Expires')}</span>
        <span className="v">{dateText(token.expiresAt)}</span>
        <span className="k">{t('Last used')}</span>
        <span className="v">
          {token.lastUsedAt === null ? t('Never used') : relativeText(token.lastUsedAt, now)}
        </span>
        <span className="k">{t('Id')}</span>
        <span className="v">{mono(token.id)}</span>
      </div>
      {token.state === 'active' ? (
        <div className="hacts">
          <Button
            variant="quiet"
            icon={Icon.ban()}
            className="del"
            onClick={props.onRevoke}
            data-testid={`settings.tokens.revoke.${token.id}`}
          >
            {t('Revoke…')}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function Row(props: {
  token: TokenInfo;
  devices: readonly DeviceInfo[];
  now: number;
  open: boolean;
  onToggle: () => void;
  onRevoke: () => void;
}) {
  const { token, devices, now, open } = props;
  const id = `tk-${token.id}`;
  return (
    <div className="tkw" data-testid={`settings.tokens.row.${token.id}`}>
      <div className={`tkr${token.state === 'active' ? '' : ' dim'}${open ? ' open' : ''}`}>
        <span className="lead">{Icon.key()}</span>
        <div className="tkm">
          <div className="nm">
            <span className="n">{token.name}</span>
            <StateTag token={token} />
          </div>
          <div className="sub">
            <AbilityTags grant={token.grant} />
            <span>
              <Scope grant={token.grant} devices={devices} />
            </span>
          </div>
        </div>
        <div className="when">
          <When token={token} now={now} />
        </div>
        <IconButton
          label={open ? t('Collapse') : t('Details')}
          icon={open ? Icon.down() : Icon.chevron()}
          aria-expanded={open}
          aria-controls={id}
          onClick={props.onToggle}
          data-testid={`settings.tokens.toggle.${token.id}`}
        />
      </div>
      <div id={id}>
        {open ? (
          <Detail token={token} devices={devices} now={now} onRevoke={props.onRevoke} />
        ) : null}
      </div>
    </div>
  );
}

// -- The form --------------------------------------------------------------------------------------------------------

function Field(props: { label: ReactNode; hint?: ReactNode; bad?: boolean; children: ReactNode }) {
  return (
    <div className="fld">
      <span className="l">{props.label}</span>
      {props.children}
      {props.hint ? <span className={`h${props.bad ? ' bad' : ''}`}>{props.hint}</span> : null}
    </div>
  );
}

const EXPIRY_LABEL: Record<ExpiryChoice, () => string> = {
  '1': () => t('1 day'),
  '7': () => t('7 days'),
  '30': () => t('30 days'),
  '90': () => t('90 days'),
  custom: () => t('Custom'),
};

function Check(props: { on: boolean; part?: boolean }) {
  return (
    <span className={`cbx${props.part ? ' part' : props.on ? ' on' : ''}`} aria-hidden="true">
      {props.on && !props.part ? Icon.check() : null}
    </span>
  );
}

function Picker(props: {
  devices: readonly DeviceInfo[];
  projects: readonly { deviceId: string; path: string }[];
  allow: TokenAllowEntry[];
  onChange: (allow: TokenAllowEntry[]) => void;
}) {
  const { devices, projects, allow } = props;
  if (devices.length === 0) {
    return <span className="tkdesc">{t('No machines yet.')}</span>;
  }
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of check rows, as in the mockup
    <div
      className="tkscope"
      role="group"
      aria-label={t('Machines and projects')}
      data-testid="settings.tokens.picker"
    >
      {devices.map((d) => {
        const own = projects.filter((p) => p.deviceId === d.deviceId).map((p) => p.path);
        const whole = holdsMachine(allow, d.deviceId);
        const part = holdsPartOf(allow, d.deviceId);
        const chosen = own.filter((p) => holdsProject(allow, d.deviceId, p)).length;
        return (
          <div key={d.deviceId} className="tkgrp">
            {/* biome-ignore lint/a11y/useSemanticElements: a custom check box, as in the mockup */}
            <button
              type="button"
              role="checkbox"
              aria-checked={part ? 'mixed' : whole}
              className="sr h"
              onClick={() => props.onChange(toggleMachine(allow, d.deviceId))}
              data-testid={`settings.tokens.pick.${d.deviceId}`}
            >
              <Check on={whole || part} part={part} />
              {Icon.laptop()}
              {mono(d.name)}
              <span className="sp" />
              <span className="meta">
                {whole
                  ? t('Whole machine')
                  : part
                    ? t('{n} of {total} projects')
                        .replace('{n}', String(chosen))
                        .replace('{total}', String(own.length))
                    : t('Not included')}
              </span>
            </button>
            {own.map((path) => {
              const on = holdsProject(allow, d.deviceId, path);
              return (
                // biome-ignore lint/a11y/useSemanticElements: a custom check box, as in the mockup
                <button
                  type="button"
                  role="checkbox"
                  aria-checked={on}
                  className="sr"
                  key={path}
                  onClick={() => props.onChange(toggleProject(allow, d.deviceId, path, own))}
                  data-testid={`settings.tokens.pick.${d.deviceId}.${path}`}
                >
                  <Check on={on} />
                  {Icon.folder()}
                  {mono(baseName(path))}
                </button>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}

function ScopeField(props: {
  form: TokenForm;
  devices: readonly DeviceInfo[];
  projects: readonly { deviceId: string; path: string }[];
  picking: boolean;
  bad: boolean;
  set: (change: Partial<TokenForm>) => void;
  onPicking: (picking: boolean) => void;
}) {
  const { form, devices, bad } = props;
  const all = form.scope === 'all';
  return (
    <Field label={t('Machines and projects')}>
      <Segmented
        label={t('Machines and projects')}
        value={form.scope}
        options={[
          { value: 'all', label: t('All machines') },
          { value: 'only', label: t('Only these') },
        ]}
        onChange={(scope) => props.set({ scope })}
        data-testid="settings.tokens.scope"
      />
      {all ? (
        <span className="tkdesc">
          {t('Every machine, including ones added later, and every project on them.')}
        </span>
      ) : (
        <>
          <div className="tkchips">
            {form.allow.map((e) => {
              const name = `${machineName(devices, e.deviceId)}${e.path === undefined ? '' : ` › ${baseName(e.path)}`}`;
              return (
                <span className="tag" key={`${e.deviceId}\u0000${e.path ?? ''}`}>
                  {name}
                  {e.path === undefined ? <span className="meta">{t('every project')}</span> : null}
                  <button
                    type="button"
                    className="tkx"
                    aria-label={t('Remove {name}').replace('{name}', name)}
                    onClick={() => props.set({ allow: removeEntry(form.allow, e) })}
                  >
                    {Icon.close()}
                  </button>
                </span>
              );
            })}
            <Button
              variant="quiet"
              icon={Icon.plus()}
              aria-expanded={props.picking}
              onClick={() => props.onPicking(!props.picking)}
              data-testid="settings.tokens.add-place"
            >
              {t('Add machine or project')}
            </Button>
          </div>
          {props.picking ? (
            <Picker
              devices={devices}
              projects={props.projects}
              allow={form.allow}
              onChange={(allow) => props.set({ allow })}
            />
          ) : null}
          <span className={`tkdesc${bad ? ' bad' : ''}`} role={bad ? 'alert' : undefined}>
            {bad
              ? t('Choose 1 to {n} machines or projects.').replace('{n}', String(MAX_TOKEN_ALLOW))
              : t(
                  'Anything outside the list does not exist for this token. Machines added later are not included.',
                )}
          </span>
        </>
      )}
    </Field>
  );
}

function IssueForm(props: {
  initial: TokenForm;
  initialPicking: boolean;
  initialTried: boolean;
  devices: readonly DeviceInfo[];
  projects: readonly { deviceId: string; path: string }[];
  now: number;
  active: number;
  state: TokensState;
  problem: string | null;
  onIssued: (issued: TokenCreateResponse) => void;
  onClose: () => void;
}) {
  const { devices, now, state } = props;
  const [form, setForm] = useState(props.initial);
  const [picking, setPicking] = useState(props.initialPicking);
  const [tried, setTried] = useState(props.initialTried);
  const set = (change: Partial<TokenForm>) => setForm((f) => ({ ...f, ...change }));
  const check = checkForm(form);
  const shown = tried ? check.errors : {};
  const days = daysOf(form);
  const submit = async () => {
    if (state.busy) return;
    const body = toRequest(form);
    if (!body) {
      setTried(true);
      return;
    }
    const made = await state.issue(body);
    if (made) props.onIssued(made);
  };
  const title = t('Issue a token');
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      className="mcard tkpage-card"
      role="group"
      aria-label={title}
      data-testid="settings.tokens.form"
      onKeyDown={(e) => {
        if (e.key === 'Escape' && !e.defaultPrevented && !e.nativeEvent.isComposing) {
          e.preventDefault();
          e.stopPropagation();
          props.onClose();
        }
      }}
    >
      <div className="mcard-h">
        <b>{title}</b>
        <IconButton label={t('Close (Esc)')} icon={Icon.close()} onClick={props.onClose} />
      </div>
      <div className="tkf">
        {props.problem ? (
          <div className="tkerr" role="alert" data-testid="settings.tokens.problem">
            {Icon.alert()}
            <span>{props.problem}</span>
          </div>
        ) : null}
        <div className="two">
          <Field
            label={t('Name')}
            bad={shown.name === true}
            hint={
              shown.name
                ? t('Give the token a name of up to {n} characters.').replace(
                    '{n}',
                    String(MAX_TOKEN_NAME),
                  )
                : t('Shown in the list and in the audit log.')
            }
          >
            <TextField
              label={t('Name')}
              value={form.name}
              maxLength={MAX_TOKEN_NAME}
              placeholder={t('For example, CI release bot')}
              aria-invalid={shown.name === true}
              onChange={(e) => set({ name: e.target.value })}
              data-testid="settings.tokens.name"
            />
          </Field>
          <Field
            label={t('Expires')}
            bad={shown.days === true}
            hint={
              days === null
                ? t('Enter a whole number of days, 1 to {n}.').replace(
                    '{n}',
                    String(MAX_TOKEN_DAYS),
                  )
                : t('Expires {date}. Every token expires; the longest is {n} days.')
                    .replace('{date}', dayText(expiryOf(days, now)))
                    .replace('{n}', String(MAX_TOKEN_DAYS))
            }
          >
            <Segmented
              label={t('Expires')}
              value={form.expiry}
              options={EXPIRY_CHOICES.map((value) => ({ value, label: EXPIRY_LABEL[value]() }))}
              onChange={(expiry) => set({ expiry })}
              data-testid="settings.tokens.expiry"
            />
            {form.expiry === 'custom' ? (
              <TextField
                label={t('Days until it expires')}
                inputMode="numeric"
                value={form.customDays}
                placeholder={t('Days')}
                aria-invalid={shown.days === true}
                onChange={(e) => set({ customDays: e.target.value })}
                data-testid="settings.tokens.days"
              />
            ) : null}
          </Field>
        </div>
        <Field label={t('Access')} hint={presetHint(form.preset)}>
          <Segmented<Preset>
            label={t('Access')}
            value={form.preset}
            options={PRESETS.map((value) => ({ value, label: presetName(value) }))}
            onChange={(preset) => set({ preset })}
            data-testid="settings.tokens.preset"
          />
        </Field>
        <div className={`tkans${form.answer ? ' on' : ''}`}>
          <Switch
            label={t('May answer permission requests')}
            hideLabel
            checked={form.answer}
            onChange={(answer) => set({ answer })}
            data-testid="settings.tokens.answer"
          />
          <div className="tx">
            <b>{t('May answer permission requests')}</b>
            <span className="tkdesc">
              {t('Off by default. Answering decides what a tool may do on that machine.')}
            </span>
          </div>
        </div>
        {form.answer ? (
          <div className="hwarn" role="note" data-testid="settings.tokens.answer-warning">
            {Icon.alert()}
            <span>
              {form.preset === 'create'
                ? fill(
                    t(
                      'With {answer} and {create}, this token can approve and run tool calls on every machine it can reach. It still cannot choose Bypass permissions. Limit it to the machines and projects below.',
                    ),
                    { answer: <b>{t('answer')}</b>, create: <b>{t('create')}</b> },
                  )
                : fill(
                    t(
                      'With {answer}, this token can approve the tool calls the sessions it can reach are waiting on. Limit it to the machines and projects below.',
                    ),
                    { answer: <b>{t('answer')}</b> },
                  )}
            </span>
          </div>
        ) : null}
        <ScopeField
          form={form}
          devices={devices}
          projects={props.projects}
          picking={picking}
          bad={shown.allow === true}
          set={set}
          onPicking={setPicking}
        />
      </div>
      <div className="mcard-f">
        <span className="l">
          {t('{n} of {max} tokens active')
            .replace('{n}', String(props.active))
            .replace('{max}', String(MAX_ACTIVE_TOKENS))}
        </span>
        <Button variant="quiet" onClick={props.onClose}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={state.busy}
          onClick={() => void submit()}
          data-testid="settings.tokens.submit"
        >
          {state.busy ? t('Issuing…') : t('Issue token')}
        </Button>
      </div>
    </div>
  );
}

// -- The one-time reveal ---------------------------------------------------------------------------------------------

function Code(props: { lines: string[] }) {
  return (
    <div className="tkcode">
      {props.lines.map((line) => (
        <span key={line} className={`mono${line.startsWith('#') ? ' c' : ''}`}>
          {line}
        </span>
      ))}
    </div>
  );
}

/**
 * The token just issued, shown once. Its secret lives in this component's props and nowhere else; Done lets the
 * parent drop it.
 */
function Reveal(props: {
  issued: TokenCreateResponse;
  devices: readonly DeviceInfo[];
  origin: string;
  copied: boolean;
  onCopy: () => void;
  onDone: () => void;
}) {
  const { issued, devices } = props;
  const secret = issued.token;
  const title = t('Token issued · {name}').replace('{name}', issued.name);
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div className="mcard" role="group" aria-label={title} data-testid="settings.tokens.reveal">
      <div className="mcard-h">
        <b>{title}</b>
      </div>
      <div className="tkf">
        <div className="hwarn" role="note">
          {Icon.alert()}
          <span>
            {t(
              'Copy this token now. Hoplet keeps only a hash of it and cannot show it again. If it is lost, revoke it and issue a new one.',
            )}
          </span>
        </div>
        <Field label={t('Token')}>
          <div className="hsec">
            <span className="mono" data-testid="settings.tokens.secret">
              {secret}
            </span>
            <Button
              icon={props.copied ? Icon.check() : Icon.copy()}
              onClick={props.onCopy}
              data-testid="settings.tokens.copy"
            >
              {props.copied ? t('Copied') : t('Copy')}
            </Button>
          </div>
        </Field>
        <div className="tkhow">
          <span className="k">{t('Use it from the hoplet CLI or a script')}</span>
          <Field label={t('Environment variable')}>
            <Code lines={[`export HOPLET_URL=${props.origin}`, `export HOPLET_TOKEN=${secret}`]} />
          </Field>
          <Field
            label={t('Token file, which keeps it out of the shell history and the process list')}
          >
            <Code
              lines={[
                'umask 077; pbpaste > ~/.config/hoplet/token',
                'export HOPLET_TOKEN_FILE=~/.config/hoplet/token',
                '# then: hoplet --help',
              ]}
            />
          </Field>
        </div>
        <span className="meta tkcap">
          {[
            presetName(presetOf(issued.grant)) +
              (answersOf(issued.grant) ? ` + ${t('answers permissions')}` : ''),
            scopeText(issued.grant, devices),
            t('expires {date}').replace('{date}', dateText(issued.expiresAt)),
          ].join(' · ')}
        </span>
      </div>
      <div className="mcard-f">
        <Button variant="primary" onClick={props.onDone} data-testid="settings.tokens.done">
          {t('Done')}
        </Button>
      </div>
    </div>
  );
}

// -- The revoke confirmation -----------------------------------------------------------------------------------------

function ConfirmRevoke(props: {
  token: TokenInfo;
  now: number;
  busy: boolean;
  onKeep: () => void;
  onRevoke: () => void;
}) {
  const { token } = props;
  return (
    <Dialog
      label={t('Revoke a token')}
      onClose={props.onKeep}
      className="dlg tkcf"
      data-testid="settings.tokens.confirm"
    >
      <div className="dlg-h">{t('Revoke “{name}”?').replace('{name}', token.name)}</div>
      <div className="dlg-b">
        <p className="dlg-p">
          {fill(
            t(
              'Anything that uses this token stops working at once, and the connections it has open are closed. {strong}; issue a new token to replace it.',
            ),
            { strong: <b>{t('This cannot be undone')}</b> },
          )}
        </p>
        <span className="meta">{usedText(token, props.now)}</span>
      </div>
      <div className="dlg-f">
        <Button variant="quiet" onClick={props.onKeep} data-autofocus>
          {t('Cancel')}
        </Button>
        <Button
          variant="danger"
          icon={Icon.ban()}
          disabled={props.busy}
          onClick={props.onRevoke}
          data-testid="settings.tokens.confirm-revoke"
        >
          {t('Revoke token')}
        </Button>
      </div>
    </Dialog>
  );
}

// -- The section -----------------------------------------------------------------------------------------------------

export function TokensSection(props: { fixture?: TokensFixture | undefined }) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const state = useTokens(fixture?.list);
  const projects = useProjectChoices(devices, fixture?.projects);
  const [toast, say] = useToast(null);
  const [form, setForm] = useState<TokenForm | null>(fixture?.form ?? null);
  const [issued, setIssued] = useState<TokenCreateResponse | null>(fixture?.issued ?? null);
  const [copied, setCopied] = useState(fixture?.copied ?? false);
  const [open, setOpen] = useState<string | null>(fixture?.open ?? null);
  const [confirm, setConfirm] = useState<string | null>(fixture?.confirm ?? null);
  const now = fixture?.now ?? Date.now();
  const origin = fixture?.origin ?? window.location.origin;

  if (state.loading || fixture?.loading) {
    return (
      <div className="tkpage">
        <h1>{t('API tokens')}</h1>
        <p className="rmeta" role="status" data-testid="settings.tokens.loading">
          {t('Loading…')}
        </p>
      </div>
    );
  }
  const failure = fixture?.error ?? state.error;
  if (failure || !state.data) {
    return (
      <div className="tkpage">
        <h1>{t('API tokens')}</h1>
        <p className="rmeta" role="alert" data-testid="settings.tokens.load-error">
          {failure ?? t('Loading…')}
        </p>
      </div>
    );
  }

  const tokens = state.data.tokens;
  const active = tokens.filter((k) => k.state === 'active');
  const gone = tokens.filter((k) => k.state !== 'active');
  const confirming = confirm ? tokens.find((k) => k.id === confirm) : undefined;
  const editing = form !== null || issued !== null;

  const copy = () => {
    if (!issued) return;
    void copyText(issued.token).then((ok) => {
      setCopied(ok);
      if (!ok) say(t('Could not copy. Select the token and copy it by hand.'), true);
    });
  };
  const rows = (list: TokenInfo[]) => (
    <div className="mp-list">
      {list.map((token) => (
        <Row
          key={token.id}
          token={token}
          devices={devices}
          now={now}
          open={open === token.id}
          onToggle={() => setOpen((o) => (o === token.id ? null : token.id))}
          onRevoke={() => setConfirm(token.id)}
        />
      ))}
    </div>
  );
  const openForm = () => {
    state.clearProblem();
    setForm(emptyForm());
  };
  const issueButton = (
    <Button
      variant="primary"
      icon={Icon.plus()}
      disabled={editing}
      onClick={openForm}
      data-testid="settings.tokens.issue"
    >
      {t('Issue token')}
    </Button>
  );

  return (
    <div className="tkpage" data-testid="settings.tokens">
      <h1>{t('API tokens')}</h1>
      {editing ? null : (
        <p className="tk-lead">
          {t(
            'Tokens let a script, a shortcut or another tool use Hoplet without the host token. Each one has the abilities, machines and projects you choose, and it expires.',
          )}
        </p>
      )}
      {tokens.length > 0 ? <div className="tk-bar tk-top">{issueButton}</div> : null}
      {form ? (
        <IssueForm
          initial={form}
          initialPicking={fixture?.picker ?? false}
          initialTried={fixture?.tried ?? false}
          devices={devices}
          projects={projects}
          now={now}
          active={active.length}
          state={state}
          problem={fixture?.problem ?? state.problem}
          onIssued={(made) => {
            setForm(null);
            setCopied(false);
            setIssued(made);
          }}
          onClose={() => {
            state.clearProblem();
            setForm(null);
          }}
        />
      ) : null}
      {issued ? (
        <Reveal
          issued={issued}
          devices={devices}
          origin={origin}
          copied={copied}
          onCopy={copy}
          onDone={() => {
            setIssued(null);
            setCopied(false);
            setOpen(issued.id);
          }}
        />
      ) : null}
      {tokens.length === 0 && !editing ? (
        <div className="mp-list">
          <div className="uempty" data-testid="settings.tokens.empty">
            <span className="tile">{Icon.key()}</span>
            <b>{t('No API tokens')}</b>
            <p>
              {t(
                'Issue a token to let a script, a shortcut or another tool read sessions, send prompts or start work, without the host token. It sees only the machines and projects you choose.',
              )}
            </p>
            <div className="acts">{issueButton}</div>
          </div>
        </div>
      ) : null}
      {active.length > 0 ? (
        <>
          <div className="tk-bar">
            <span className="lb">{t('Active')}</span>
            <span className="meta">
              {t('{n} of {max}')
                .replace('{n}', String(active.length))
                .replace('{max}', String(MAX_ACTIVE_TOKENS))}
            </span>
          </div>
          {rows(active)}
        </>
      ) : null}
      {gone.length > 0 ? (
        <>
          <div className="tk-bar tk-gone">
            <span className="lb">{t('Revoked and expired')}</span>
          </div>
          {rows(gone)}
        </>
      ) : null}
      {confirming ? (
        <ConfirmRevoke
          token={confirming}
          now={now}
          busy={state.busy}
          onKeep={() => setConfirm(null)}
          onRevoke={() => {
            void state.revoke(confirming.id).then((ok) => {
              setConfirm(null);
              say(ok ? t('Token revoked') : t('The change was not saved'), !ok);
            });
          }}
        />
      ) : null}
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </div>
  );
}
