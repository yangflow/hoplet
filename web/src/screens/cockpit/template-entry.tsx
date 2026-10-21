import type {
  ConfigOption,
  DeviceInfo,
  LiveSessionMeta,
  RuntimeCapabilities,
  SessionConfig,
  Template,
  TemplateListResponse,
} from '@hoplet/shared';
import { type ReactNode, useCallback, useMemo, useState } from 'react';
import { fetchLines } from '../../lib/api.ts';
import { currentValueName } from '../../lib/config-options.ts';
import { notReady } from '../../lib/configurations.ts';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import {
  draftTemplateBody,
  fieldsLabel,
  type StartIssue,
  type StartTarget,
  startIssues,
  startProblem,
  templateDetail,
} from '../../lib/template-start.ts';
import { baseName } from '../../lib/templates.ts';
import { useTemplates } from '../../lib/use-templates.ts';
import { modeOption } from './composer/mode-menu.tsx';
import { withValues } from './composer/model-menu.tsx';
import { FillDialog, SaveDialog } from './composer/template-dialogs.tsx';
import type { TemplateChoice } from './composer/templates-menu.tsx';
import { loadBlock } from './conversation/blocks.tsx';
import { PAGE_LINES } from './conversation/page-cache.ts';
import { buildTurns } from './conversation/turns.ts';
import { optionsText, startContext } from './draft.tsx';
import type { SessionTarget, TemplateStart } from './model.ts';

/** Development fixtures for /__states: the templates, and the dialogs to start open. */
export type TemplateEntryFixture = {
  templates: TemplateListResponse;
  /** Opens the fill dialog of this template, with these values in its fields. */
  fill?: { id: string; values?: Record<string, string> };
  /** Opens the save dialog for the open session with this name and prompt. */
  save?: { name: string; text: string };
  /** Opens the sidebar's New session menu. */
  menu?: boolean;
};

/** A template in the sidebar menu and the palette: the choice, and where it runs in words. */
export type EntryChoice = TemplateChoice & {
  /** `machine / project`, or "Any machine". */
  place: string;
  /** The machine alone, or "Any machine". */
  machine: string;
};

/** Where a start from a template would run, and what of the template that place cannot honour. */
export type StartPlan = {
  deviceId: string;
  machine: string;
  path: string;
  /** The options as words for the fill dialog. */
  options: ConfigOption[];
  issues: StartIssue[];
};

/**
 * Where `template` starts without the draft: the machine and project it names, when the machine is known and
 * online. `null` when it names no machine or project, or the machine is not known: the draft is where those are
 * chosen. The issues are what the machine does not offer (a project that is not on it, a missing model); a plan
 * with issues is also sent to the draft, which lists them with their fixes.
 */
export function planStart(template: Template, targets: readonly SessionTarget[]): StartPlan | null {
  const there = targets.find(
    (x) => template.deviceId !== undefined && x.deviceId === template.deviceId,
  );
  if (!there?.deviceId || !template.projectPath) return null;
  const wanted = template.configuration ?? template.provider;
  const provider =
    there.providers.find((p) => p.configuration.id === wanted) ??
    there.providers.find((p) => !notReady(p)) ??
    there.providers[0];
  const caps = provider?.capabilities;
  const config: SessionConfig = Object.fromEntries(
    Object.entries({ ...provider?.configuration.defaults, ...template.config }).filter(([id]) =>
      caps?.config.some((o) => o.id === id),
    ),
  );
  const target: StartTarget = {
    deviceId: there.deviceId,
    deviceName: there.deviceName,
    online: there.online,
    dirs: there.dirs.map((d) => d.path),
    path: template.projectPath,
    provider,
    providers: there.providers,
    config,
  };
  return {
    deviceId: there.deviceId,
    machine: there.deviceName,
    path: template.projectPath,
    options: withValues(caps?.config, config),
    issues: startIssues(template, target),
  };
}

/** The template as the sidebar menu and the palette list it. */
export function entryChoices(
  templates: readonly Template[],
  targets: readonly SessionTarget[],
): EntryChoice[] {
  return templates.map((template) => {
    const machine = targets.find((x) => x.deviceId === template.deviceId)?.deviceName;
    const project = template.projectPath ? baseName(template.projectPath) : null;
    return {
      template,
      detail: templateDetail(template, targets),
      machine: machine ?? t('Any machine'),
      place: machine ? [machine, project].filter(Boolean).join(' / ') : t('Any machine'),
    };
  });
}

/** How many fields a template asks for, or that it starts at once when it has none. */
export function entryFields(template: Pick<Template, 'placeholders'>): string {
  return template.placeholders.length === 0 ? t('Starts at once') : fieldsLabel(template);
}

/** The words under a template's name in the sidebar menu: where it runs and how many fields it asks for. */
export function menuDetail(choice: Pick<EntryChoice, 'place' | 'template'>): string {
  return `${choice.place} · ${entryFields(choice.template)}`;
}

/** The first prompt of a session in full, or '' when it has none in its first page. */
export async function firstPrompt(sessionId: string): Promise<string> {
  const page = await fetchLines(sessionId, 0, PAGE_LINES);
  const prompt = buildTurns(page.entries).find((turn) => turn.prompt)?.prompt;
  if (!prompt) return '';
  const text = prompt.elided
    ? await loadBlock(sessionId, prompt.elided.line, prompt.elided.block)
    : prompt.text;
  return text.trim();
}

/** The options a session runs with, by option ID: what a template made from it keeps. */
export function sessionConfig(options: readonly ConfigOption[]): SessionConfig {
  const out: SessionConfig = {};
  for (const o of options) {
    if ((o.type === 'select' || o.type === 'boolean') && o.currentValue !== undefined) {
      out[o.id] = o.currentValue;
    }
  }
  return out;
}

export type TemplateEntry = {
  choices: EntryChoice[];
  /** Starts from the template: at once, after its fields are filled, or in the draft when the place needs a choice. */
  choose: (template: Template) => void;
  /** Opens the save dialog for a session, with its first prompt and its options. */
  saveAs: (session: LiveSessionMeta, capabilities: RuntimeCapabilities | undefined) => void;
  /** The fill and save dialogs; render them anywhere in the screen. */
  dialogs: ReactNode;
};

/**
 * The entries that start a session from a template or make a template without the draft: the sidebar's menu, the
 * palette and the session actions menu. They share the dialogs of the draft's Templates menu.
 */
export function useTemplateEntry(props: {
  targets: readonly SessionTarget[];
  devices: readonly DeviceInfo[];
  fixture?: TemplateEntryFixture | undefined;
  /** The open session, for the fixtures' save dialog. */
  session: LiveSessionMeta | null;
  capabilities: RuntimeCapabilities | undefined;
  startTemplate?: ((request: TemplateStart) => Promise<void>) | undefined;
  /** Puts the template on a new draft, with its fields' values when they are known. */
  openDraft: (template: Template, values?: Record<string, string>) => void;
  /** A session started from a template. */
  onStarted: () => void;
  say: (text: string, failed?: boolean) => void;
}): TemplateEntry {
  const { targets, fixture, say } = props;
  const templates = useTemplates(fixture?.templates, props.devices as DeviceInfo[]);
  const choices = useMemo(
    () => entryChoices(templates.data?.templates ?? [], targets),
    [templates.data, targets],
  );

  const [fill, setFill] = useState<{ template: Template; plan: StartPlan } | null>(() => {
    const template = fixture?.fill
      ? fixture.templates.templates.find((x) => x.id === fixture.fill?.id)
      : undefined;
    const plan = template ? planStart(template, targets) : null;
    return template && plan ? { template, plan } : null;
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [save, setSave] = useState<{
    session: LiveSessionMeta;
    text: string;
    options: ConfigOption[];
  } | null>(() =>
    fixture?.save && props.session
      ? {
          session: props.session,
          text: fixture.save.text,
          options: props.capabilities?.config ?? [],
        }
      : null,
  );

  const start = useCallback(
    async (template: Template, plan: StartPlan, values: Record<string, string>, form: boolean) => {
      setBusy(true);
      setError(null);
      try {
        await props.startTemplate?.({
          templateId: template.id,
          values,
          deviceId: plan.deviceId,
          path: plan.path,
        });
        setFill(null);
        setBusy(false);
        props.onStarted();
      } catch (err) {
        const reason = startProblem(err) ?? errorText(err);
        setBusy(false);
        if (form) setError(reason);
        else say(reason, true);
      }
    },
    [props.startTemplate, props.onStarted, say],
  );

  const choose = useCallback(
    (template: Template) => {
      const plan = planStart(template, targets);
      if (!plan || plan.issues.length > 0) {
        props.openDraft(template);
        return;
      }
      setError(null);
      if (template.placeholders.length > 0) setFill({ template, plan });
      else void start(template, plan, {}, false);
    },
    [targets, props.openDraft, start],
  );

  const saveAs = useCallback(
    (session: LiveSessionMeta, capabilities: RuntimeCapabilities | undefined) => {
      const options = capabilities?.config ?? [];
      void firstPrompt(session.sessionId)
        .catch((): string => {
          say(t('Could not read the first prompt'), true);
          return '';
        })
        .then((text) => setSave({ session, text, options }));
    },
    [say],
  );

  const dialogs = (
    <>
      {fill ? (
        <FillDialog
          template={fill.template}
          issues={[]}
          context={startContext(fill.plan.machine, fill.plan.path, fill.plan.options)}
          busy={busy}
          error={error}
          initial={fixture?.fill?.values ?? {}}
          onStart={(values) => void start(fill.template, fill.plan, values, true)}
          onEdit={(values) => {
            const { template } = fill;
            setFill(null);
            props.openDraft(template, values);
          }}
          onClose={() => {
            setFill(null);
            setError(null);
          }}
        />
      ) : null}
      {save ? (
        <SaveDialog
          text={save.text}
          initialName={fixture?.save?.name ?? ''}
          summary={{
            where: `${save.session.deviceName} / ${baseName(save.session.projectPath)}`,
            options: optionsText(save.options),
            mode: modeName(save.options),
          }}
          saving={templates.saving}
          problem={templates.problem}
          onEdited={templates.clearProblem}
          onClose={() => {
            templates.clearProblem();
            setSave(null);
          }}
          onSave={(input) => {
            const { session, options } = save;
            const mode = modeOption(options);
            void templates
              .save(
                null,
                draftTemplateBody({
                  name: input.name,
                  text: input.text,
                  deviceId: session.ownerDeviceId ?? null,
                  path: session.projectPath,
                  provider: session.provider,
                  configuration: session.configuration?.id ?? session.provider,
                  config: sessionConfig(options),
                  ...(mode ? { modeId: mode.id } : {}),
                  keep: input.keep,
                }),
              )
              .then((ok) => {
                if (!ok) return;
                setSave(null);
                say(t('Template saved'));
              });
          }}
        />
      ) : null}
    </>
  );

  return { choices, choose, saveAs, dialogs };
}

function modeName(options: readonly ConfigOption[]): string | null {
  const mode = modeOption(options);
  return mode ? (currentValueName(mode) ?? null) : null;
}
