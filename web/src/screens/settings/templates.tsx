import {
  type ConfigOption,
  type DeviceInfo,
  safeParse,
  type Template,
  type TemplateFile,
  TemplateFileSchema,
  type TemplateImportResult,
  type TemplateListResponse,
} from '@hoplet/shared';
import { type ChangeEvent, type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { MachineIcon } from '../../components/MachineIcon.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { Toast, useToast } from '../../components/Toast.tsx';
import { fetchDeviceProjects } from '../../lib/api.ts';
import { optionName, valueName } from '../../lib/config-options.ts';
import { t } from '../../lib/i18n.ts';
import { systemOf } from '../../lib/system.ts';
import {
  baseName,
  downloadJson,
  draftOf,
  exportFileName,
  fill,
  formatCount,
  MAX_TEMPLATE_TEXT_CHARS,
  MAX_TEMPLATES,
  newDraft,
  optionsOf,
  placeholderLabel,
  placeholdersOf,
  providerKey,
  providersOf,
  settingOf,
  type TemplateDraft,
  toBody,
  validateDraft,
} from '../../lib/templates.ts';
import { useDevices } from '../../lib/use-devices.ts';
import { type TemplatesState, useTemplates } from '../../lib/use-templates.ts';
import './templates.css';

/** What the development fixtures hand to Settings > Templates. */
export type TemplatesFixture = {
  list: TemplateListResponse;
  devices: DeviceInfo[];
  /** The directories each machine offers, by device ID. */
  projects: Record<string, string[]>;
  /** The ID of the template whose form starts open, or `new`. */
  editing?: string;
  /** An import that starts open: the file and the name it came from. */
  importing?: { file: TemplateFile; fileName: string; machines?: Record<string, string> };
  /** A toast that starts on screen. */
  toast?: string;
};

/** The directories of a machine: the fixture's, else what its agent lists. */
function useProjects(deviceId: string, fixture?: Record<string, string[]>): string[] {
  const [fetched, setFetched] = useState<{ deviceId: string; paths: string[] } | null>(null);
  const fixed = fixture ? (fixture[deviceId] ?? []) : null;
  useEffect(() => {
    if (!deviceId || fixture) return;
    let live = true;
    fetchDeviceProjects(deviceId)
      .then((r) => live && setFetched({ deviceId, paths: r ? r.projects.map((p) => p.path) : [] }))
      .catch(() => live && setFetched({ deviceId, paths: [] }));
    return () => {
      live = false;
    };
  }, [deviceId, fixture]);
  if (!deviceId) return [];
  if (fixed) return fixed;
  return fetched?.deviceId === deviceId ? fetched.paths : [];
}

function MachineName(props: { device: DeviceInfo | undefined; fallback: string }) {
  const d = props.device;
  return (
    <>
      <MachineIcon kind={systemOf(d?.system?.os)} online={d?.connected ?? false} />
      {d?.name ?? props.fallback}
    </>
  );
}

/** The model is named first, then the switches, then the other selects such as the permission mode. */
function inWords(options: ConfigOption[]): ConfigOption[] {
  const rank = (o: ConfigOption) => (o.id === 'model' ? 0 : o.type === 'boolean' ? 1 : 2);
  return options
    .map((o, i) => ({ o, i }))
    .sort((x, y) => rank(x.o) - rank(y.o) || x.i - y.i)
    .map((x) => x.o);
}

/** The options a template sets, in the words of the picker: `Opus 5.5 · Thinking · Manual`. */
function optionsText(
  template: Pick<Template, 'provider' | 'configuration' | 'config' | 'deviceId'>,
  devices: readonly DeviceInfo[],
  only?: string,
): string {
  const info = providersOf(devices, template.deviceId ?? '').find(
    (p) =>
      p.provider === template.provider &&
      p.configuration.id === (template.configuration ?? template.provider),
  );
  const words: string[] = [];
  for (const option of inWords(optionsOf(info))) {
    if (only !== undefined && option.id !== only) continue;
    const set = template.config?.[option.id];
    if (set === undefined) continue;
    if (option.type === 'boolean') {
      if (set === true) words.push(optionName(option));
    } else if (option.type === 'select') {
      const found = option.options.find((o) => o.value === set);
      words.push(found ? valueName(option, found.value, found.name) : String(set));
    }
  }
  return words.join(' · ');
}

function TemplateRow(props: {
  template: Template;
  devices: readonly DeviceInfo[];
  busy: boolean;
  onEdit: () => void;
  onCopy: () => void;
  onDelete: () => void;
}) {
  const { template: x, devices } = props;
  const device = devices.find((d) => d.deviceId === x.deviceId);
  const chosen = x.deviceId !== undefined;
  const options = optionsText(x, devices);
  return (
    <div className="pr" data-testid={`settings.template.${x.id}`}>
      <span className="lead">{Icon.template()}</span>
      <div className="nm">
        <b>{x.name}</b>
        <span>{x.text}</span>
      </div>
      <span className="tp-at">
        {chosen ? (
          <>
            <MachineName device={device} fallback={x.deviceId ?? ''} />
            {x.projectPath ? (
              <>
                <span className="sl">/</span>
                <span className="pj">{baseName(x.projectPath)}</span>
              </>
            ) : null}
          </>
        ) : (
          t('Ask when starting')
        )}
      </span>
      <span className="tpo" title={options}>
        {options}
      </span>
      <div className="acts">
        <IconButton
          label={t('Edit')}
          icon={Icon.pen()}
          onClick={props.onEdit}
          data-testid="settings.templates.edit"
        />
        <IconButton
          label={t('Duplicate')}
          icon={Icon.copy()}
          onClick={props.onCopy}
          data-testid="settings.templates.duplicate"
        />
        <IconButton
          label={t('Delete')}
          icon={Icon.trash()}
          disabled={props.busy}
          onClick={props.onDelete}
          data-testid="settings.templates.remove"
        />
      </div>
    </div>
  );
}

function TemplateForm(props: {
  draft: TemplateDraft;
  devices: readonly DeviceInfo[];
  projects: Record<string, string[]> | undefined;
  state: TemplatesState;
  onChange: (draft: TemplateDraft) => void;
  onClose: () => void;
  onSave: () => void;
  onDelete: () => void;
}) {
  const { draft, devices, state } = props;
  const [touched, setTouched] = useState(false);
  const first = useRef<HTMLDivElement>(null);
  useEffect(() => {
    first.current?.querySelector<HTMLElement>('input')?.focus();
  }, []);
  const paths = useProjects(draft.deviceId, props.projects);
  const providers = providersOf(devices, draft.deviceId);
  const key = `${draft.provider}/${draft.configuration}`;
  const info = providers.find((p) => providerKey(p) === key) ?? providers[0];
  const options = optionsOf(info);
  const set = (change: Partial<TemplateDraft>) => props.onChange({ ...draft, ...change });
  const setOption = (id: string, value: string | boolean) =>
    set({ config: { ...draft.config, [id]: value } });
  const bad = validateDraft(draft);
  const problem = state.problem;
  const nameError =
    (touched ? bad?.name : undefined) ?? (problem?.field === 'name' ? problem.message : undefined);
  const textError =
    (touched ? bad?.text : undefined) ?? (problem?.field === 'text' ? problem.message : undefined);
  const formError =
    problem && problem.field !== 'name' && problem.field !== 'text' ? problem.message : null;
  const placeholders = placeholdersOf(draft.text);
  const left = MAX_TEMPLATE_TEXT_CHARS - draft.text.length;
  const projects =
    draft.projectPath && !paths.includes(draft.projectPath) ? [...paths, draft.projectPath] : paths;
  const heading = draft.id === null ? t('New template') : t('Edit template');
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== 'Escape' || e.defaultPrevented || e.nativeEvent.isComposing) return;
    e.preventDefault();
    props.onClose();
  };
  const ask = { value: '', label: t('Ask when starting') };
  const booleans = options.filter((o) => o.type === 'boolean');
  const selects = inWords(options).filter((o) => o.type === 'select');
  return (
    // biome-ignore lint/a11y/useSemanticElements: a group of fields, as in the mockup
    <div
      role="group"
      aria-label={heading}
      onKeyDown={onKeyDown}
      data-testid="settings.templates.form"
    >
      <div className="tp-form" ref={first}>
        <div className="tfl">
          <TextField
            label={t('Name')}
            showLabel
            value={draft.name}
            aria-invalid={nameError !== undefined}
            onChange={(e) => {
              state.clearProblem();
              set({ name: e.target.value });
            }}
            onBlur={() => setTouched(true)}
            data-testid="settings.templates.name"
          />
          {nameError ? (
            <p className="tp-note bad" role="alert" data-testid="settings.templates.name-error">
              {nameError}
            </p>
          ) : null}
        </div>
        <div className="tfl">
          <span className="l">
            {t('Prompt')}
            <em className={left < 0 ? 'bad' : undefined}>
              {fill(t('{count} of {max} characters left'), {
                count: formatCount(left),
                max: formatCount(MAX_TEMPLATE_TEXT_CHARS),
              })}
            </em>
          </span>
          <TextField
            multiline
            label={t('Prompt')}
            value={draft.text}
            aria-invalid={textError !== undefined}
            onChange={(e) => {
              state.clearProblem();
              set({ text: e.target.value });
            }}
            onBlur={() => setTouched(true)}
            data-testid="settings.templates.text"
          />
          {textError ? (
            <p className="tp-note bad" role="alert" data-testid="settings.templates.text-error">
              {textError}
            </p>
          ) : null}
          {placeholders.length > 0 ? (
            <div className="tp-toks" data-testid="settings.templates.placeholders">
              {placeholders.map((p) => (
                <span key={p.name} className="tp-tok">
                  {placeholderLabel(p)}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        <div className="tp-help">
          <span>
            <code>{'{{name}}'}</code> {t('asks for a value when the session starts')}
          </span>
          <span>
            <code>{'{{name:default}}'}</code> {t('fills in a default')}
          </span>
          <span>
            <code>{'\\{{'}</code> {t('is a literal')} <code>{'{{'}</code>
          </span>
        </div>
        <div className="half">
          <div className="tfl">
            <span className="l">{t('Machine')}</span>
            <Select
              label={t('Machine')}
              value={draft.deviceId}
              options={[
                ask,
                ...devices.map((d) => ({
                  value: d.deviceId,
                  label: d.name,
                  leading: <MachineIcon kind={systemOf(d.system?.os)} online={d.connected} />,
                })),
              ]}
              onChange={(deviceId) => set({ deviceId, projectPath: '' })}
              data-testid="settings.templates.machine"
            />
          </div>
          <div className="tfl">
            <span className="l">{t('Directory')}</span>
            <Select
              label={t('Directory')}
              value={draft.projectPath}
              disabled={draft.deviceId === ''}
              options={[
                ask,
                ...projects.map((p) => ({
                  value: p,
                  label: baseName(p),
                  leading: Icon.folder(),
                  detail: p,
                })),
              ]}
              onChange={(projectPath) => set({ projectPath })}
              data-testid="settings.templates.directory"
            />
          </div>
        </div>
        <div className="half">
          <div className="tfl">
            <span className="l">{t('Configuration')}</span>
            <Select
              label={t('Configuration')}
              value={info ? providerKey(info) : key}
              disabled={providers.length === 0}
              options={
                providers.length > 0
                  ? providers.map((p) => ({
                      value: providerKey(p),
                      label: p.configuration.name,
                      leading: <ProviderMark provider={p.provider} />,
                    }))
                  : [
                      {
                        value: key,
                        label: draft.configuration,
                        leading: <ProviderMark provider={draft.provider} />,
                      },
                    ]
              }
              onChange={(next) => {
                const p = providers.find((x) => providerKey(x) === next);
                if (p) set({ provider: p.provider, configuration: p.configuration.id, config: {} });
              }}
              data-testid="settings.templates.configuration"
            />
          </div>
          {selects.map((o) => (
            <div className="tfl" key={o.id}>
              <span className="l">{optionName(o)}</span>
              <Select
                label={optionName(o)}
                value={String(settingOf(info, o, draft.config))}
                options={o.options.map((v) => ({
                  value: v.value,
                  label: valueName(o, v.value, v.name),
                  leading: o.category === 'mode' ? Icon.shield() : null,
                }))}
                onChange={(value) => setOption(o.id, value)}
                data-testid={`settings.templates.option.${o.id}`}
              />
            </div>
          ))}
          {booleans.length > 0 ? (
            <div className="tfl">
              <span className="l">{t('Options')}</span>
              <div className="tp-sum">
                {booleans.map((o) => (
                  <Switch
                    key={o.id}
                    label={optionName(o)}
                    icon={
                      o.category === 'thought_level'
                        ? Icon.thinking()
                        : o.category === 'model_config'
                          ? Icon.fast()
                          : undefined
                    }
                    checked={settingOf(info, o, draft.config) === true}
                    onChange={(on) => setOption(o.id, on)}
                    data-testid={`settings.templates.option.${o.id}`}
                  />
                ))}
              </div>
            </div>
          ) : null}
        </div>
        {formError ? (
          <p className="tp-note bad" role="alert" data-testid="settings.templates.error">
            {formError}
          </p>
        ) : null}
        <div className="tp-foot">
          <Button
            variant="primary"
            disabled={state.saving}
            onClick={() => {
              setTouched(true);
              if (bad === null) props.onSave();
            }}
            data-testid="settings.templates.save"
          >
            {t('Save')}
          </Button>
          <Button variant="quiet" onClick={props.onClose} data-testid="settings.templates.cancel">
            {t('Cancel')}
          </Button>
          {draft.id !== null ? (
            <Button
              variant="quiet"
              className="del"
              disabled={state.saving}
              onClick={props.onDelete}
              data-testid="settings.templates.delete"
            >
              {t('Delete')}
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

type Importing = { file: TemplateFile; fileName: string; machines: Record<string, string> };

/** The names an import file gives to machines this host does not have. */
function unmatchedDevices(file: TemplateFile, devices: readonly DeviceInfo[]): string[] {
  const names = file.templates.flatMap((x) => (x.device ? [x.device] : []));
  return [...new Set(names)].filter((n) => !devices.some((d) => d.name === n));
}

function ImportPanel(props: {
  importing: Importing;
  devices: readonly DeviceInfo[];
  state: TemplatesState;
  onMachines: (machines: Record<string, string>) => void;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { importing, devices, state } = props;
  const [result, setResult] = useState<TemplateImportResult | null>(null);
  const { preview } = state;
  const mapping = JSON.stringify(importing.machines);
  // biome-ignore lint/correctness/useExhaustiveDependencies: `mapping` stands for `importing.machines`
  useEffect(() => {
    let live = true;
    void preview(importing.file, importing.machines).then((r) => {
      if (live) setResult(r);
    });
    return () => {
      live = false;
    };
  }, [preview, importing.file, mapping]);
  const unmatched = unmatchedDevices(importing.file, devices);
  const items = result?.items ?? [];
  const problem = state.problem;
  return (
    <div data-testid="settings.templates.import">
      <p className="tp-note before">
        {`${importing.fileName} · v${importing.file.v} · `}
        {fill(t('{count} templates. Nothing starts and no credential is read.'), {
          count: importing.file.templates.length,
        })}
      </p>
      <div className="plist tp-imp" data-testid="settings.templates.import-list">
        {items.map((item, i) => {
          const entry = importing.file.templates[i];
          const missing = item.device !== undefined && item.deviceId === undefined;
          const model = entry
            ? optionsText(
                { provider: entry.provider, config: entry.config ?? {}, deviceId: undefined },
                devices,
                'model',
              )
            : '';
          const where =
            [item.device, entry?.project ? baseName(entry.project) : undefined]
              .filter(Boolean)
              .join(' / ') || t('Ask when starting');
          return (
            <div className="pr" key={item.name}>
              <span className="lead">{Icon.template()}</span>
              <div className="nm">
                <b>{item.name}</b>
                <span>{model ? `${where} · ${model}` : where}</span>
              </div>
              <span className="tst">
                {item.renamed ? fill(t('Renamed to “{name}”'), { name: item.name }) : t('New')}
              </span>
              {missing ? (
                <span className="tst warn">
                  {Icon.alert()}
                  {fill(t('No machine named {name} here'), { name: item.device ?? '' })}
                </span>
              ) : null}
            </div>
          );
        })}
      </div>
      {unmatched.length > 0 ? (
        <div className="tp-maps">
          {unmatched.map((name) => (
            <div className="tfl" key={name}>
              <span className="l">{fill(t('Run “{device}” templates on'), { device: name })}</span>
              <Select
                label={fill(t('Run “{device}” templates on'), { device: name })}
                value={importing.machines[name] ?? ''}
                options={[
                  { value: '', label: t('Ask when starting') },
                  ...devices.map((d) => ({
                    value: d.deviceId,
                    label: d.name,
                    leading: <MachineIcon kind={systemOf(d.system?.os)} online={d.connected} />,
                  })),
                ]}
                onChange={(id) => {
                  const next = { ...importing.machines };
                  if (id) next[name] = id;
                  else delete next[name];
                  props.onMachines(next);
                }}
                data-testid="settings.templates.import-machine"
              />
            </div>
          ))}
        </div>
      ) : null}
      {problem ? (
        <p className="tp-note bad after" role="alert" data-testid="settings.templates.error">
          {problem.message}
        </p>
      ) : null}
      <div className="tp-foot-row">
        <Button
          variant="primary"
          disabled={state.saving || result === null}
          onClick={props.onConfirm}
          data-testid="settings.templates.import-confirm"
        >
          {fill(t('Import {count} templates'), { count: importing.file.templates.length })}
        </Button>
        <Button
          variant="quiet"
          onClick={props.onCancel}
          data-testid="settings.templates.import-cancel"
        >
          {t('Cancel')}
        </Button>
      </div>
    </div>
  );
}

/** Settings > Templates: the session templates of the host, where they are written, changed, imported and exported. */
export function TemplatesSection(props: { fixture?: TemplatesFixture | undefined }) {
  const { fixture } = props;
  const devices = useDevices(fixture?.devices);
  const state = useTemplates(fixture?.list, devices);
  const list = state.data?.templates ?? [];
  const [editing, setEditing] = useState<TemplateDraft | null>(() => {
    if (fixture?.editing === undefined) return null;
    if (fixture.editing === 'new') return newDraft();
    const found = fixture.list.templates.find((x) => x.id === fixture.editing);
    return found ? draftOf(found) : null;
  });
  const [importing, setImporting] = useState<Importing | null>(() =>
    fixture?.importing ? { machines: {}, ...fixture.importing } : null,
  );
  const [fileError, setFileError] = useState<string | null>(null);
  const [toast, say] = useToast(fixture?.toast ? { text: fixture.toast } : null);
  const picker = useRef<HTMLInputElement>(null);

  if (state.loading && !state.data) {
    return (
      <p className="rmeta" role="status">
        {t('Loading…')}
      </p>
    );
  }
  if (!state.data) {
    return (
      <p className="rmeta" role="alert" data-testid="settings.templates.load-error">
        {state.error}
      </p>
    );
  }

  const close = () => {
    state.clearProblem();
    setEditing(null);
  };
  const startEdit = (draft: TemplateDraft) => {
    state.clearProblem();
    setEditing(draft);
  };
  const save = async () => {
    if (!editing) return;
    const body = toBody(editing);
    if (await state.save(editing.id, body)) {
      setEditing(null);
      say(fill(t('Saved “{name}”'), { name: body.name }));
    }
  };
  const remove = async (x: Pick<Template, 'id' | 'name'>) => {
    if (await state.remove(x.id)) {
      setEditing(null);
      say(fill(t('Deleted “{name}”'), { name: x.name }));
    }
  };
  const exportAll = async () => {
    const file = await state.exportFile();
    if (!file) return;
    downloadJson(exportFileName(new Date()), file);
    say(fill(t('Exported {count} templates'), { count: file.templates.length }));
  };
  const pick = async (e: ChangeEvent<HTMLInputElement>) => {
    const chosen = e.target.files?.[0];
    e.target.value = '';
    if (!chosen) return;
    setFileError(null);
    try {
      const parsed = safeParse(TemplateFileSchema, JSON.parse(await chosen.text()));
      if (!parsed.success) throw new Error('shape');
      state.clearProblem();
      setImporting({ file: parsed.output, fileName: chosen.name, machines: {} });
    } catch {
      setFileError(t('This is not a Hoplet template file.'));
    }
  };
  const confirmImport = async () => {
    if (!importing) return;
    if (await state.importFile(importing.file, importing.machines)) {
      const count = importing.file.templates.length;
      setImporting(null);
      say(fill(t('Imported {count} templates'), { count }));
    }
  };

  const empty = list.length === 0 && !editing && !importing;
  return (
    <div className="tpw" data-testid="settings.templates">
      <div className="tp-head">
        <h1>
          {importing
            ? t('Import templates')
            : editing
              ? editing.id === null
                ? t('New template')
                : t('Edit template')
              : t('Templates')}
        </h1>
        {importing || editing ? null : (
          <>
            <Button
              icon={Icon.upload()}
              onClick={() => picker.current?.click()}
              data-testid="settings.templates.import-open"
            >
              {t('Import')}
            </Button>
            {empty ? null : (
              <Button
                icon={Icon.download()}
                onClick={() => void exportAll()}
                data-testid="settings.templates.export"
              >
                {t('Export')}
              </Button>
            )}
            <Button
              icon={Icon.plus()}
              onClick={() => startEdit(newDraft())}
              data-testid="settings.templates.add"
            >
              {t('New template')}
            </Button>
          </>
        )}
        <input
          ref={picker}
          type="file"
          accept=".json,application/json"
          hidden
          onChange={(e) => void pick(e)}
          data-testid="settings.templates.file"
        />
      </div>
      {fileError ? (
        <p className="tp-note bad before" role="alert" data-testid="settings.templates.file-error">
          {fileError}
        </p>
      ) : null}
      {importing ? (
        <ImportPanel
          importing={importing}
          devices={devices}
          state={state}
          onMachines={(machines) => setImporting({ ...importing, machines })}
          onCancel={() => {
            state.clearProblem();
            setImporting(null);
          }}
          onConfirm={() => void confirmImport()}
        />
      ) : empty ? (
        <div className="tp-empty" data-testid="settings.templates.empty">
          <span>
            {t(
              'A template keeps a prompt, where it runs and its options, so that one click starts the same session again.',
            )}
          </span>
          <span className="tp-note">
            {t(
              'Write {{name}} where a value changes each time. Save one from a draft or from a session’s ··· menu.',
            )}
          </span>
        </div>
      ) : editing ? (
        <TemplateForm
          draft={editing}
          devices={devices}
          projects={fixture?.projects}
          state={state}
          onChange={setEditing}
          onClose={close}
          onSave={() => void save()}
          onDelete={() => {
            const target = list.find((x) => x.id === editing.id);
            if (target) void remove(target);
          }}
        />
      ) : (
        <>
          <div className="plh meta">
            <span>{t('Template')}</span>
            <span>{t('Runs on · options')}</span>
          </div>
          <div className="plist tp-list" data-testid="settings.templates.list">
            {list.map((x) => (
              <TemplateRow
                key={x.id}
                template={x}
                devices={devices}
                busy={state.saving}
                onEdit={() => startEdit(draftOf(x))}
                onCopy={() => startEdit(draftOf(x, true))}
                onDelete={() => void remove(x)}
              />
            ))}
          </div>
          <p className="tp-note after">
            {fill(
              t('{count} of {max} templates. Placeholders are filled in when a session starts.'),
              { count: list.length, max: MAX_TEMPLATES },
            )}
          </p>
        </>
      )}
      {state.problem && !editing && !importing ? (
        <p className="tp-note bad after" role="alert" data-testid="settings.templates.error">
          {state.problem.message}
        </p>
      ) : null}
      {toast ? <Toast {...(toast.failed ? { failed: true } : {})}>{toast.text}</Toast> : null}
    </div>
  );
}
