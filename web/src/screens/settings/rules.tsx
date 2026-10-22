import type {
  DeviceInfo,
  LocalPermissionRule,
  PermissionRuleEntry,
  PermissionRuleListResponse,
  PermissionRuleMachinesResponse,
} from '@hoplet/shared';
import { MAX_PERMISSION_NOTE_CHARS } from '@hoplet/shared';
import { type ReactNode, useEffect, useMemo, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { fill } from '../../components/fill.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import {
  ActionBadge,
  actionIcon,
  actionName,
  ChipInput,
  KindMark,
  kindIcon,
  kindName,
  MatchView,
} from '../../components/rule-parts.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { Select } from '../../components/Select.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import { fetchDeviceProjects } from '../../lib/api.ts';
import { t } from '../../lib/i18n.ts';
import {
  builtinOn,
  checkForm,
  emptyForm,
  formOfRule,
  groupRules,
  matchParts,
  RULE_KINDS,
  type RuleForm,
  type RuleKind,
  ruleName,
  summaryOf,
  toBody,
} from '../../lib/permission-rules.ts';
import { systemOf } from '../../lib/system.ts';
import { baseName, fill as fillText } from '../../lib/templates.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { type PermissionRulesState, usePermissionRules } from '../../lib/use-permission-rules.ts';
import { RuleMachines } from './rule-machines.tsx';
import '../../components/rules.css';

/** What the development fixtures hand to Settings > Permission rules. */
export type RulesFixture = {
  list: PermissionRuleListResponse;
  devices: DeviceInfo[];
  /** The directories each machine offers, by device ID. */
  projects: Record<string, string[]>;
  /** The form that starts open in the editor. */
  form?: RuleForm;
  /** The built-in group starts open. */
  builtinOpen?: boolean;
  /** The ID of the built-in rule whose "turn off" confirmation starts open. */
  confirm?: string;
  /** The ID of the rule being edited, to mark its row. */
  toast?: string;
  /** The machines panel's answer. */
  machines?: PermissionRuleMachinesResponse;
};

const mono = (s: string) => <span className="mono">{s}</span>;

/** The projects of every machine, as the choices of a project rule: the fixture's, else what the agents list. */
export function useProjectChoices(
  devices: readonly DeviceInfo[],
  fixture?: Record<string, string[]>,
): { deviceId: string; path: string }[] {
  const [fetched, setFetched] = useState<Record<string, string[]>>({});
  const key = devices.map((d) => d.deviceId).join('\u0000');
  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` stands for the list of machines
  useEffect(() => {
    if (fixture) return;
    let live = true;
    for (const d of devices) {
      fetchDeviceProjects(d.deviceId)
        .then((r) => {
          if (live)
            setFetched((f) => ({ ...f, [d.deviceId]: r ? r.projects.map((p) => p.path) : [] }));
        })
        .catch(() => undefined);
    }
    return () => {
      live = false;
    };
  }, [key, fixture]);
  const source = fixture ?? fetched;
  return devices.flatMap((d) =>
    (source[d.deviceId] ?? []).map((path) => ({ deviceId: d.deviceId, path })),
  );
}

const pairValue = (deviceId: string, path: string) => `${deviceId}\u0000${path}`;

function machineOf(devices: readonly DeviceInfo[], deviceId: string): string {
  return devices.find((d) => d.deviceId === deviceId)?.name ?? deviceId;
}

// -- A row ----------------------------------------------------------------------------------------------------------

function RuleRow(props: {
  rule: PermissionRuleEntry;
  selected: boolean;
  busy: boolean;
  onToggle: (enabled: boolean) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { rule, selected, busy } = props;
  const editable = formOfRule(rule) !== null;
  const name = ruleName(rule);
  const shown = rule.note.trim() ? (rule.builtin ? t(rule.note.trim()) : rule.note.trim()) : '';
  return (
    <div
      className={`qrow${rule.enabled ? '' : ' off'}${selected ? ' sel' : ''}`}
      data-testid={`settings.rules.row.${rule.id}`}
    >
      <ActionBadge action={rule.action} />
      <div className="qx">
        <div className="qm">
          <KindMark tool={rule.tool} />
          <MatchView rule={rule} />
        </div>
        {shown ? <div className="qn">{shown}</div> : null}
      </div>
      <div className="qt">
        {rule.builtin ? (
          <span className="tag" title={t('Built in. It can be turned off, not edited or deleted.')}>
            {Icon.lock()}
            {t('Built in')}
          </span>
        ) : null}
        <Switch
          label={name}
          hideLabel
          checked={rule.enabled}
          disabled={busy}
          onChange={props.onToggle}
          data-testid={`settings.rules.switch.${rule.id}`}
        />
        {rule.builtin ? null : editable ? (
          <IconButton
            label={t('Edit rule')}
            icon={Icon.pen()}
            onClick={props.onEdit}
            data-testid={`settings.rules.edit.${rule.id}`}
          />
        ) : (
          <IconButton
            label={t('Delete rule')}
            icon={Icon.trash()}
            disabled={busy}
            onClick={props.onDelete}
            data-testid={`settings.rules.delete.${rule.id}`}
          />
        )}
      </div>
    </div>
  );
}

function LocalRow(props: { rule: LocalPermissionRule }) {
  const { rule } = props;
  return (
    <div className="qrow">
      <ActionBadge action={rule.action} />
      <div className="qx">
        <div className="qm">
          <KindMark tool={rule.tool} />
          <MatchView rule={rule} />
        </div>
        {rule.note.trim() ? <div className="qn">{rule.note.trim()}</div> : null}
      </div>
      <div className="qt">
        <span className="qlock" title={t('Set in the agent’s configuration on this machine')}>
          {Icon.lock()}
          {t('Read-only')}
        </span>
      </div>
    </div>
  );
}

function Group(props: {
  icon: ReactNode;
  title: string;
  count: string;
  note?: ReactNode;
  first?: boolean;
  open?: boolean;
  onToggle?: () => void;
  testid?: string;
}) {
  return (
    <div className={`qg${props.first ? ' first' : ''}`} data-testid={props.testid}>
      {props.open === undefined ? null : (
        <IconButton
          label={props.open ? t('Collapse') : t('Expand')}
          icon={props.open ? Icon.down() : Icon.chevron()}
          onClick={props.onToggle ?? (() => undefined)}
          aria-expanded={props.open}
        />
      )}
      <span className="qk">{props.icon}</span>
      <b>{props.title}</b>
      <span className="meta">{props.count}</span>
      {props.note ? <span className="qgr">{props.note}</span> : null}
    </div>
  );
}

// -- The editor -----------------------------------------------------------------------------------------------------

function Field(props: { label: ReactNode; hint?: ReactNode; children: ReactNode; full?: boolean }) {
  return (
    <div className={`fld${props.full ? ' full' : ''}`}>
      <span className="l">{props.label}</span>
      {props.children}
      {props.hint ? <span className="h">{props.hint}</span> : null}
    </div>
  );
}

const optional = () => <span className="meta">{t('optional')}</span>;

function kindHint(kind: RuleKind): string {
  switch (kind) {
    case 'run':
      return t('Shell commands, matched by their words and never as plain text.');
    case 'read':
      return t('Reading, searching and listing files.');
    case 'write':
      return t('Creating or changing files.');
    case 'fetch':
      return t('Web fetch and web search.');
    case 'mcp':
      return t('One tool of an MCP server.');
    case 'any':
      return '';
  }
}

function actionHint(form: RuleForm): string {
  if (form.action === 'ask') {
    return t('Always asks, in every permission mode, Bypass permissions included.');
  }
  if (form.action === 'deny') {
    return t(
      'Refuses the call and tells the session which rule did. A deny wins over every allow and ask.',
    );
  }
  if (form.kind === 'run') {
    return t(
      'Runs without asking. A command with a pipe, &&, a redirection or $( ) is never allowed by a rule: it asks each time.',
    );
  }
  return t('Runs without asking.');
}

function MatchFields(props: { form: RuleForm; set: (change: Partial<RuleForm>) => void }) {
  const { form, set } = props;
  const check = checkForm(form);
  switch (form.kind) {
    case 'run':
      return (
        <>
          <Field
            label={t('Command')}
            hint={fill(
              t(
                'Words from the start. {one} also matches {two}; {star} stands for any characters inside a word.',
              ),
              { one: mono('git push'), two: mono('git push origin main'), star: mono('*') },
            )}
          >
            <TextField
              label={t('Command')}
              mono
              value={form.command}
              placeholder="git push"
              spellCheck={false}
              autoCapitalize="off"
              onChange={(e) => set({ command: e.target.value })}
              data-testid="settings.rules.command"
            />
          </Field>
          <div className="fgrid">
            <Field
              label={
                <>
                  {t('Some later word matches')} {optional()}
                </>
              }
            >
              <ChipInput
                label={t('Some later word matches')}
                value={form.anyArg}
                onChange={(anyArg) => set({ anyArg })}
                placeholder="--force*, -f"
                data-testid="settings.rules.any-arg"
              />
            </Field>
            <Field
              label={
                <>
                  {t('Output piped to')} {optional()}
                </>
              }
            >
              <ChipInput
                label={t('Output piped to')}
                value={form.pipedTo}
                onChange={(pipedTo) => set({ pipedTo })}
                placeholder="sh, bash"
                data-testid="settings.rules.piped-to"
              />
            </Field>
          </div>
        </>
      );
    case 'read':
    case 'write':
      return (
        <>
          <Field
            label={t('Paths')}
            hint={
              check.badPath
                ? undefined
                : fill(
                    t(
                      'Absolute globs: {star} inside a segment, {globstar} across segments, {tilde} for home, {workspace} for the project folder. Symlinks are resolved first.',
                    ),
                    {
                      star: mono('*'),
                      globstar: mono('**'),
                      tilde: mono('~'),
                      workspace: mono('{workspace}'),
                    },
                  )
            }
          >
            <ChipInput
              label={t('Paths')}
              value={form.paths}
              onChange={(paths) => set({ paths })}
              placeholder="~/work/**"
              bad={check.badPath !== null}
              data-testid="settings.rules.paths"
            />
          </Field>
          {check.badPath ? (
            <div className="qdesc bad" role="alert" data-testid="settings.rules.paths-error">
              {Icon.alert()}
              <span>
                {fill(
                  t('{path} is not an absolute path. Start with {slash}, {tilde} or {workspace}.'),
                  {
                    path: mono(check.badPath),
                    slash: mono('/'),
                    tilde: mono('~'),
                    workspace: mono('{workspace}'),
                  },
                )}
              </span>
            </div>
          ) : null}
        </>
      );
    case 'fetch':
      return (
        <>
          <Field
            label={t('Domains')}
            hint={fill(
              t(
                'Exact, or {sub} for subdomains (not {bare} itself). An IP address matches only exactly. A {curl} in a shell command is a Run rule.',
              ),
              { sub: mono('*.example.com'), bare: mono('example.com'), curl: mono('curl') },
            )}
          >
            <ChipInput
              label={t('Domains')}
              value={form.domains}
              onChange={(domains) => set({ domains })}
              placeholder="docs.example.com, *.example.com"
              bad={check.badDomain !== null}
              data-testid="settings.rules.domains"
            />
          </Field>
          {check.badDomain ? (
            <div className="qdesc bad" role="alert" data-testid="settings.rules.domains-error">
              {Icon.alert()}
              <span>
                {fill(t('{domain} is not a valid domain.'), { domain: mono(check.badDomain) })}
              </span>
            </div>
          ) : null}
        </>
      );
    case 'mcp':
      return (
        <>
          <div className="fgrid">
            <Field label={t('Server')}>
              <TextField
                label={t('Server')}
                mono
                value={form.server}
                placeholder="github"
                spellCheck={false}
                onChange={(e) => set({ server: e.target.value })}
                data-testid="settings.rules.server"
              />
            </Field>
            <Field label={t('Tool')}>
              <TextField
                label={t('Tool')}
                mono
                value={form.tool}
                placeholder="*"
                spellCheck={false}
                onChange={(e) => set({ tool: e.target.value })}
                data-testid="settings.rules.tool"
              />
            </Field>
          </div>
          <div className={`qdesc${check.errors.server ? ' bad' : ''}`}>
            {Icon.info()}
            <span>
              {check.errors.server
                ? t('A server or tool name cannot contain a slash.')
                : fill(t('{star} stands for any. The tool’s own arguments are not looked at.'), {
                    star: mono('*'),
                  })}
            </span>
          </div>
        </>
      );
    case 'any':
      return (
        <div className="qdesc att">
          {Icon.alert()}
          <span>
            {t('Matches every call, including the ones Hoplet cannot classify. Use Ask or Deny.')}
          </span>
        </div>
      );
  }
}

function SummaryLine(props: { form: RuleForm; devices: readonly DeviceInfo[] }) {
  const { form, devices } = props;
  const s = summaryOf(form);
  const what = mono(s.what || '…');
  const verb =
    s.action === 'allow' ? t('Allows') : s.action === 'ask' ? t('Asks before') : t('Denies');
  const where =
    form.scope === 'project' && form.projectPath
      ? fillText(t('in the project {project} on {machine}'), {
          project: baseName(form.projectPath),
          machine: machineOf(devices, form.deviceId),
        })
      : t('in every session');
  const template =
    s.kind === 'run'
      ? t('{verb} running commands that start with {what} {where}.')
      : s.kind === 'read'
        ? t('{verb} reading {what} {where}.')
        : s.kind === 'write'
          ? t('{verb} writing {what} {where}.')
          : s.kind === 'fetch'
            ? t('{verb} fetching from {what} {where}.')
            : s.kind === 'mcp'
              ? t('{verb} calling {what} {where}.')
              : t('{verb} any tool call {where}.');
  return (
    <div className="qsum" data-testid="settings.rules.summary">
      {Icon.info()}
      <span>{fill(template, { verb, what, where })}</span>
    </div>
  );
}

function RuleEditor(props: {
  form: RuleForm;
  devices: readonly DeviceInfo[];
  projects: { deviceId: string; path: string }[];
  state: PermissionRulesState;
  onChange: (form: RuleForm) => void;
  onClose: () => void;
  onSaved: (text: string) => void;
  onDelete: () => void;
}) {
  const { form, state, devices } = props;
  const set = (change: Partial<RuleForm>) => {
    state.clearProblem();
    props.onChange({ ...form, ...change });
  };
  const check = checkForm(form);
  const existing = form.id !== null;
  const title = existing ? t('Edit rule') : t('New rule');
  const choices =
    form.deviceId &&
    form.projectPath &&
    !props.projects.some((p) => p.deviceId === form.deviceId && p.path === form.projectPath)
      ? [...props.projects, { deviceId: form.deviceId, path: form.projectPath }]
      : props.projects;
  const count = form.note.length;
  const save = async () => {
    if (!check.ok || state.saving) return;
    if (await state.save(form.id, toBody(form))) {
      props.onSaved(existing ? t('Rule saved') : t('Rule added'));
    }
  };
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      className="mcard"
      role="group"
      aria-label={title}
      data-testid="settings.rules.editor"
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
      <div className="qf">
        <Field label={t('Tool')} hint={kindHint(form.kind) || undefined}>
          <Segmented
            label={t('Tool')}
            value={form.kind}
            options={RULE_KINDS.map((k) => ({ value: k, label: kindName(k), icon: kindIcon(k) }))}
            onChange={(kind) => set({ kind })}
            data-testid="settings.rules.kind"
          />
        </Field>
        <MatchFields form={form} set={set} />
        <div className="fgrid">
          <Field label={t('Action')}>
            <Segmented
              label={t('Action')}
              value={form.action}
              options={(['allow', 'ask', 'deny'] as const).map((a) => ({
                value: a,
                label: actionName(a),
                icon: actionIcon(a),
              }))}
              onChange={(action) => set({ action })}
              data-testid="settings.rules.action"
            />
          </Field>
          <Field label={t('Scope')}>
            <Segmented
              label={t('Scope')}
              value={form.scope}
              options={[
                { value: 'global', label: t('Global'), icon: Icon.globe() },
                { value: 'project', label: t('Project'), icon: Icon.folder() },
              ]}
              onChange={(scope) => set({ scope })}
              data-testid="settings.rules.scope"
            />
          </Field>
          <div className={`qdesc${form.action === 'deny' ? '' : ''}`}>
            {Icon.info()}
            <span>{actionHint(form)}</span>
          </div>
          {form.scope === 'project' ? (
            <Field label={t('Project')}>
              <Select
                label={t('Project')}
                value={
                  form.deviceId && form.projectPath
                    ? pairValue(form.deviceId, form.projectPath)
                    : ''
                }
                placeholder={t('Choose a project')}
                options={choices.map((p) => ({
                  value: pairValue(p.deviceId, p.path),
                  label: baseName(p.path),
                  detail: machineOf(devices, p.deviceId),
                  leading: Icon.folder(),
                }))}
                onChange={(v) => {
                  const [deviceId = '', projectPath = ''] = v.split('\u0000');
                  set({ deviceId, projectPath });
                }}
                data-testid="settings.rules.project"
              />
            </Field>
          ) : (
            <div className="qdesc">
              {Icon.globe()}
              <span>{t('Applies to every session on every machine.')}</span>
            </div>
          )}
        </div>
        <Field
          label={
            <>
              {t('Note')}
              <span className={`qcount meta${check.errors.note ? ' bad' : ''}`}>
                {count}/{MAX_PERMISSION_NOTE_CHARS}
              </span>
            </>
          }
          hint={t(
            'Shown in the conversation when the rule decides. Without a note the line names the match.',
          )}
        >
          <TextField
            label={t('Note')}
            value={form.note}
            placeholder={t('Run the unit tests')}
            aria-invalid={check.errors.note ? true : undefined}
            onChange={(e) => set({ note: e.target.value })}
            data-testid="settings.rules.note"
          />
        </Field>
        <SummaryLine form={form} devices={devices} />
        {state.problem ? (
          <div className="qdesc bad" role="alert" data-testid="settings.rules.problem">
            {Icon.alert()}
            <span>{state.problem}</span>
          </div>
        ) : null}
      </div>
      <div className="mcard-f">
        {existing ? (
          <Button
            variant="quiet"
            className="del"
            icon={Icon.trash()}
            disabled={state.saving}
            onClick={props.onDelete}
            data-testid="settings.rules.delete"
          >
            {t('Delete')}
          </Button>
        ) : null}
        <Button variant="quiet" onClick={props.onClose}>
          {t('Cancel')}
        </Button>
        <Button
          variant="primary"
          disabled={!check.ok || state.saving}
          onClick={() => void save()}
          data-testid="settings.rules.save"
        >
          {existing ? t('Save') : t('Add rule')}
        </Button>
      </div>
    </div>
  );
}

// -- Turning a built-in rule off -----------------------------------------------------------------------------------

const OFF_EFFECTS: Record<string, string> = {
  'default.secret-files':
    'Sessions on every machine could read .env files, which puts their keys in the model’s context and in the stored session.',
  'default.credential-stores':
    'Sessions could read SSH keys, cloud credentials and other secrets that sit outside any project.',
  'default.hoplet-and-provider-config':
    'Sessions could read the device token and change the hooks and settings that carry these rules.',
  'default.write-outside-workspace':
    'A session could change files anywhere on the machine, not only in its project.',
  'default.force-push':
    'A force push would run without a card whenever the permission mode allows commands, and it rewrites shared history.',
  'default.destructive-git':
    'Resetting, cleaning and discarding changes would run without asking, and the work they remove may not be recoverable.',
  'default.recursive-delete-and-sudo':
    'Recursive deletes and privileged commands would run without asking.',
  'default.download-and-run': 'Code nobody read could be fetched and run in one step.',
};

function ConfirmOff(props: {
  rule: PermissionRuleEntry;
  busy: boolean;
  onKeep: () => void;
  onOff: () => void;
}) {
  const { rule } = props;
  const name = ruleName(rule);
  const effect = OFF_EFFECTS[rule.id];
  return (
    <Dialog
      label={t('Turn off a built-in rule')}
      onClose={props.onKeep}
      className="dlg qd qcf"
      data-testid="settings.rules.confirm"
    >
      <div className="dlg-h">{fillText(t('Turn off “{name}”?'), { name })}</div>
      <div className="dlg-b">
        <div className="qreq">
          <ActionBadge action={rule.action} />
          <span className="mono">{rule.id}</span>
        </div>
        {effect ? <p className="dlg-p">{t(effect)}</p> : null}
        <div className="qxnote att">
          {Icon.alert()}
          <span>
            {t(
              'The rule is turned off for every session on every machine. You can turn it on again here.',
            )}
          </span>
        </div>
      </div>
      <div className="dlg-f">
        <Button variant="quiet" onClick={props.onKeep} data-autofocus>
          {t('Keep it on')}
        </Button>
        <Button
          variant="danger"
          disabled={props.busy}
          onClick={props.onOff}
          data-testid="settings.rules.confirm-off"
        >
          {t('Turn off rule')}
        </Button>
      </div>
    </Dialog>
  );
}

// -- The section ----------------------------------------------------------------------------------------------------

/** The text a search looks in. */
function haystack(rule: {
  tool: string;
  match: PermissionRuleEntry['match'];
  note: string;
  action: string;
}) {
  return [ruleName(rule), rule.note, rule.tool, rule.action, ...matchParts(rule).map((p) => p.text)]
    .join(' ')
    .toLowerCase();
}

export function RulesSection(props: { fixture?: RulesFixture | undefined }) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const state = usePermissionRules(fixture?.list);
  const projects = useProjectChoices(devices, fixture?.projects);
  const [toast, say] = useToast(fixture?.toast ? { text: fixture.toast } : null);
  const [form, setForm] = useState<RuleForm | null>(fixture?.form ?? null);
  const [builtinOpen, setBuiltinOpen] = useState(fixture?.builtinOpen ?? false);
  const [confirm, setConfirm] = useState<string | null>(fixture?.confirm ?? null);
  const [query, setQuery] = useState('');
  const data = state.data;
  const groups = useMemo(() => (data ? groupRules(data) : null), [data]);
  const needle = query.trim().toLowerCase();
  const match = (r: PermissionRuleEntry) => needle === '' || haystack(r).includes(needle);

  if (state.loading) {
    return (
      <p className="rmeta" role="status">
        {t('Loading…')}
      </p>
    );
  }
  if (state.error || !groups || !data) {
    return (
      <p className="rmeta" role="alert" data-testid="settings.rules.load-error">
        {state.error ?? t('Loading…')}
      </p>
    );
  }

  const builtin = groups.builtin.filter(match);
  const global = groups.global.filter(match);
  const sessions = groups.sessions.filter(match);
  const projectGroups = groups.projects
    .map((g) => ({ ...g, rules: g.rules.filter(match) }))
    .filter((g) => g.rules.length > 0);
  const yours =
    groups.global.length +
    groups.sessions.length +
    groups.projects.reduce((n, g) => n + g.rules.length, 0);
  const editingId = form?.id ?? null;
  const showBuiltin = builtinOpen || needle !== '';

  const onToggle = (rule: PermissionRuleEntry, enabled: boolean) => {
    if (rule.builtin && !enabled) {
      setConfirm(rule.id);
      return;
    }
    void state.toggle(rule, enabled).then((ok) => {
      if (!ok) say(t('The change was not saved'), true);
    });
  };
  const remove = async (id: string) => {
    if (await state.remove(id)) {
      if (editingId === id) setForm(null);
      say(t('Rule deleted'));
    } else say(t('The change was not saved'), true);
  };
  const confirming = confirm ? data.rules.find((r) => r.id === confirm) : undefined;
  const rows = (rules: PermissionRuleEntry[]) => (
    <div className="mp-list">
      {rules.map((rule) => (
        <RuleRow
          key={rule.id}
          rule={rule}
          selected={rule.id === editingId}
          busy={state.saving}
          onToggle={(enabled) => onToggle(rule, enabled)}
          onEdit={() => {
            state.clearProblem();
            const next = formOfRule(rule);
            if (next) setForm(next);
          }}
          onDelete={() => void remove(rule.id)}
        />
      ))}
    </div>
  );
  const empty = yours === 0 && needle === '';
  const noMatch =
    needle !== '' && builtin.length + global.length + sessions.length + projectGroups.length === 0;
  let firstGroup = form === null;
  const first = () => {
    const f = firstGroup;
    firstGroup = false;
    return f;
  };

  return (
    <div className="qpage" data-testid="settings.rules">
      <div className="qhead">
        <h1>{t('Permission rules')}</h1>
      </div>
      <p className="rmeta qlead">
        {t(
          'A rule decides a tool call before it reaches you. A deny always wins; otherwise the narrowest scope decides, and ask beats allow within a scope. When nothing matches, the session’s permission mode decides.',
        )}
      </p>
      <RuleMachines fixture={fixture?.machines} />
      <div className="qtb">
        <TextField
          label={t('Search rules')}
          type="search"
          value={query}
          placeholder={t('Search rules')}
          onChange={(e) => setQuery(e.target.value)}
          data-testid="settings.rules.search"
        />
        <Button
          variant="primary"
          icon={Icon.plus()}
          onClick={() => {
            state.clearProblem();
            setForm(emptyForm());
          }}
          data-testid="settings.rules.add"
        >
          {t('Add rule')}
        </Button>
      </div>
      {form ? (
        <RuleEditor
          form={form}
          devices={devices}
          projects={projects}
          state={state}
          onChange={setForm}
          onClose={() => {
            state.clearProblem();
            setForm(null);
          }}
          onSaved={(text) => {
            setForm(null);
            say(text);
          }}
          onDelete={() => form.id && void remove(form.id)}
        />
      ) : null}
      {groups.builtin.length > 0 && (needle === '' || builtin.length > 0) ? (
        <>
          <Group
            icon={Icon.lock()}
            title={t('Built in')}
            count={fillText(t('{count} rules · {on} on'), {
              count: groups.builtin.length,
              on: builtinOn(groups.builtin),
            })}
            note={t('Turn off needs a second confirmation')}
            first={first()}
            open={showBuiltin}
            onToggle={() => setBuiltinOpen((o) => !o)}
            testid="settings.rules.group.builtin"
          />
          {showBuiltin ? rows(builtin) : null}
        </>
      ) : null}
      {empty ? (
        <>
          <Group icon={Icon.globe()} title={t('Global')} count="0" first={first()} />
          <div className="mp-list">
            <div className="uempty" data-testid="settings.rules.empty">
              <span className="tile">{Icon.rules()}</span>
              <b>{t('No rules of your own')}</b>
              <p>
                {t(
                  'The built-in rules still apply. Add a rule to allow routine commands, ask before risky ones, or deny what a session must never touch.',
                )}
              </p>
              <div className="acts">
                <Button variant="primary" icon={Icon.plus()} onClick={() => setForm(emptyForm())}>
                  {t('Add rule')}
                </Button>
              </div>
            </div>
          </div>
        </>
      ) : null}
      {global.length > 0 ? (
        <>
          <Group
            icon={Icon.globe()}
            title={t('Global')}
            count={String(global.length)}
            note={t('Every session')}
            first={first()}
          />
          {rows(global)}
        </>
      ) : null}
      {projectGroups.map((g) => (
        <div key={g.key}>
          <Group
            icon={Icon.folder()}
            title={fillText(t('Project · {name} · {machine}'), {
              name: baseName(g.projectPath),
              machine: machineOf(devices, g.deviceId),
            })}
            count={String(g.rules.length)}
            note={t('Sessions of this project on this machine')}
            first={first()}
          />
          {rows(g.rules)}
        </div>
      ))}
      {sessions.length > 0 ? (
        <>
          <Group
            icon={Icon.terminal()}
            title={t('Single sessions')}
            count={String(sessions.length)}
            note={t('Made from a permission card')}
            first={first()}
          />
          {rows(sessions)}
        </>
      ) : null}
      {noMatch ? (
        <p className="rmeta" role="status" data-testid="settings.rules.no-match">
          {t('No rule matches this search.')}
        </p>
      ) : null}
      {groups.local.map((l) => {
        const rules = l.rules.filter((r) => needle === '' || haystack(r).includes(needle));
        if (rules.length === 0) return null;
        const device = devices.find((d) => d.deviceId === l.deviceId);
        return (
          <div key={l.deviceId} data-testid={`settings.rules.local.${l.deviceId}`}>
            <Group
              icon={
                <MachineIcon
                  kind={systemOf(device?.system?.os)}
                  online={device?.connected ?? false}
                />
              }
              title={fillText(t('{machine} · set on the machine'), {
                machine: device?.name ?? l.deviceId,
              })}
              count={String(rules.length)}
              note={
                <>
                  {Icon.lock()}
                  {t('Read-only')}
                </>
              }
            />
            <div className="mp-list">
              {rules.map((r, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: the machine's own list, in order
                <LocalRow key={i} rule={r} />
              ))}
              <div className="qsub">
                {fill(t('From {key} in the agent’s configuration. Edit it on that machine.'), {
                  key: mono('permission_deny'),
                })}
              </div>
            </div>
          </div>
        );
      })}
      {confirming ? (
        <ConfirmOff
          rule={confirming}
          busy={state.saving}
          onKeep={() => setConfirm(null)}
          onOff={() => {
            void state.toggle(confirming, false).then((ok) => {
              setConfirm(null);
              say(ok ? t('Rule turned off') : t('The change was not saved'), !ok);
            });
          }}
        />
      ) : null}
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </div>
  );
}
