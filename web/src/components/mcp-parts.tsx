import type { DeviceInfo, SessionMcpSettings, SessionVisibility } from '@hoplet/shared';
import { Fragment, type ReactNode, useState } from 'react';
import { t } from '../lib/i18n.ts';
import {
  addableMachines,
  reachName,
  reachSummary,
  sendOn,
  withAutoApprove,
  withCreate,
  withHandoff,
  withMachine,
  withoutMachine,
  withReach,
  withReadConfig,
  withSend,
  withWriteConfig,
} from '../lib/session-mcp.ts';
import { Icon } from './icons.tsx';
import { Menu } from './Menu.tsx';
import { Segmented } from './Segmented.tsx';
import { Switch } from './Switch.tsx';
import './mcp.css';

// The parts the inspector tab and Settings > Other sessions share: what a session may do to other sessions
// through Hoplet's MCP tools (ADR 0034). Every change goes out through `onChange` with the settings whole.

const HOSTING_URL = 'https://github.com/yangflow/hoplet/blob/main/docs/hosting.md#session-mcp';

/** `text` with its backtick-quoted parts set as code. */
export function Ticked({ text }: { text: string }) {
  let at = 0;
  return (
    <>
      {text.split('`').map((part, i) => {
        const key = at;
        at += part.length + 1;
        return i % 2 === 1 ? <code key={key}>{part}</code> : <Fragment key={key}>{part}</Fragment>;
      })}
    </>
  );
}

/** A quiet note; `warn` draws the attention colour. */
export function McNote(props: {
  icon: ReactNode;
  warn?: boolean;
  testid?: string;
  children: ReactNode;
}) {
  return (
    <div className={`mc-note${props.warn ? ' warn' : ''}`} role="note" data-testid={props.testid}>
      {props.icon}
      <span>{props.children}</span>
    </div>
  );
}

function LockTag(props: { children: string }) {
  return (
    <span className="mc-tag line">
      {Icon.lock()}
      {props.children}
    </span>
  );
}

function Row(props: {
  icon: ReactNode;
  title: string;
  tools: string | undefined;
  metas: (string | false | undefined)[];
  control: ReactNode;
  extra?: ReactNode;
  testid: string;
}) {
  return (
    <div className="mc-cr" data-testid={props.testid}>
      {props.icon}
      <div className="mc-cx">
        <b>{props.title}</b>
        {props.tools ? <span className="mc-tools">{props.tools}</span> : null}
        {props.metas.map((m) =>
          m ? (
            <span key={m} className="mc-meta">
              {m}
            </span>
          ) : null,
        )}
        {props.extra}
      </div>
      <div className="mc-cc">{props.control}</div>
    </div>
  );
}

/** The machines a session's hand-off may be approved for without asking, and the control that adds one. */
function AutoApprove(props: {
  settings: SessionMcpSettings;
  devices: readonly DeviceInfo[];
  onChange: (next: SessionMcpSettings) => void;
  testid: string;
}) {
  const { settings: s, devices, onChange, testid } = props;
  const [adding, setAdding] = useState(false);
  const name = (id: string) => devices.find((d) => d.deviceId === id)?.name ?? id;
  const addable = addableMachines(devices, s.autoApproveMachines);
  const label = t('Approve automatically');
  return (
    <div className="mc-sub" data-testid={`${testid}.auto`}>
      <div className="mc-sr">
        <span className="grow">{label}</span>
        <Switch
          label={label}
          hideLabel
          checked={s.autoApproveHandoff}
          onChange={(on) => onChange(withAutoApprove(s, on))}
          data-testid={`${testid}.auto.switch`}
        />
      </div>
      {s.autoApproveHandoff ? (
        <div className="mc-sr" data-testid={`${testid}.auto.machines`}>
          <span className="mc-meta">{t('Machines')}</span>
          {s.autoApproveMachines.map((id) => (
            <span key={id} className="mc-tag line">
              {Icon.laptop()}
              {name(id)}
              <button
                type="button"
                className="mc-x"
                aria-label={t('Remove {name}').replace('{name}', name(id))}
                title={t('Remove {name}').replace('{name}', name(id))}
                onClick={() => onChange(withoutMachine(s, id))}
              >
                {Icon.close()}
              </button>
            </span>
          ))}
          <span className="mc-pick">
            <button
              type="button"
              className="mc-tag"
              aria-haspopup="menu"
              aria-expanded={adding}
              disabled={addable.length === 0}
              onClick={() => setAdding((o) => !o)}
              data-testid={`${testid}.auto.add`}
            >
              {Icon.plus()}
              {t('Add machine')}
            </button>
            {adding ? (
              <Menu
                label={t('Add machine')}
                entries={addable.map((d) => ({
                  kind: 'item' as const,
                  id: `machine.${d.deviceId}`,
                  label: d.name,
                  icon: Icon.laptop(),
                  onSelect: () => onChange(withMachine(s, d.deviceId)),
                }))}
                onClose={() => setAdding(false)}
              />
            ) : null}
          </span>
        </div>
      ) : null}
      <span className="mc-meta">
        {s.autoApproveHandoff
          ? t(
              'Only for the machines listed, up to 3 an hour, and each still waits for the end of the turn. A request for any other machine asks you.',
            )
          : t(
              'Off. Every request asks you. Turn it on to let the machines you list go ahead without a card.',
            )}
      </span>
    </div>
  );
}

/**
 * The abilities of a session, one row each. Reading is always on and answering permission requests never
 * available; `compact` leaves out the tool names and the longer notes. `devices` names the machines of the
 * automatic approval, which only `autoApprove` shows.
 */
export function Caps(props: {
  settings: SessionMcpSettings;
  onChange: (next: SessionMcpSettings) => void;
  compact?: boolean;
  /** Shows the automatic approval below the hand-off row when hand-off is on. */
  autoApprove?: { devices: readonly DeviceInfo[] };
  testid: string;
}) {
  const { settings: s, onChange, compact, testid } = props;
  const tools = (names: string) => (compact ? undefined : names);
  const nSend = t('Send prompts and interrupt');
  const nStart = t('Start new sessions');
  const nHandoff = t('Ask to hand this session off');
  const nRead = t('Read configuration');
  const nWrite = t('Propose configuration changes');
  return (
    <div className={`mc-caps${compact ? ' compact' : ''}`} data-testid={testid}>
      <Row
        testid={`${testid}.read`}
        icon={Icon.eye()}
        title={t('Read other sessions')}
        tools={tools(
          'list_machines · list_sessions · read_session · wait_children · read_child_diff',
        )}
        metas={[]}
        control={<LockTag>{t('Always on')}</LockTag>}
      />
      <Row
        testid={`${testid}.send`}
        icon={Icon.paperPlane()}
        title={nSend}
        tools={tools('send_prompt · interrupt_session')}
        metas={[s.create && t('Included with starting sessions.')]}
        control={
          <Switch
            label={nSend}
            hideLabel
            checked={sendOn(s)}
            disabled={s.create}
            onChange={(on) => onChange(withSend(s, on))}
            data-testid={`${testid}.send.switch`}
          />
        }
      />
      <Row
        testid={`${testid}.start`}
        icon={Icon.start()}
        title={nStart}
        tools={tools('start_session · collect_child · cancel_child')}
        metas={[!compact && t('Up to 12 an hour and 4 running at once.')]}
        control={
          <Switch
            label={nStart}
            hideLabel
            checked={s.create}
            onChange={(on) => onChange(withCreate(s, on))}
            data-testid={`${testid}.start.switch`}
          />
        }
      />
      <Row
        testid={`${testid}.handoff`}
        icon={Icon.handoff()}
        title={nHandoff}
        tools={tools('request_handoff')}
        metas={[!compact && t('You confirm every request, in every permission mode.')]}
        control={
          <Switch
            label={nHandoff}
            hideLabel
            checked={s.requestHandoff}
            onChange={(on) => onChange(withHandoff(s, on))}
            data-testid={`${testid}.handoff.switch`}
          />
        }
        extra={
          props.autoApprove && s.requestHandoff ? (
            <AutoApprove
              settings={s}
              devices={props.autoApprove.devices}
              onChange={onChange}
              testid={`${testid}.handoff`}
            />
          ) : null
        }
      />
      <Row
        testid={`${testid}.read-config`}
        icon={Icon.read()}
        title={nRead}
        tools={tools('get_config · list_skills · read_skill · read_memory')}
        metas={[s.writeConfig && t('Included with proposing changes.')]}
        control={
          <Switch
            label={nRead}
            hideLabel
            checked={s.readConfig || s.writeConfig}
            disabled={s.writeConfig}
            onChange={(on) => onChange(withReadConfig(s, on))}
            data-testid={`${testid}.read-config.switch`}
          />
        }
      />
      <Row
        testid={`${testid}.write-config`}
        icon={Icon.sliders()}
        title={nWrite}
        tools={tools('propose_config_change · propose_memory_change · propose_skill_change')}
        metas={[
          !compact &&
            t(
              'You confirm every change request, in every permission mode. Nothing is written first.',
            ),
        ]}
        control={
          <Switch
            label={nWrite}
            hideLabel
            checked={s.writeConfig}
            onChange={(on) => onChange(withWriteConfig(s, on))}
            data-testid={`${testid}.write-config.switch`}
          />
        }
      />
      <Row
        testid={`${testid}.answer`}
        icon={Icon.ban()}
        title={t('Answer permission requests')}
        tools={undefined}
        metas={[!compact && t('Only you can. A session cannot be given this.')]}
        control={<LockTag>{t('Never')}</LockTag>}
      />
    </div>
  );
}

/** How far a session reaches; `machine` names the machine the session runs on, or is left out for a default. */
export function Reach(props: {
  settings: SessionMcpSettings;
  onChange: (next: SessionMcpSettings) => void;
  machine?: string | undefined;
  testid: string;
}) {
  const { settings: s, onChange, machine, testid } = props;
  const label = t('Reach');
  const text: Record<SessionVisibility, string> = machine
    ? {
        project: t('Sessions of this project on this machine, worktrees included.'),
        machine: t('Every session on {machine}.').replace('{machine}', machine),
        all: t('Every session Hoplet knows, on every machine.'),
      }
    : {
        project: t('Sessions of the same project on the same machine, worktrees included.'),
        machine: t('Every session on the same machine.'),
        all: t('Every session Hoplet knows, on every machine.'),
      };
  return (
    <div className="mc-vis" data-testid={testid}>
      <span className="l">{label}</span>
      <Segmented
        label={label}
        value={s.visibility}
        options={[
          { value: 'project', label: reachName('project') },
          { value: 'machine', label: reachName('machine') },
          { value: 'all', label: reachName('all') },
        ]}
        onChange={(v) => onChange(withReach(s, v))}
        data-testid={`${testid}.seg`}
      />
      <span className="mc-meta" data-testid={`${testid}.text`}>
        {text[s.visibility]}
      </span>
      {s.visibility === 'all' ? (
        <McNote warn icon={Icon.alert()} testid={`${testid}.warn`}>
          {t(
            'A session that reaches every machine can steer work in projects it was not started for. It still cannot answer permission requests.',
          )}
        </McNote>
      ) : null}
      {sendOn(s) ? (
        <McNote icon={Icon.shield()} testid={`${testid}.mode`}>
          {t(
            'A prompt only reaches a session whose permission mode is not more permissive than this one’s. From least to most: Plan, Default, Accept edits, Auto, Bypass permissions.',
          )}
        </McNote>
      ) : null}
    </div>
  );
}

export function LimitsNote(props: { testid: string }) {
  return (
    <McNote icon={Icon.clock()} testid={props.testid}>
      {t(
        'Limits per session: 6 prompts a minute and 60 an hour, 5 new sessions an hour with 10 running at most, and a chain of at most 3 sessions. A message from you starts a new chain.',
      )}
    </McNote>
  );
}

/** What stands where the settings would: the machine's agent has `session_mcp` off. */
export function OffBox(props: { machine: string; testid: string }) {
  const { machine } = props;
  return (
    <div className="mc-off" data-testid={props.testid}>
      <span className="tile">{Icon.lock()}</span>
      <b>{t('Other sessions is off on {machine}').replace('{machine}', machine)}</b>
      <p>
        <Ticked
          text={t(
            'Set `session_mcp` in the agent’s `config.json` on {machine} and restart the agent. Only the machine’s operator can turn it on; Hoplet cannot.',
          ).replace('{machine}', machine)}
        />
      </p>
      <a className="mc-link" href={HOSTING_URL} target="_blank" rel="noopener noreferrer">
        {t('Hosting guide')}
      </a>
    </div>
  );
}

/** The name and one-line meaning of a reach, as the menu of the table lists them. */
export function reachOptions(): { value: SessionVisibility; name: string; text: string }[] {
  return (['project', 'machine', 'all'] as const).map((value) => ({
    value,
    name: reachName(value),
    text: reachSummary(value),
  }));
}
