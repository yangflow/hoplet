import type {
  CommandsResult,
  ConfigOption,
  PromptImage,
  SessionConfig,
  Template,
  TemplateListResponse,
} from '@hoplet/shared';
import { useEffect, useMemo, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { providerName } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { postAttachment } from '../../lib/api.ts';
import { currentValueName, optionName } from '../../lib/config-options.ts';
import { notReady } from '../../lib/configurations.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { machineLabel, systemName, systemOf } from '../../lib/system.ts';
import {
  draftTemplateBody,
  draftText,
  type IssueFix,
  startIssues,
  startProblem,
  templateDetail,
} from '../../lib/template-start.ts';
import { baseName, fill } from '../../lib/templates.ts';
import { useTemplates } from '../../lib/use-templates.ts';
import { Composer } from './composer/composer.tsx';
import { ConfigurationMenu, showsConfigurationChip } from './composer/configuration-menu.tsx';
import { ModeMenu, modeOption, nextMode } from './composer/mode-menu.tsx';
import { hasChoices, ModelMenu, menuConfig, withValues } from './composer/model-menu.tsx';
import { FillDialog, SaveDialog } from './composer/template-dialogs.tsx';
import { type TemplateChoice, TemplatesMenu } from './composer/templates-menu.tsx';
import { useCommands } from './composer/use-completions.ts';
import { BranchPart, WhereRow } from './composer/where.tsx';
import type { NewSessionRequest, SessionTarget, TemplateStart } from './model.ts';
import type { ComposerView } from './runtime.ts';
import { type Dir, useDeviceDirs } from './use-device-dirs.ts';

/** Where a draft starts its session: a machine and one of its watched project directories. */
export type DraftWhere = { deviceName: string; path: string };

/** What development fixtures set: the text, and a picker or the model menu open. */
export type DraftFixture = {
  prompt?: string;
  open?: 'machine' | 'directory' | 'model' | 'configuration' | 'templates';
  /** The machine's directories as its catalog would list them. */
  dirs?: Dir[];
  /** The templates of the host; changes stay local. */
  templates?: TemplateListResponse;
  /** A template already applied: its ID, the values its fields had, and the dialog that is open. */
  applied?: { id: string; values?: Record<string, string>; dialog?: 'fill' };
  /** The save dialog is open, with this name typed. */
  save?: { name: string };
  /** What `/` shows, and the selected and hovered rows. */
  completions?: { commands: CommandsResult; selected?: number; hovered?: number };
};

/** The folder a directory sits in, as the picker shows it: the home directory reads `~`. */
export function folderOf(path: string): string {
  const parent = path.replace(/[/\\]+[^/\\]+[/\\]*$/, '') || '/';
  return parent.replace(/^\/(?:Users|home)\/[^/]+(?=\/|$)/, '~');
}

/** The options of the draft in words, without the permission mode: `Opus · Extended thinking`. */
export function optionsText(shown: readonly ConfigOption[]): string | null {
  const words = menuConfig(shown)
    .map((o) => (o.type === 'select' ? currentValueName(o) : o.currentValue ? optionName(o) : null))
    .filter((x): x is string => !!x);
  return words.length > 0 ? words.join(' · ') : null;
}

/** The machine, the folder and the options in words, as the fill dialog shows where a template will run. */
export function startContext(
  machine: string,
  path: string,
  shown: readonly ConfigOption[],
): { machine: string; folder: string; options: string } {
  const mode = modeOption(shown);
  return {
    machine,
    folder: baseName(path),
    options: [optionsText(shown), mode ? currentValueName(mode) : null]
      .filter((x): x is string => !!x)
      .join(' · '),
  };
}

/** A template to put on the draft once it is open, and the values its fields already have. */
export type DraftEntry = { template: Template; values?: Record<string, string> };

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
  /** Opens Settings › Model providers on the machine. */
  onManageProviders: (deviceId: string) => void;
  /** Starts a session from a template; resolves when it exists. */
  onStartTemplate: (request: TemplateStart) => Promise<void>;
  /** Opens Settings › Templates. */
  onManageTemplates: () => void;
  /** A template chosen outside the draft (the sidebar, the palette); applied once, then `onEntryUsed` is called. */
  entry?: DraftEntry | null;
  onEntryUsed?: () => void;
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
  // The enabled configurations of the machine; the first one that is ready starts the session until one is picked.
  const providers = target?.providers ?? [];
  const fx = props.fixture;
  const appliedAtStart = fx?.applied
    ? fx.templates?.templates.find((x) => x.id === fx.applied?.id)
    : undefined;
  const [pickedConfiguration, setPickedConfiguration] = useState<string | null>(
    appliedAtStart ? (appliedAtStart.configuration ?? appliedAtStart.provider) : null,
  );
  const [picked, setPicked] = useState<SessionConfig>(appliedAtStart?.config ?? {});
  const provider =
    providers.find((p) => p.configuration.id === pickedConfiguration) ??
    providers.find((p) => !notReady(p)) ??
    providers[0];
  const caps = provider?.capabilities;
  // The configuration's defaults prefill the options; the user's picks win. Only options the driver declares hold.
  const config: SessionConfig = Object.fromEntries(
    Object.entries({ ...provider?.configuration.defaults, ...picked }).filter(([id]) =>
      caps?.config.some((o) => o.id === id),
    ),
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

  // A template: the one applied to the draft, what the user accepted of its mismatches, and the dialog that is open.
  const templates = useTemplates(fx?.templates);
  const [tpl, setTpl] = useState<Template | null>(appliedAtStart ?? null);
  const [accepted, setAccepted] = useState<ReadonlySet<string>>(new Set());
  const [dialog, setDialog] = useState<'fill' | 'save' | null>(
    appliedAtStart && fx?.applied?.dialog ? 'fill' : fx?.save ? 'save' : null,
  );
  const [fillText, setFillText] = useState<{ text: string; n: number } | null>(
    appliedAtStart && !fx?.applied?.dialog
      ? { text: draftText(appliedAtStart.text, fx?.applied?.values), n: 1 }
      : null,
  );
  const [text, setText] = useState(fx?.prompt ?? '');
  const [startBusy, setStartBusy] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);
  const issues = tpl
    ? startIssues(
        tpl,
        {
          deviceId: target?.deviceId ?? null,
          deviceName: machine,
          online: !!target?.online,
          dirs: dirs.map((d) => d.path),
          path,
          provider,
          providers,
          config,
        },
        accepted,
        tpl.deviceId === undefined || targets.some((x) => x.deviceId === tpl.deviceId),
      )
    : [];

  /** Puts a template's place and options on the draft; a template with fields asks for them next unless `values` are given, one without fills the prompt. */
  function apply(template: Template, values?: Record<string, string>) {
    const there = targets.find(
      (x) => template.deviceId !== undefined && x.deviceId === template.deviceId,
    );
    const next = there?.deviceName ?? machine;
    const wanted = template.projectPath ?? (there ? '' : path);
    if (next !== machine || wanted !== path) onWhere({ deviceName: next, path: wanted });
    setPickedConfiguration(template.configuration ?? template.provider);
    setPicked(template.config ?? {});
    setTpl(template);
    setAccepted(new Set());
    setStartError(null);
    if (template.placeholders.length > 0 && !values) {
      setDialog('fill');
      return;
    }
    // Nothing to ask: the prompt goes in the composer and the user sends it when it suits.
    setFillText((cur) => ({ text: draftText(template.text, values ?? {}), n: (cur?.n ?? 0) + 1 }));
    setDialog(null);
  }

  const entry = props.entry;
  const entryUsed = props.onEntryUsed;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a chosen template is applied once, when it arrives
  useEffect(() => {
    if (!entry) return;
    apply(entry.template, entry.values);
    entryUsed?.();
  }, [entry]);

  function clearTemplate() {
    setTpl(null);
    setAccepted(new Set());
    setDialog(null);
  }

  function fix(issueKey: string, how: IssueFix | undefined) {
    if (how?.kind === 'set') setPicked((cur) => ({ ...cur, [how.id]: how.value }));
    else setAccepted((cur) => new Set(cur).add(issueKey));
  }

  async function startFromTemplate(values: Record<string, string>) {
    if (!tpl || !target?.deviceId || path === '') return;
    setStartBusy(true);
    setStartError(null);
    try {
      await props.onStartTemplate({ templateId: tpl.id, values, deviceId: target.deviceId, path });
    } catch (err) {
      setStartError(startProblem(err) ?? errorText(err));
      setStartBusy(false);
    }
  }

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
        ...(provider.configuration.id !== provider.provider
          ? { configuration: provider.configuration.id }
          : {}),
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

  const choices: TemplateChoice[] = (templates.data?.templates ?? []).map((template) => ({
    template,
    detail: templateDetail(
      template,
      targets.map((x) => ({
        deviceId: x.deviceId,
        deviceName: x.deviceName,
        providers: x.providers,
      })),
    ),
  }));

  const bar = tpl ? (
    <div className="tp-bar" data-testid="cockpit.template-bar">
      {Icon.template()}
      <span className="nm">{tpl.name}</span>
      <span className="tp-note">{fill(t('Runs on {machine}'), { machine })}</span>
      <span className="sp" />
      <IconButton
        label={t('Clear the template')}
        icon={Icon.close()}
        onClick={clearTemplate}
        data-testid="cockpit.template-clear"
      />
    </div>
  ) : null;

  const warning =
    tpl && issues.length > 0 ? (
      <div className="tp-warn" role="status" data-testid="cockpit.template-warning">
        {Icon.alert()}
        <div className="tx">
          <b>{fill(t('This template does not fit {machine}'), { machine })}</b>
          {issues.map((issue) => (
            <div className="tp-fix" key={issue.key}>
              <span className="what">{issue.message}</span>
              {issue.fix ? (
                <Button
                  variant="default"
                  onClick={() => fix(issue.key, issue.fix)}
                  data-testid="cockpit.template-fix"
                >
                  {issue.fix.label}
                </Button>
              ) : null}
            </div>
          ))}
        </div>
      </div>
    ) : null;

  const leading = (
    <>
      {provider && showsConfigurationChip(providers) && target?.deviceId ? (
        <ConfigurationMenu
          configurations={providers}
          value={provider.configuration.id}
          machine={machine}
          onChange={(id) => {
            setPickedConfiguration(id);
            setPicked({});
          }}
          onManage={() => props.onManageProviders(target.deviceId ?? '')}
          disabled={busy}
          initialOpen={open === 'configuration'}
        />
      ) : null}
      <TemplatesMenu
        choices={choices}
        loading={templates.loading}
        canSave={text.trim() !== '' && !!provider && !!target?.deviceId}
        onPick={apply}
        onSave={() => setDialog('save')}
        onManage={props.onManageTemplates}
        disabled={busy}
        initialOpen={open === 'templates'}
      />
    </>
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
            leading={leading}
            above={bar}
            below={warning}
            fill={fillText}
            onText={setText}
            sendBlocked={issues.length > 0}
            options={
              <>
                {mode ? (
                  <ModeMenu
                    option={mode}
                    onChange={(id, value) => setPicked((cur) => ({ ...cur, [id]: value }))}
                    disabled={busy}
                  />
                ) : null}
                {hasChoices(provider) ? (
                  <ModelMenu
                    provider={provider?.provider ?? 'claude'}
                    config={shown}
                    onChange={(id, value) => setPicked((cur) => ({ ...cur, [id]: value }))}
                    disabled={busy}
                    warn={issues.some((issue) => issue.key.startsWith('option:'))}
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
      {dialog === 'fill' && tpl ? (
        <FillDialog
          template={tpl}
          issues={issues}
          context={startContext(machine, path, shown)}
          busy={startBusy}
          error={startError}
          initial={fx?.applied?.values ?? {}}
          onStart={(values) => void startFromTemplate(values)}
          onEdit={(values) => {
            setFillText((cur) => ({ text: draftText(tpl.text, values), n: (cur?.n ?? 0) + 1 }));
            setDialog(null);
          }}
          onClose={clearTemplate}
        />
      ) : null}
      {dialog === 'save' ? (
        <SaveDialog
          text={text}
          initialName={fx?.save?.name ?? ''}
          summary={{
            where: `${machine} / ${baseName(path)}`,
            options: optionsText(shown),
            mode: mode ? (currentValueName(mode) ?? null) : null,
          }}
          saving={templates.saving}
          problem={templates.problem}
          onEdited={templates.clearProblem}
          onClose={() => {
            templates.clearProblem();
            setDialog(null);
          }}
          onSave={(input) => {
            if (!provider) return;
            void templates
              .save(
                null,
                draftTemplateBody({
                  name: input.name,
                  text: input.text,
                  deviceId: target?.deviceId ?? null,
                  path,
                  provider: provider.provider,
                  configuration: provider.configuration.id,
                  config,
                  ...(mode ? { modeId: mode.id } : {}),
                  keep: input.keep,
                }),
              )
              .then((ok) => {
                if (ok) setDialog(null);
              });
          }}
        />
      ) : null}
    </div>
  );
}
