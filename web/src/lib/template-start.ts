import {
  type ConfigSetting,
  fillTemplate,
  type Provider,
  type ProviderInfo,
  parseTemplate,
  type SessionConfig,
  type Template,
  type TemplateBody,
} from '@hoplet/shared';
import { optionName, valueName } from './config-options.ts';
import { ApiError } from './errors.ts';
import { t } from './i18n.ts';
import { baseName, fill, newDraft, toBody } from './templates.ts';

/** A piece of the prompt a template will send: literal text, or the value a placeholder has so far. */
export type PreviewPart = { text: string; field?: string };

/**
 * The prompt with `values` inserted. A placeholder with neither a value nor a default shows its name, marked as a
 * field, so the preview reads like the prompt that will be sent.
 */
export function previewParts(
  text: string,
  values: Readonly<Record<string, string>>,
): PreviewPart[] {
  const parsed = parseTemplate(text);
  if (!parsed.ok) return [{ text }];
  const defaults = new Map(parsed.placeholders.map((p) => [p.name, p.default]));
  return parsed.parts.map((part) => {
    if (part.kind === 'text') return { text: part.text };
    const given = values[part.name];
    const value = given === undefined || given === '' ? defaults.get(part.name) : given;
    return { text: value ?? part.name, field: part.name };
  });
}

/** The text a template puts in the draft: the placeholders filled as far as `values` and the defaults go. */
export function draftText(text: string, values: Readonly<Record<string, string>> = {}): string {
  const parsed = parseTemplate(text);
  if (!parsed.ok) return text;
  const defaults = new Map(parsed.placeholders.map((p) => [p.name, p.default]));
  return parsed.parts
    .map((part) => {
      if (part.kind === 'text') return part.text;
      const given = values[part.name];
      return given === undefined || given === ''
        ? (defaults.get(part.name) ?? `{{${part.name}}}`)
        : given;
    })
    .join('');
}

/** Whether every placeholder of `text` has a value or a default. */
export function isComplete(text: string, values: Readonly<Record<string, string>>): boolean {
  const parsed = parseTemplate(text);
  return parsed.ok && fillTemplate(parsed, values).ok;
}

/** What the machine a template would run on offers it. */
export type StartTarget = {
  deviceId: string | null;
  deviceName: string;
  online: boolean;
  /** The paths of the directories known on it. */
  dirs: readonly string[];
  /** The path the start would use. */
  path: string;
  /** The configuration that would run it. */
  provider: ProviderInfo | undefined;
  /** The enabled configurations of the machine. */
  providers: readonly ProviderInfo[];
  /** The options as they stand now: the configuration's defaults under what was picked. */
  config: SessionConfig;
};

/** What a one-click fix does. */
export type IssueFix =
  | { kind: 'set'; id: string; value: ConfigSetting; label: string }
  | { kind: 'accept'; label: string };

/** A way the template does not match where it would start. */
export type StartIssue = {
  /** `machine`, `project`, `configuration` or `option:<id>`: what `accepted` names. */
  key: string;
  message: string;
  fix?: IssueFix;
};

/**
 * What of `template` the target cannot honour: its machine is gone or offline, its project is not on the machine,
 * its configuration is not enabled there, or an option it sets is not offered or has no such value. Nothing is
 * dropped silently: each is listed, with a fix when there is one, and an issue that is `accepted` no longer counts.
 * `machineKnown` is false when the template names a machine that is not on this host.
 */
export function startIssues(
  template: Template,
  target: StartTarget,
  accepted: ReadonlySet<string> = new Set(),
  machineKnown = true,
): StartIssue[] {
  const out: StartIssue[] = [];
  const add = (issue: StartIssue) => {
    if (!accepted.has(issue.key)) out.push(issue);
  };
  const here = target.deviceName;
  const onItsMachine = template.deviceId === undefined || target.deviceId === template.deviceId;

  if (template.deviceId !== undefined && !machineKnown) {
    add({
      key: 'machine',
      message: t('The machine of this template is not on this host.'),
      fix: { kind: 'accept', label: fill(t('Run on {machine}'), { machine: here }) },
    });
  } else if (onItsMachine && !target.online) {
    add({ key: 'machine', message: fill(t('{machine} is offline.'), { machine: here }) });
  }

  if (
    onItsMachine &&
    template.projectPath !== undefined &&
    !target.dirs.includes(template.projectPath)
  ) {
    add({
      key: 'project',
      message: fill(t('{project} is not a project on {machine}.'), {
        project: baseName(template.projectPath),
        machine: here,
      }),
      fix: {
        kind: 'accept',
        label: fill(t('Use {project}'), { project: baseName(target.path) }),
      },
    });
  }

  const wantedConfiguration = template.configuration ?? template.provider;
  if (!target.providers.some((p) => p.configuration.id === wantedConfiguration)) {
    add({
      key: 'configuration',
      message: fill(t('The configuration {name} is not enabled on {machine}.'), {
        name: wantedConfiguration,
        machine: here,
      }),
      fix: {
        kind: 'accept',
        label: fill(t('Use {name}'), { name: target.provider?.configuration.name ?? '' }),
      },
    });
  }

  for (const [id, wanted] of Object.entries(template.config ?? {})) {
    const option = target.provider?.capabilities.config.find((o) => o.id === id);
    const key = `option:${id}`;
    if (!option) {
      add({
        key,
        message: fill(t('{option} is not available on {machine}.'), { option: id, machine: here }),
        fix: { kind: 'accept', label: t('Start without it') },
      });
      continue;
    }
    if (option.type !== 'select' || option.options.some((v) => v.value === wanted)) continue;
    // Once the user picks another value the option is theirs.
    if (target.config[id] !== wanted) continue;
    const fallback = target.provider?.configuration.defaults[id] ?? option.currentValue;
    const entry = option.options.find((v) => v.value === fallback);
    add({
      key,
      message: fill(t('{option} {value} is not offered on {machine}.'), {
        option: optionName(option),
        value: String(wanted),
        machine: here,
      }),
      ...(entry
        ? {
            fix: {
              kind: 'set' as const,
              id,
              value: entry.value,
              label: fill(t('Use {value}'), { value: valueName(option, entry.value, entry.name) }),
            },
          }
        : {}),
    });
  }
  return out;
}

/** The sentence for a refused template start; null when the reason is not one this knows. */
export function startProblem(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  if (err.code === 'target-offline') return t('The machine is offline.');
  if (err.code !== 'invalid-request') return null;
  const reason = err.reason ?? '';
  if (reason.startsWith('values:missing:')) {
    return fill(t('Fill in {names}.'), { names: reason.slice('values:missing:'.length) });
  }
  if (reason === 'projectPath') return t('The project is not on the machine.');
  if (reason === 'deviceId') return t('The machine of this template is not on this host.');
  if (reason === 'config') return t('The machine does not offer an option of this template.');
  if (reason === 'text') return t('The prompt is longer than a prompt may be.');
  return null;
}

/** The number of fields of a template in words: `No fields` when it has none. */
export function fieldsLabel(template: Pick<Template, 'placeholders'>): string {
  const n = template.placeholders.length;
  if (n === 0) return t('No fields');
  return n === 1 ? t('1 field') : fill(t('{n} fields'), { n: String(n) });
}

/** What the picker shows under a template's name: where it runs, its model and its fields. */
export function templateDetail(
  template: Template,
  machines: readonly {
    deviceId: string | null;
    deviceName: string;
    providers: readonly ProviderInfo[];
  }[],
): string {
  const machine = machines.find((m) => m.deviceId === template.deviceId);
  const where = [machine?.deviceName, template.projectPath ? baseName(template.projectPath) : null]
    .filter((x): x is string => !!x)
    .join(' / ');
  const model = template.config?.model;
  let modelName: string | undefined;
  if (typeof model === 'string') {
    const wanted = template.configuration ?? template.provider;
    for (const m of machines) {
      const option = m.providers
        .find((p) => p.configuration.id === wanted)
        ?.capabilities.config.find((o) => o.id === 'model');
      const entry =
        option?.type === 'select' ? option.options.find((v) => v.value === model) : undefined;
      if (option && entry) {
        modelName = valueName(option, entry.value, entry.name);
        break;
      }
    }
    modelName ??= model;
  }
  return [where || t('Any machine'), modelName, fieldsLabel(template)]
    .filter((x): x is string => !!x)
    .join(' · ');
}

/** What of the draft's place and options a saved template keeps. */
export type KeepOptions = { where: boolean; options: boolean; mode: boolean };

/**
 * The body that saves the draft as a template. The configuration always goes with it, because a template names
 * the provider it runs; `keep` says whether the machine and directory, the options and the permission mode do.
 */
export function draftTemplateBody(input: {
  name: string;
  text: string;
  deviceId: string | null;
  path: string;
  provider: Provider;
  configuration: string;
  config: SessionConfig;
  /** The option that holds the permission mode, when the driver has one. */
  modeId?: string;
  keep: KeepOptions;
}): TemplateBody {
  const config: SessionConfig = {};
  for (const [id, value] of Object.entries(input.config)) {
    if (id === input.modeId ? input.keep.mode : input.keep.options) config[id] = value;
  }
  return toBody({
    ...newDraft(input.provider),
    name: input.name,
    text: input.text,
    deviceId: input.keep.where ? (input.deviceId ?? '') : '',
    projectPath: input.keep.where ? input.path : '',
    configuration: input.configuration,
    config,
  });
}
