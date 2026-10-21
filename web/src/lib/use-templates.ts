import {
  type DeviceInfo,
  parseTemplate,
  type Template,
  type TemplateBody,
  type TemplateFile,
  type TemplateImportResult,
  type TemplateListResponse,
} from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  deleteTemplate,
  fetchTemplateExport,
  fetchTemplates,
  postTemplate,
  postTemplateImport,
  putTemplate,
} from './api.ts';
import { errorText } from './errors.ts';
import { planImport, type TemplateProblem, templateProblem, templateReason } from './templates.ts';
import type { Loaded } from './use-usage.ts';

export type TemplatesState = Loaded<TemplateListResponse> & {
  /** Saves a new template (`id` null) or changes one; resolves false and sets `problem` when the core refused. */
  save: (id: string | null, body: TemplateBody) => Promise<boolean>;
  remove: (id: string) => Promise<boolean>;
  /** Asks what importing `file` would do, or null with `problem` set. Nothing is stored. */
  preview: (
    file: TemplateFile,
    machines: Record<string, string>,
  ) => Promise<TemplateImportResult | null>;
  /** Imports `file`: all entries or none. */
  importFile: (file: TemplateFile, machines: Record<string, string>) => Promise<boolean>;
  /** Every template as an export file, or null with `problem` set. */
  exportFile: () => Promise<TemplateFile | null>;
  saving: boolean;
  /** The core's reason for the last refused request, at the field it belongs to, or null. */
  problem: TemplateProblem | null;
  clearProblem: () => void;
};

/** What a request does in a fixture: its value, or the refusal the core would give. */
type Local<T> = { value: T } | { problem: TemplateProblem };

/** The template a fixture stores for a body, the way the core would. */
function stored(id: string, body: TemplateBody, at: number): Template {
  const parsed = parseTemplate(body.text);
  return {
    id,
    name: body.name,
    text: body.text,
    ...(body.deviceId ? { deviceId: body.deviceId } : {}),
    ...(body.projectPath ? { projectPath: body.projectPath } : {}),
    provider: body.provider,
    ...(body.configuration ? { configuration: body.configuration } : {}),
    ...(body.config ? { config: body.config } : {}),
    placeholders: parsed.ok ? parsed.placeholders : [],
    createdAt: at,
    updatedAt: at,
  };
}

/**
 * The session templates of the host, and their changes. A `fixture` is the answer itself and the changes stay
 * local; `devices` are the machines it names.
 */
export function useTemplates(
  fixture?: TemplateListResponse,
  devices: readonly DeviceInfo[] = [],
): TemplatesState {
  const [data, setData] = useState<TemplateListResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [problem, setProblem] = useState<TemplateProblem | null>(null);
  const [saving, setSaving] = useState(false);
  const [again, setAgain] = useState(0);
  const locked = fixture !== undefined;
  // The callbacks below keep their identity while the list and the machines change.
  const latest = useRef({ data, devices });
  latest.current = { data, devices };

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the reload after a change
  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    fetchTemplates(abort.signal)
      .then((r) => {
        setData(r);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => abort.abort();
  }, [locked, again]);

  /**
   * Runs a request and turns its refusal into `problem`. `change` is a request that changes the list: it shows
   * as saving and reloads the list. `source` is what the offsets of a refusal point into.
   */
  const run = useCallback(
    async <T>(
      local: () => Local<T>,
      remote: () => Promise<T>,
      change: boolean,
      source: { text?: string; file?: TemplateFile } = {},
    ): Promise<T | null> => {
      setProblem(null);
      if (locked) {
        const out = local();
        if ('problem' in out) {
          setProblem(out.problem);
          return null;
        }
        return out.value;
      }
      if (change) setSaving(true);
      try {
        const out = await remote();
        if (change) setAgain((n) => n + 1);
        return out;
      } catch (e) {
        setProblem(templateProblem(e, errorText, source));
        return null;
      } finally {
        if (change) setSaving(false);
      }
    },
    [locked],
  );

  const save = useCallback(
    async (id: string | null, body: TemplateBody) => {
      const out = await run<true>(
        () => {
          const list = latest.current.data?.templates ?? [];
          if (list.some((x) => x.id !== id && x.name === body.name)) {
            return { problem: templateReason('name', {}) as TemplateProblem };
          }
          const at = Date.now();
          const next = id
            ? list.map((x) =>
                x.id === id ? { ...stored(id, body, at), createdAt: x.createdAt } : x,
              )
            : [...list, stored(`tpl-${at.toString(36)}-${list.length}`, body, at)];
          setData({ templates: next });
          return { value: true };
        },
        async () => {
          await (id === null ? postTemplate(body) : putTemplate(id, body));
          return true;
        },
        true,
        { text: body.text },
      );
      return out === true;
    },
    [run],
  );

  const remove = useCallback(
    async (id: string) => {
      const out = await run<true>(
        () => {
          setData((d) => (d ? { templates: d.templates.filter((x) => x.id !== id) } : d));
          return { value: true };
        },
        async () => {
          await deleteTemplate(id);
          return true;
        },
        true,
      );
      return out === true;
    },
    [run],
  );

  const preview = useCallback(
    (file: TemplateFile, machines: Record<string, string>) =>
      run<TemplateImportResult>(
        () => ({
          value: planImport(
            latest.current.data?.templates ?? [],
            latest.current.devices,
            file,
            machines,
          ),
        }),
        () => postTemplateImport({ file, machines, dryRun: true }),
        false,
        { file },
      ),
    [run],
  );

  const importFile = useCallback(
    async (file: TemplateFile, machines: Record<string, string>) => {
      const out = await run<true>(
        () => {
          const plan = planImport(
            latest.current.data?.templates ?? [],
            latest.current.devices,
            file,
            machines,
          );
          const at = Date.now();
          const added = file.templates.map((entry, i) => {
            const item = plan.items[i];
            return stored(
              `tpl-${at.toString(36)}-i${i}`,
              {
                name: item?.name ?? entry.name,
                text: entry.text,
                ...(item?.deviceId ? { deviceId: item.deviceId } : {}),
                ...(entry.project ? { projectPath: entry.project } : {}),
                provider: entry.provider,
                ...(entry.configuration ? { configuration: entry.configuration } : {}),
                ...(entry.config ? { config: entry.config } : {}),
              },
              at,
            );
          });
          setData((d) => ({ templates: [...(d?.templates ?? []), ...added] }));
          return { value: true };
        },
        async () => {
          await postTemplateImport({ file, machines, dryRun: false });
          return true;
        },
        true,
        { file },
      );
      return out === true;
    },
    [run],
  );

  const exportFile = useCallback(
    () =>
      run<TemplateFile>(
        () => ({
          value: {
            v: 1,
            templates: (latest.current.data?.templates ?? []).map((x) => ({
              name: x.name,
              text: x.text,
              ...(x.deviceId
                ? {
                    device:
                      latest.current.devices.find((d) => d.deviceId === x.deviceId)?.name ??
                      x.deviceId,
                  }
                : {}),
              ...(x.projectPath ? { project: x.projectPath } : {}),
              provider: x.provider,
              ...(x.configuration ? { configuration: x.configuration } : {}),
              ...(x.config ? { config: x.config } : {}),
            })),
          },
        }),
        () => fetchTemplateExport(),
        false,
      ),
    [run],
  );

  return {
    data,
    loading,
    error,
    save,
    remove,
    preview,
    importFile,
    exportFile,
    saving,
    problem,
    clearProblem: useCallback(() => setProblem(null), []),
  };
}
