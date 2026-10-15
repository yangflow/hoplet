import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../../components/icons.tsx';
import { providerName } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { Composer } from './composer/composer.tsx';
import { hasChoices, type ModelChoice, ModelMenu } from './composer/model-menu.tsx';
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

  const providers = target?.providers ?? [];
  const [picked, setPicked] = useState<ModelChoice>({
    provider: providers[0]?.provider ?? 'claude',
    model: '',
    thinking: true,
    fast: false,
  });
  const provider = providers.find((p) => p.provider === picked.provider) ?? providers[0];
  const caps = provider?.capabilities;
  const choice: ModelChoice = {
    ...picked,
    provider: provider?.provider ?? picked.provider,
    model: caps?.models.includes(picked.model) ? picked.model : (caps?.models[0] ?? ''),
  };

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [restore, setRestore] = useState<{ text: string; n: number } | null>(null);

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

  async function start(text: string) {
    if (!target?.deviceId || !provider || !caps || path === '') return;
    setBusy(true);
    setError(null);
    try {
      await props.onSubmit({
        deviceId: target.deviceId,
        deviceName: target.deviceName,
        path,
        provider: provider.provider,
        options: {
          ...(caps.models.length > 0 ? { model: choice.model } : {}),
          ...(caps.thinking ? { thinking: choice.thinking } : {}),
          ...(caps.fast ? { fast: choice.fast } : {}),
        },
        prompt: text,
      });
    } catch (err) {
      setError(errorText(err));
      setRestore((r) => ({ text, n: (r?.n ?? 0) + 1 }));
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
          detail={target?.online ? t('Machine · online') : t('Machine · offline')}
          value={machine}
          options={targets.map((x) => ({
            value: x.deviceName,
            label: x.deviceName,
            disabled: !x.online,
            leading: <span className={`dot${x.online ? '' : ' off'}`} />,
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
            options={
              hasChoices(providers, provider) ? (
                <ModelMenu
                  providers={providers}
                  value={choice}
                  onChange={setPicked}
                  disabled={busy}
                  initialOpen={open === 'model'}
                />
              ) : null
            }
            sendLabel={busy ? t('Starting…') : t('Start')}
            providerName={name}
            busy={busy}
            error={error}
            restore={restore}
            voiceAvailable={props.voiceAvailable}
            {...(props.fixture?.prompt ? { initialText: props.fixture.prompt } : {})}
            onSend={(text) => void start(text)}
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
