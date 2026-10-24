import type {
  DeviceInfo,
  SessionMcpDefaultsResponse,
  SessionMcpSettings,
  SessionVisibility,
} from '@hoplet/shared';
import { useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { Menu } from '../../components/Menu.tsx';
import { McNote, reachOptions, Ticked } from '../../components/mcp-parts.tsx';
import { Popover } from '../../components/Popover.tsx';
import { Switch } from '../../components/Switch.tsx';
import { t } from '../../lib/i18n.ts';
import {
  mcpOff,
  projectName,
  reachName,
  sendOn,
  sortedDefaults,
  withCreate,
  withHandoff,
  withReach,
  withSend,
  withWriteConfig,
} from '../../lib/session-mcp.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { useMcpDefaults } from '../../lib/use-session-mcp.ts';
import { useProjectChoices } from './rules.tsx';
import '../../components/mcp.css';

/** What the development fixtures hand to Settings > Other sessions. */
export type McpFixture = {
  defaults: SessionMcpDefaultsResponse;
  devices: DeviceInfo[];
  /** The directories each machine offers for a new project default, by device ID. */
  projects: Record<string, string[]>;
  /** The row whose Reach menu starts open: `global`, or the project's `<deviceId><path>`. */
  menu?: string;
  loading?: boolean;
  error?: string;
};

const GLOBAL = 'global';
const rowKey = (deviceId: string, path: string) => `${deviceId}${path}`;

/** The Reach of one row: a button that opens a list of three, each with its one-line meaning. */
function ReachSelect(props: {
  value: SessionVisibility;
  label: string;
  open: boolean;
  onOpen: (open: boolean) => void;
  onPick: (v: SessionVisibility) => void;
  testid: string;
}) {
  const { value, open, onOpen } = props;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    ref.current?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus();
  }, [open]);
  return (
    <span className="mc-rcell">
      <button
        type="button"
        className="mc-rsel"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`${t('Reach')}: ${reachName(value)} · ${props.label}`}
        onClick={() => onOpen(!open)}
        data-testid={`${props.testid}.reach`}
      >
        {reachName(value)}
        {Icon.down()}
      </button>
      <Popover open={open} onClose={() => onOpen(false)} className="mc-rmenu">
        <div
          ref={ref}
          role="menu"
          aria-label={t('Reach')}
          tabIndex={-1}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.stopPropagation();
              onOpen(false);
            }
          }}
          data-testid="settings.mcp.menu"
        >
          {reachOptions().map((o) => (
            <button
              key={o.value}
              type="button"
              role="menuitemradio"
              aria-checked={o.value === value}
              className={`mc-rmi${o.value === value ? ' on' : ''}`}
              onClick={() => {
                props.onPick(o.value);
                onOpen(false);
              }}
            >
              <span className="radio" aria-hidden="true" />
              <span className="tx">
                <b>{o.name}</b>
                <span>{o.text}</span>
              </span>
            </button>
          ))}
        </div>
      </Popover>
    </span>
  );
}

function Row(props: {
  testid: string;
  name: string;
  /** The second line: a path, or what the row stands for. */
  sub: string;
  place: string;
  /** The machine's agent has `session_mcp` off. */
  off: boolean;
  settings: SessionMcpSettings;
  onChange: (next: SessionMcpSettings) => void;
  menuOpen: boolean;
  onMenu: (open: boolean) => void;
  global?: boolean;
  onRemove?: () => void;
}) {
  const { settings: s, onChange, name } = props;
  const sw = (what: string) => `${what} · ${name}`;
  return (
    <div
      className={`mx-r${props.global ? ' glob' : ''}${props.off ? ' off' : ''}`}
      data-testid={props.testid}
    >
      <div className="nm">
        <b>{name}</b>
        <span className="rmeta">{props.sub}</span>
      </div>
      <span
        className="mc-tag line"
        title={props.off ? t('session_mcp is off on this machine') : undefined}
      >
        {props.global ? Icon.globe() : props.off ? Icon.lock() : Icon.laptop()}
        {props.place}
      </span>
      <span className="ce">
        <Switch
          label={sw(t('Send prompts and interrupt'))}
          hideLabel
          checked={sendOn(s)}
          disabled={s.create}
          onChange={(on) => onChange(withSend(s, on))}
          data-testid={`${props.testid}.send`}
        />
      </span>
      <span className="ce">
        <Switch
          label={sw(t('Start new sessions'))}
          hideLabel
          checked={s.create}
          onChange={(on) => onChange(withCreate(s, on))}
          data-testid={`${props.testid}.start`}
        />
      </span>
      <span className="ce">
        <Switch
          label={sw(t('Ask to hand this session off'))}
          hideLabel
          checked={s.requestHandoff}
          onChange={(on) => onChange(withHandoff(s, on))}
          data-testid={`${props.testid}.handoff`}
        />
      </span>
      <span className="ce">
        <Switch
          label={sw(t('Propose configuration changes'))}
          hideLabel
          checked={s.writeConfig}
          onChange={(on) => onChange(withWriteConfig(s, on))}
          data-testid={`${props.testid}.write-config`}
        />
      </span>
      <ReachSelect
        value={s.visibility}
        label={name}
        open={props.menuOpen}
        onOpen={props.onMenu}
        onPick={(v) => onChange(withReach(s, v))}
        testid={props.testid}
      />
      <span className="acts2">
        {props.onRemove ? (
          <IconButton
            label={`${t('Remove default')} · ${name}`}
            icon={Icon.trash()}
            onClick={props.onRemove}
            data-testid={`${props.testid}.remove`}
          />
        ) : null}
      </span>
    </div>
  );
}

/**
 * Settings > Other sessions (ADR 0034): the defaults a new session starts with, as a table. The first row is the
 * global default, one row per project follows. A session's own settings are in its inspector.
 */
export function McpSection(props: { fixture?: McpFixture | undefined }) {
  const { fixture } = props;
  const state = useMcpDefaults(fixture?.defaults);
  const devices = useDevices(fixture?.devices);
  const choices = useProjectChoices(devices, fixture?.projects);
  const [menu, setMenu] = useState<string | null>(fixture?.menu ?? null);
  const [adding, setAdding] = useState(false);

  if (state.loading || fixture?.loading) {
    return (
      <div className="tkpage">
        <h1>{t('Other sessions')}</h1>
        <p className="rmeta" role="status" data-testid="settings.mcp.loading">
          {t('Loading…')}
        </p>
      </div>
    );
  }
  const failure = fixture?.error ?? state.error;
  if (failure || !state.data) {
    return (
      <div className="tkpage">
        <h1>{t('Other sessions')}</h1>
        <p className="rmeta" role="alert" data-testid="settings.mcp.load-error">
          {failure ?? t('Loading…')}
        </p>
      </div>
    );
  }

  const { global, projects } = state.data;
  const machineOf = (id: string) => devices.find((d) => d.deviceId === id);
  const rows = sortedDefaults(projects, devices);
  const free = choices.filter(
    (c) => !projects.some((p) => p.deviceId === c.deviceId && p.path === c.path),
  );
  const anyOff = rows.some((p) => mcpOff(machineOf(p.deviceId)));
  const addButton = (
    <span className="mc-pick">
      <Button
        icon={Icon.plus()}
        aria-haspopup="menu"
        aria-expanded={adding}
        disabled={free.length === 0}
        onClick={() => setAdding((o) => !o)}
        data-testid="settings.mcp.add"
      >
        {t('Add project default')}
      </Button>
      {adding ? (
        <Menu
          label={t('Add project default')}
          entries={free.map((c) => ({
            kind: 'item' as const,
            id: `add.${rowKey(c.deviceId, c.path)}`,
            label: `${projectName(c.path)} · ${machineOf(c.deviceId)?.name ?? c.deviceId}`,
            icon: Icon.folder(),
            onSelect: () => state.setProject(c.deviceId, c.path, global),
          }))}
          onClose={() => setAdding(false)}
        />
      ) : null}
    </span>
  );

  return (
    <div className="tkpage mc-wrap" data-testid="settings.mcp">
      <h1>{t('Other sessions')}</h1>
      <p className="tk-lead mc-lead">
        {t(
          'What a session may do to other sessions through Hoplet’s MCP tools. A default applies to sessions that begin afterwards; a running session keeps its own settings. Only the host token changes these.',
        )}
      </p>
      <div className="plist mc-table" data-testid="settings.mcp.table">
        <div className="mx-h" aria-hidden="true">
          <span>{t('Project')}</span>
          <span>{t('Machine')}</span>
          <span className="ce">{t('Send')}</span>
          <span className="ce">{t('Start')}</span>
          <span className="ce">{t('Hand-off')}</span>
          <span className="ce">{t('Propose changes')}</span>
          <span>{t('Reach')}</span>
          <span />
        </div>
        <Row
          testid="settings.mcp.row.global"
          name={t('Global default')}
          sub={t('Sessions with no project default')}
          place={t('All projects')}
          off={false}
          global
          settings={global}
          onChange={state.setGlobal}
          menuOpen={menu === GLOBAL}
          onMenu={(open) => setMenu(open ? GLOBAL : null)}
        />
        {rows.map((p) => {
          const key = rowKey(p.deviceId, p.path);
          const machine = machineOf(p.deviceId);
          return (
            <Row
              key={key}
              testid={`settings.mcp.row.${key}`}
              name={projectName(p.path)}
              sub={p.path}
              place={machine?.name ?? p.deviceId}
              off={mcpOff(machine)}
              settings={p.settings}
              onChange={(next) => state.setProject(p.deviceId, p.path, next)}
              menuOpen={menu === key}
              onMenu={(open) => setMenu(open ? key : null)}
              onRemove={() => state.removeProject(p.deviceId, p.path)}
            />
          );
        })}
      </div>
      {state.problem ? (
        <p className="rmeta" role="alert" data-testid="settings.mcp.problem">
          {state.problem}
        </p>
      ) : null}
      {rows.length === 0 ? (
        <div className="plist uempty mc-empty" data-testid="settings.mcp.empty">
          <span className="tile">{Icon.folder()}</span>
          <b>{t('No project defaults')}</b>
          <p>
            {t(
              'Every project uses the global row. Add a default for a project that needs more or less.',
            )}
          </p>
          <div className="acts">{addButton}</div>
        </div>
      ) : (
        <div className="mc-addrow">{addButton}</div>
      )}
      <span className="mc-meta mc-foot" data-testid="settings.mcp.always">
        {t(
          'Reading other sessions is always on. Answering permission requests is never available to a session.',
        )}
      </span>
      {anyOff ? (
        <McNote icon={Icon.lock()} testid="settings.mcp.off-note">
          <Ticked
            text={t(
              'A greyed row is on a machine with `session_mcp` off. Its default is kept and applies once the operator turns it on in the agent’s `config.json`.',
            )}
          />
        </McNote>
      ) : null}
    </div>
  );
}
