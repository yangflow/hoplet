import {
  type ConfigOption,
  type ConfigSetting,
  type DeviceInfo,
  MAX_PLACEHOLDERS,
  MAX_TEMPLATE_TEXT_CHARS,
  MAX_TEMPLATES,
  type Placeholder,
  type PlaceholderProblem,
  type ProviderInfo,
  parseTemplate,
  type SessionConfig,
  type Template,
  type TemplateBody,
  type TemplateFile,
  type TemplateImportResult,
} from '@hoplet/shared';
import { ApiError } from './errors.ts';
import { t } from './i18n.ts';

export { MAX_TEMPLATE_TEXT_CHARS, MAX_TEMPLATES };

/** `{name}` in a sentence is replaced by the value of that name. */
export function fill(text: string, values: Record<string, string | number>): string {
  return text.replace(/\{(\w+)\}/g, (all, key: string) =>
    key in values ? String(values[key]) : all,
  );
}

/** The 1-based line and column of a 0-based offset into `text`. */
export function positionOf(text: string, offset: number): { line: number; column: number } {
  const before = text.slice(0, offset);
  const line = before.split('\n').length;
  return { line, column: offset - (before.lastIndexOf('\n') + 1) + 1 };
}

/** The `{{…}}` at `offset`, as far as it goes on that line, for the sentence that names it. */
function tokenAt(text: string, offset: number): string {
  const rest = text.slice(offset).split('\n')[0] ?? '';
  const close = rest.indexOf('}}');
  return close >= 0 ? rest.slice(0, close + 2) : rest.slice(0, 12);
}

/** What is wrong with a placeholder, where it is, in a sentence. */
export function placeholderMessage(
  text: string,
  problem: PlaceholderProblem,
  offset: number,
): string {
  const { line, column } = positionOf(text, offset);
  if (problem === 'too-many') {
    return fill(t('Line {line}, column {column}: a prompt can have at most {max} placeholders.'), {
      line,
      column,
      max: MAX_PLACEHOLDERS,
    });
  }
  return fill(
    t(
      'Line {line}, column {column}: {token} is not a placeholder. A name is lower case letters, digits and _. Write \\{{ for a literal {{.',
    ),
    { line, column, token: tokenAt(text, offset) },
  );
}

/** The field a refusal belongs to; `form` is for what no field owns. */
export type TemplateField = 'name' | 'text' | 'machines' | 'form';

/** A refusal of the core, shown at its field. */
export type TemplateProblem = { field: TemplateField; message: string };

/**
 * Reads the `reason` of an `invalid-request` for a template: `name`, `text:<problem>:<offset>`, and for an import
 * `file.<index>.text:<problem>:<offset>` and `machines`. `text` is the prompt of the template (or of the entry
 * of `file`) the offset points into.
 */
export function templateReason(
  reason: string | undefined,
  source: { text?: string; file?: TemplateFile },
): TemplateProblem | null {
  if (reason === 'name') {
    return { field: 'name', message: t('A template with this name exists. Choose another name.') };
  }
  if (reason === 'machines') {
    return {
      field: 'machines',
      message: t('One of the machines you chose does not exist on this host.'),
    };
  }
  const entry = /^(?:file\.(\d+)\.)?text:(syntax|too-many):(\d+)$/.exec(reason ?? '');
  if (!entry) return null;
  const problem = entry[2] as PlaceholderProblem;
  const offset = Number(entry[3]);
  if (entry[1] !== undefined) {
    const index = Number(entry[1]);
    const item = source.file?.templates[index];
    const where = fill(t('Template {number}, {name}.'), {
      number: index + 1,
      name: item?.name ?? '',
    });
    return {
      field: 'form',
      message: `${where} ${placeholderMessage(item?.text ?? '', problem, offset)}`,
    };
  }
  return { field: 'text', message: placeholderMessage(source.text ?? '', problem, offset) };
}

/** What to show for a failed template request, and at which field. */
export function templateProblem(
  err: unknown,
  describe: (err: unknown) => string,
  source: { text?: string; file?: TemplateFile } = {},
): TemplateProblem {
  if (err instanceof ApiError && err.code === 'invalid-request') {
    const found = templateReason(err.reason, source);
    if (found) return found;
  }
  if (err instanceof ApiError && err.code === 'too-large') {
    return {
      field: 'form',
      message: fill(t('A host keeps at most {max} templates.'), { max: MAX_TEMPLATES }),
    };
  }
  return { field: 'form', message: describe(err) };
}

/** The values the editor holds. */
export type TemplateDraft = {
  /** null for a new template. */
  id: string | null;
  name: string;
  text: string;
  deviceId: string;
  projectPath: string;
  provider: 'claude' | 'codex';
  /** The configuration's ID; the vendor's own one is the vendor's name. */
  configuration: string;
  /** Only the options the user set; the others keep the default. */
  config: SessionConfig;
};

export function newDraft(provider: 'claude' | 'codex' = 'claude'): TemplateDraft {
  return {
    id: null,
    name: '',
    text: '',
    deviceId: '',
    projectPath: '',
    provider,
    configuration: provider,
    config: {},
  };
}

/** The draft of a template; a copy has no ID and says so in its name. */
export function draftOf(template: Template, copy = false): TemplateDraft {
  return {
    id: copy ? null : template.id,
    name: copy ? fill(t('{name} (copy)'), { name: template.name }) : template.name,
    text: template.text,
    deviceId: template.deviceId ?? '',
    projectPath: template.projectPath ?? '',
    provider: template.provider,
    configuration: template.configuration ?? template.provider,
    config: { ...(template.config ?? {}) },
  };
}

/** The request body of a draft. */
export function toBody(d: TemplateDraft): TemplateBody {
  return {
    name: d.name.trim(),
    text: d.text.trim(),
    ...(d.deviceId ? { deviceId: d.deviceId } : {}),
    ...(d.projectPath ? { projectPath: d.projectPath } : {}),
    provider: d.provider,
    ...(d.configuration !== d.provider ? { configuration: d.configuration } : {}),
    ...(Object.keys(d.config).length > 0 ? { config: d.config } : {}),
  };
}

export function formatCount(n: number): string {
  return n.toLocaleString('en-US');
}

/** What the editor says about the fields before the core is asked; null when the draft can be saved. */
export function validateDraft(d: TemplateDraft): { name?: string; text?: string } | null {
  const out: { name?: string; text?: string } = {};
  const name = d.name.trim();
  if (name === '') out.name = t('Give the template a name.');
  else if (name.length > 80) out.name = t('A name has at most 80 characters.');
  const text = d.text.trim();
  if (text === '') out.text = t('Write the prompt.');
  else if (text.length > MAX_TEMPLATE_TEXT_CHARS) {
    out.text = fill(t('A prompt has at most {max} characters.'), {
      max: formatCount(MAX_TEMPLATE_TEXT_CHARS),
    });
  } else {
    const parsed = parseTemplate(text);
    if (!parsed.ok) out.text = placeholderMessage(text, parsed.problem, parsed.position);
  }
  return out.name || out.text ? out : null;
}

/** The placeholders of a prompt in the order of first use; none while it does not parse. */
export function placeholdersOf(text: string): Placeholder[] {
  const parsed = parseTemplate(text);
  return parsed.ok ? parsed.placeholders : [];
}

/** The final path segment of a project path. */
export function baseName(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/** The key of a provider configuration in a list. */
export function providerKey(p: { provider: string; configuration: { id: string } }): string {
  return `${p.provider}/${p.configuration.id}`;
}

/** The enabled provider configurations a draft can choose: of the machine, else of every machine. */
export function providersOf(devices: readonly DeviceInfo[], deviceId: string): ProviderInfo[] {
  const pool = deviceId ? devices.filter((d) => d.deviceId === deviceId) : [...devices];
  const seen = new Map<string, ProviderInfo>();
  for (const device of pool) {
    for (const p of device.providers) {
      if (!p.configuration.enabled) continue;
      const key = providerKey(p);
      if (!seen.has(key)) seen.set(key, p);
    }
  }
  return [...seen.values()];
}

/** The options of a configuration that the editor can set. */
export function optionsOf(info: ProviderInfo | undefined): ConfigOption[] {
  return (info?.capabilities.config ?? []).filter(
    (o) => o.type === 'select' || o.type === 'boolean',
  );
}

/** The setting an option shows: what the draft set, else the configuration's default, else the driver's. */
export function settingOf(
  info: ProviderInfo | undefined,
  option: ConfigOption,
  config: SessionConfig,
): ConfigSetting {
  return config[option.id] ?? info?.configuration.defaults[option.id] ?? option.currentValue;
}

/** A placeholder as it is written: `{{pr}}` and `{{repo:hoplet}}`. */
export function placeholderLabel(p: Placeholder): string {
  return p.default === undefined ? `{{${p.name}}}` : `{{${p.name}:${p.default}}}`;
}

/**
 * What an import of `file` would do, the way the core answers a dry run; for fixtures, which keep their changes
 * locally. A taken name gets ` (2)`, the next free number after it.
 */
export function planImport(
  existing: readonly Template[],
  devices: readonly DeviceInfo[],
  file: TemplateFile,
  machines: Record<string, string> = {},
): TemplateImportResult {
  const taken = new Set(existing.map((x) => x.name));
  const items = file.templates.map((entry) => {
    let name = entry.name;
    for (let n = 2; taken.has(name); n += 1) name = `${entry.name} (${n})`;
    taken.add(name);
    const byName = entry.device
      ? devices.find((d) => d.name === entry.device)?.deviceId
      : undefined;
    const deviceId = byName ?? (entry.device ? machines[entry.device] : undefined);
    return {
      name,
      renamed: name !== entry.name,
      ...(entry.device ? { device: entry.device } : {}),
      ...(deviceId ? { deviceId } : {}),
    };
  });
  return { dryRun: true, items, templates: [] };
}

/** The file name of an export made on `date`. */
export function exportFileName(date: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `hoplet-templates-${date.getFullYear()}-${p(date.getMonth() + 1)}-${p(date.getDate())}.json`;
}

/** Offers `data` as a download; false where the browser cannot (a test). */
export function downloadJson(name: string, data: unknown): boolean {
  if (typeof URL.createObjectURL !== 'function') return false;
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
  return true;
}
