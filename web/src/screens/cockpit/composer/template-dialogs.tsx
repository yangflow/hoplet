import type { Template } from '@hoplet/shared';
import { type FormEvent, useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { Dialog } from '../../../components/Dialog.tsx';
import { Icon } from '../../../components/icons.tsx';
import { Switch } from '../../../components/Switch.tsx';
import { TextField } from '../../../components/TextField.tsx';
import { t } from '../../../lib/i18n.ts';
import {
  isComplete,
  type KeepOptions,
  previewParts,
  type StartIssue,
} from '../../../lib/template-start.ts';
import {
  fill,
  formatCount,
  MAX_TEMPLATE_TEXT_CHARS,
  newDraft,
  placeholderLabel,
  placeholdersOf,
  type TemplateProblem,
  validateDraft,
} from '../../../lib/templates.ts';
import '../../../components/templates.css';
import './templates.css';

/** The prompt as it will be sent: the values in place, a field without one under its name. */
function Preview(props: { text: string; values: Record<string, string> }) {
  return (
    <div className="tp-pv" data-testid="cockpit.template-preview">
      {previewParts(props.text, props.values).map((part, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: the parts of a prompt have no identity but their place
        <span key={i} className={part.field ? 'tp-tok pv' : undefined}>
          {part.text}
        </span>
      ))}
    </div>
  );
}

/**
 * The form a template with placeholders opens: one field for each, the prompt that will be sent, and where it will
 * run. Start sends it at once; "Edit in the draft" puts the prompt in the composer instead.
 */
export function FillDialog(props: {
  template: Template;
  /** What the template does not get where it would start; Start waits until there are none. */
  issues: StartIssue[];
  /** The machine, the folder and the options, as words. */
  context: { machine: string; folder: string; options: string };
  busy: boolean;
  error: string | null;
  initial?: Record<string, string>;
  onStart: (values: Record<string, string>) => void;
  onEdit: (values: Record<string, string>) => void;
  onClose: () => void;
}) {
  const { template } = props;
  const [values, setValues] = useState<Record<string, string>>(props.initial ?? {});
  const ready = isComplete(template.text, values) && props.issues.length === 0 && !props.busy;

  function submit(e: FormEvent) {
    e.preventDefault();
    if (ready) props.onStart(values);
  }

  return (
    <Dialog
      label={t('Start from a template')}
      onClose={props.onClose}
      className="tp"
      data-testid="cockpit.template-fill"
    >
      <form onSubmit={submit}>
        <div className="dlg-h">{fill(t('Start “{name}”'), { name: template.name })}</div>
        <div className="dlg-b">
          {template.placeholders.map((p, i) => (
            <div className="tfl" key={p.name}>
              <span className="l">
                <span className="mono">{p.name}</span>
                <em>
                  {p.default !== undefined
                    ? fill(t('default {value}'), { value: p.default })
                    : t('required')}
                </em>
              </span>
              <TextField
                label={p.name}
                value={values[p.name] ?? ''}
                placeholder={p.default ?? ''}
                {...(i === 0 ? { 'data-autofocus': '' } : {})}
                onChange={(e) => setValues((cur) => ({ ...cur, [p.name]: e.target.value }))}
                data-testid={`cockpit.template-field.${p.name}`}
              />
            </div>
          ))}
          <div className="tfl">
            <span className="l">{t('The prompt that is sent')}</span>
            <Preview text={template.text} values={values} />
          </div>
          <div className="tp-line">
            <span className="tp-chip">{props.context.machine}</span>
            <span className="tp-chip">
              {Icon.folder()}
              {props.context.folder}
            </span>
            {props.context.options ? (
              <span className="tp-chip">{props.context.options}</span>
            ) : null}
          </div>
          {props.issues.length > 0 ? (
            <div className="tp-issues" role="status" data-testid="cockpit.template-issues">
              {props.issues.map((issue) => (
                <p className="tp-note bad" key={issue.key}>
                  {issue.message}
                </p>
              ))}
            </div>
          ) : null}
          {props.error ? (
            <p className="tp-note bad" role="alert" data-testid="cockpit.template-error">
              {props.error}
            </p>
          ) : null}
        </div>
        <div className="dlg-f">
          <Button variant="quiet" onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          {props.issues.length > 0 ? (
            <Button
              variant="quiet"
              onClick={() => props.onEdit(values)}
              data-testid="cockpit.template-edit"
            >
              {t('Edit in the draft')}
            </Button>
          ) : null}
          <Button
            variant="primary"
            type="submit"
            disabled={!ready}
            data-testid="cockpit.template-start"
          >
            {props.busy ? t('Starting…') : t('Start')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** What the draft shows in the save dialog's summary, and which of it can be left out. */
export type SaveSummary = {
  where: string;
  options: string | null;
  mode: string | null;
};

/**
 * Saves the draft as a template: a name, the prompt (placeholders written in it), and three switches for what of
 * the draft's place and options goes along.
 */
export function SaveDialog(props: {
  /** The draft's text, which the dialog starts with. */
  text: string;
  summary: SaveSummary;
  saving: boolean;
  /** The core's refusal of the last save. */
  problem: TemplateProblem | null;
  onSave: (input: { name: string; text: string; keep: KeepOptions }) => void;
  onClose: () => void;
  onEdited: () => void;
  initialName?: string;
}) {
  const [name, setName] = useState(props.initialName ?? '');
  const [text, setText] = useState(props.text);
  const [keep, setKeep] = useState<KeepOptions>({ where: true, options: true, mode: true });
  const [touched, setTouched] = useState(false);
  const found = validateDraft({ ...newDraft(), name, text });
  const nameError = found?.name ?? (props.problem?.field === 'name' ? props.problem.message : null);
  const textError = found?.text ?? (props.problem?.field === 'text' ? props.problem.message : null);
  const formError =
    props.problem && (props.problem.field === 'form' || props.problem.field === 'machines')
      ? props.problem.message
      : null;
  const placeholders = placeholdersOf(text);
  const left = MAX_TEMPLATE_TEXT_CHARS - text.length;

  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (found || props.saving) return;
    props.onSave({ name: name.trim(), text: text.trim(), keep });
  }

  const rows: { key: keyof KeepOptions; label: string; value: string | null }[] = [
    { key: 'where', label: t('Machine and directory'), value: props.summary.where },
    { key: 'options', label: t('Model and options'), value: props.summary.options },
    { key: 'mode', label: t('Permission mode'), value: props.summary.mode },
  ];

  return (
    <Dialog
      label={t('Save as template')}
      onClose={props.onClose}
      className="tp"
      data-testid="cockpit.template-save-dialog"
    >
      <form onSubmit={submit}>
        <div className="dlg-h">{t('Save as template')}</div>
        <div className="dlg-b">
          <div className="tfl">
            <span className="l">{t('Name')}</span>
            <TextField
              label={t('Name')}
              value={name}
              maxLength={200}
              aria-invalid={touched && nameError ? true : undefined}
              data-autofocus=""
              onChange={(e) => {
                setName(e.target.value);
                props.onEdited();
              }}
              data-testid="cockpit.template-name"
            />
            {touched && nameError ? (
              <p className="tp-note bad" role="alert" data-testid="cockpit.template-name-error">
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
              value={text}
              aria-invalid={textError ? true : undefined}
              onChange={(e) => {
                setText(e.target.value);
                props.onEdited();
              }}
              data-testid="cockpit.template-text"
            />
            <span className="tp-note">
              {t('{{name}} or {{name:default}} is filled in at start · \\{{ is a literal {{')}
            </span>
            {textError ? (
              <p className="tp-note bad" role="alert" data-testid="cockpit.template-text-error">
                {textError}
              </p>
            ) : placeholders.length > 0 ? (
              <div className="tp-toks">
                {placeholders.map((p) => (
                  <span key={p.name} className="tp-tok">
                    {placeholderLabel(p)}
                  </span>
                ))}
              </div>
            ) : null}
          </div>
          <div className="tp-keep" data-testid="cockpit.template-keep">
            {rows.map((r) =>
              r.value === null ? null : (
                <div key={r.key}>
                  <span className="k">{r.label}</span>
                  <span className="v">{r.value}</span>
                  <Switch
                    label={r.label}
                    hideLabel
                    checked={keep[r.key]}
                    onChange={(on) => setKeep((cur) => ({ ...cur, [r.key]: on }))}
                    data-testid={`cockpit.template-keep.${r.key}`}
                  />
                </div>
              ),
            )}
          </div>
          {formError ? (
            <p className="tp-note bad" role="alert" data-testid="cockpit.template-error">
              {formError}
            </p>
          ) : null}
        </div>
        <div className="dlg-f">
          <Button variant="quiet" onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button
            variant="primary"
            type="submit"
            disabled={props.saving || (touched && found !== null)}
            data-testid="cockpit.template-save-submit"
          >
            {t('Save')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
