import type { CommandsResult, PromptImage, SessionConfig } from '@hoplet/shared';
import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { providerName } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { postAttachment } from '../../lib/api.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { machineLabel, systemName, systemOf } from '../../lib/system.ts';
import { Composer } from './composer/composer.tsx';
import { ModeMenu, modeOption, nextMode } from './composer/mode-menu.tsx';
import { hasChoices, ModelMenu, withValues } from './composer/model-menu.tsx';
import { useCommands } from './composer/use-completions.ts';
import { BranchPart, WhereRow } from './composer/where.tsx';
import type { NewSessionRequest, SessionTarget } from './model.ts';
import type { ComposerView } from './runtime.ts';
import { type Dir, useDeviceDirs } from './use-device-dirs.ts';

/** Where a draft starts its session: a machine and one of its watched project directories. */
export type DraftWhere = { deviceName: string; path: string };

/** What development fixtures set: the text, and a picker or the model menu open. */
export type DraftFixture = {
  prompt?: string;
  open?: 'machine' | 'directory' | 'model';
  /** The machine's directories as its catalog would list them. */
  dirs?: Dir[];
  /** What `/` shows, and the selected and hovered rows. */
  completions?: { commands: CommandsResult; selected?: number; hovered?: number };
};

/** The folder a directory sits in, as the picker shows it: the home directory reads `~`. */
export function folderOf(path: string): string {
  const parent = path.replace(/[/\\]+[^/\\]+[/\\]*$/, '') || '/';
  return parent.replace(/^\/(?:Users|home)\/[^/]+(?=\/|$)/, '~');
}

/**
 * New session as a draft (docs/spec.md#hoplet): an empty conversation whose composer chooses the machine, the
 * directory and the options its provider's runtime driver declares. The first prompt starts the session; a
 * refused start keeps the draft and its text and says why.
 */
export function Draft(props: {
  targets: SessionTarget[];
  where: Partial<DraftWhere>;
  /** The draft's machine and directory as they resolve, for the sidebar to show the draft's row. */
  onWhere: (where: DraftWhere) => void;
  /** Read each machine's directories from its catalog; fixtures turn this off. */
  loadDirectories: boolean;
  /** Changes when a project was added, so the directories are read again. */
  dirsVersion?: number;
  /** Starts the session; resolves when it exists. */
  onSubmit: (request: NewSessionRequest) => Promise<void>;
  onAddProject: (deviceName: string) => void;
  voiceAvailable: boolean;
  fixture?: DraftFixture;
}) {
  const { targets, where, onWhere } = props;
  const firstOnline = targets.find((x) => x.online) ?? targets[0];
  const machine =
    targets.find((x) => x.deviceName === where.deviceName)?.deviceName ??
    firstOnline?.deviceName ??
    '';
  const target = targets.find((x) => x.deviceName === machine);
  const catalog = useDeviceDirs(
    target?.deviceId ?? null,
    target?.dirs ?? [],
    props.loadDirectories,
    props.dirsVersion ?? 0,
  );
  const dirs = props.fixture?.dirs ?? catalog.dirs;
  const path = dirs.some((d) => d.path === where.path) ? (where.path ?? '') : (dirs[0]?.path ?? '');

  useEffect(() => {
    if (machine && path && (machine !== where.deviceName || path !== where.path)) {
      onWhere({ deviceName: machine, path });
    }
  }, [machine, path, where.deviceName, where.path, onWhere]);

  const commands = useCommands(
    !props.fixture && target?.deviceId && path ? { deviceId: target.deviceId, cwd: path } : null,
    props.fixture?.completions?.commands,
  );
  const providers = target?.providers ?? [];
  // The provider and the option values this window picked; an option left alone keeps the driver's default.
  const [pickedProvider, setPickedProvider] = useState(providers[0]?.provider ?? 'claude');
  const [picked, setPicked] = useState<SessionConfig>({});
  const provider = providers.find((p) => p.provider === pickedProvider) ?? providers[0];
  const caps = provider?.capabilities;
  // Only picks the provider declares still hold.
  const config: SessionConfig = Object.fromEntries(
    Object.entries(picked).filter(([id]) => caps?.config.some((o) => o.id === id)),
  );

  const shown = withValues(caps?.config, config);
  const mode = modeOption(shown);
  const cycleMode = () => {
    const next = mode ? nextMode(mode) : undefined;
    if (mode && next !== undefined) setPicked((cur) => ({ ...cur, [mode.id]: next }));
  };

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [restore, setRestore] = useState<{
    text: string;
    withImages?: boolean;
    n: number;
  } | null>(null);

  const name = providerName(provider?.provider ?? 'claude');
  const view: ComposerView = useMemo(() => {
    const placeholder = !target?.online
      ? t('No machine online')
      : path === ''
        ? t('Add a project to start')
        : !provider
          ? t('No provider on {device}').replace('{device}', machine)
          : busy
            ? t('Starting {provider}').replace('{provider}', name)
            : t('Ask {provider}…').replace('{provider}', name);
    return {
      layout: 'field',
      placeholder,
      inputEnabled: !!target?.online && path !== '' && !!provider && !busy,
    };
  }, [target?.online, path, provider, busy, machine, name]);

  async function start(text: string, attachments: string[]) {
    if (!target?.deviceId || !provider || !caps || path === '') return;
    setBusy(true);
    setError(null);
    try {
      await props.onSubmit({
        deviceId: target.deviceId,
        deviceName: target.deviceName,
        path,
        provider: provider.provider,
        config,
        prompt: text,
        attachments,
      });
    } catch (err) {
      setError(errorText(err));
      setRestore((r) => ({ text, withImages: attachments.length > 0, n: (r?.n ?? 0) + 1 }));
      setBusy(false);
    }
  }

  const open = props.fixture?.open;
  const branch = dirs.find((d) => d.path === path)?.branch;
  const whereRow = (
    <WhereRow>
      {[
        <Select
          key="machine"
          appearance="chip"
          label={t('Machine')}
          detail={
            target ? machineLabel(target.deviceName, target.system, target.online) : t('Machine')
          }
          value={machine}
          options={targets.map((x) => ({
            value: x.deviceName,
            label: x.deviceName,
            detail: x.online ? (systemName(x.system?.os) ?? undefined) : t('Offline'),
            disabled: !x.online,
            leading: <MachineIcon kind={systemOf(x.system?.os)} online={x.online} />,
          }))}
          disabled={busy}
          onChange={(deviceName) => onWhere({ deviceName, path: '' })}
          initialOpen={open === 'machine'}
          data-testid="cockpit.draft-machine"
        />,
        <Select
          key="directory"
          appearance="chip"
          label={t('Directory')}
          detail={path || t('No directories yet')}
          heading={machine}
          value={path}
          options={dirs.map((d) => ({
            value: d.path,
            label: d.name,
            detail: folderOf(d.path),
            leading: Icon.folder(),
          }))}
          action={{
            label: t('Add project…'),
            icon: Icon.plus(),
            onSelect: () => props.onAddProject(machine),
            'data-testid': 'cockpit.draft-add-project',
          }}
          disabled={busy || !target?.online}
          onChange={(p) => onWhere({ deviceName: machine, path: p })}
          initialOpen={open === 'directory'}
          data-testid="cockpit.draft-directory"
        />,
        branch ? <BranchPart key="branch" branch={branch} path={path} /> : null,
      ]}
    </WhereRow>
  );

  return (
    <div className="conv-wrap draft" data-testid="cockpit.draft">
      <p className="draft-note">{t('The first prompt starts the session')}</p>
      <div className="dock">
        <div className="col">
          <Composer
            view={view}
            where={whereRow}
            {...(mode && !busy ? { onCycleMode: cycleMode } : {})}
            options={
              <>
                {mode ? (
                  <ModeMenu
                    option={mode}
                    onChange={(id, value) => setPicked((cur) => ({ ...cur, [id]: value }))}
                    disabled={busy}
                  />
                ) : null}
                {hasChoices(providers, provider) ? (
                  <ModelMenu
                    providers={providers}
                    provider={provider?.provider ?? pickedProvider}
                    config={shown}
                    onProvider={(next) => {
                      setPickedProvider(next);
                      setPicked({});
                    }}
                    onChange={(id, value) => setPicked((cur) => ({ ...cur, [id]: value }))}
                    disabled={busy}
                    initialOpen={open === 'model'}
                  />
                ) : null}
              </>
            }
            sendLabel={busy ? t('Starting…') : t('Start')}
            providerName={name}
            completions={{
              commands,
              online: !!target?.online,
              deviceName: machine,
              ...(props.fixture?.completions
                ? {
                    fixture: {
                      ...(props.fixture.completions.selected !== undefined
                        ? { selected: props.fixture.completions.selected }
                        : {}),
                      ...(props.fixture.completions.hovered !== undefined
                        ? { hovered: props.fixture.completions.hovered }
                        : {}),
                    },
                  }
                : {}),
            }}
            busy={busy}
            error={error}
            restore={restore}
            voiceAvailable={props.voiceAvailable}
            {...(props.fixture?.prompt ? { initialText: props.fixture.prompt } : {})}
            onSend={(text, attachments) => void start(text, attachments)}
            acceptsImages={caps?.images === true}
            {...(target?.deviceId
              ? {
                  upload: (image: PromptImage) => postAttachment(target.deviceId ?? '', image),
                  uploadKey: target.deviceId,
                }
              : {})}
            onStop={() => {}}
            onRetry={() => {}}
            onHandoff={() => {}}
            onDirty={() => setError(null)}
          />
        </div>
      </div>
    </div>
  );
}
