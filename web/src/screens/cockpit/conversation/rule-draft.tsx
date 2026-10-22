import { type ReactNode, useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { Dialog } from '../../../components/Dialog.tsx';
import { fill } from '../../../components/fill.tsx';
import { Icon } from '../../../components/icons.tsx';
import { kindIcon } from '../../../components/rule-parts.tsx';
import { Segmented } from '../../../components/Segmented.tsx';
import { TextField } from '../../../components/TextField.tsx';
import { t } from '../../../lib/i18n.ts';
import {
  checkForm,
  type Draft,
  type DraftOption,
  emptyForm,
} from '../../../lib/permission-rules.ts';
import { fill as fillText } from '../../../lib/templates.ts';
import '../../../components/rules.css';

export type DraftScope = 'session' | 'project';

type OkDraft = Extract<Draft, { ok: true }>;

const mono = (s: string) => <span className="mono">{s}</span>;

function optionText(draft: OkDraft, option: DraftOption): string {
  switch (option.id) {
    case 'two':
      return t('The first two words. Anything after them also matches.');
    case 'one':
      return fillText(t('The first word. Wider: any {word} command.'), { word: option.value });
    case 'file':
      return t('This file.');
    case 'dir':
      return t('Every file in this folder and below. Wider.');
    default:
      return draft.kind === 'mcp' ? t('This tool, whatever its arguments.') : '';
  }
}

function matchLabel(kind: OkDraft['kind']): string {
  switch (kind) {
    case 'run':
      return t('Command');
    case 'read':
    case 'write':
      return t('Path');
    case 'fetch':
      return t('Domain');
    case 'mcp':
      return t('Tool');
  }
}

/**
 * The rule drafted from a permission request (ADR 0029): what it matches, the scope, a note. Nothing is saved until
 * the button; saving also answers the request that opened it.
 */
export function RuleDraftDialog(props: {
  draft: OkDraft;
  action: 'allow' | 'deny';
  /** The name of the session's project and machine, for the project scope. */
  projectName: string;
  machineName: string;
  /** A rule of the session's project needs the project and the machine to be known. */
  canProject: boolean;
  /** Saves; resolves to null on success, else the reason. */
  onSave: (option: DraftOption, scope: DraftScope, note: string) => Promise<string | null>;
  onClose: () => void;
  initialScope?: DraftScope;
  initialNote?: string;
  initialOption?: string;
}) {
  const { draft, action } = props;
  const [optionId, setOptionId] = useState(props.initialOption ?? draft.options[0]?.id ?? '');
  const [host, setHost] = useState(draft.editable ? (draft.options[0]?.value ?? '') : '');
  const [scope, setScope] = useState<DraftScope>(props.initialScope ?? 'session');
  const [note, setNote] = useState(props.initialNote ?? '');
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const chosen = draft.options.find((o) => o.id === optionId) ?? draft.options[0];
  const hostBad =
    draft.editable && checkForm({ ...emptyForm(), kind: 'fetch', domains: [host.trim()] });
  const hostOk =
    !draft.editable || (host.trim() !== '' && hostBad !== false && hostBad.badDomain === null);
  const option: DraftOption | undefined = draft.editable
    ? chosen && { ...chosen, value: host.trim(), match: { domains: [host.trim()] } }
    : chosen;
  const title = action === 'allow' ? t('Always allow') : t('Always deny');
  const save = async () => {
    if (!option || !hostOk || saving) return;
    setSaving(true);
    setProblem(null);
    const failed = await props.onSave(option, scope, note);
    if (failed) {
      setProblem(failed);
      setSaving(false);
    }
  };
  const effect: ReactNode =
    action === 'allow' ? (
      draft.kind === 'run' ? (
        <div className="qxnote att">
          {Icon.alert()}
          <span>
            {fill(
              t(
                'A command with a pipe, {and}, a redirection or {sub} is never allowed by this rule. It asks each time.',
              ),
              { and: mono('&&'), sub: mono('$( )') },
            )}
          </span>
        </div>
      ) : (
        <div className="qxnote">
          {Icon.info()}
          <span>
            {t('A deny at any scope still wins, and an Ask rule in this project beats this Allow.')}
          </span>
        </div>
      )
    ) : (
      <div className="qxnote">
        {Icon.info()}
        <span>
          {t(
            'A deny wins over every allow, at any scope. Turn it off in Settings › Permission rules.',
          )}
        </span>
      </div>
    );
  return (
    <Dialog
      label={title}
      onClose={props.onClose}
      className="dlg qd"
      data-testid="cockpit.rule-draft"
    >
      <div className="dlg-h">{title}</div>
      <div className="dlg-b">
        <div className="qreq">
          {kindIcon(draft.kind)}
          <span className="mono">{draft.request}</span>
          <span className="qw">{t('this request')}</span>
        </div>
        {draft.editable ? (
          <div className="qsec">
            <div className="fld">
              <span className="l">{matchLabel(draft.kind)}</span>
              <TextField
                label={matchLabel(draft.kind)}
                mono
                value={host}
                spellCheck={false}
                autoCapitalize="off"
                aria-invalid={hostOk ? undefined : true}
                onChange={(e) => setHost(e.target.value)}
                data-testid="cockpit.rule-draft.host"
              />
              <span className="h meta">
                {fill(t('The host of the URL. Use {wild} for its subdomains.'), {
                  wild: mono(`*.${draft.options[0]?.value ?? 'example.com'}`),
                })}
              </span>
            </div>
          </div>
        ) : (
          <div className="qsec">
            <span className="l">{matchLabel(draft.kind)}</span>
            <div className="opts" role="radiogroup" aria-label={matchLabel(draft.kind)}>
              {draft.options.map((o) => {
                const on = o.id === chosen?.id;
                return (
                  // biome-ignore lint/a11y/useSemanticElements: a radio drawn as the design's round mark
                  <button
                    type="button"
                    key={o.id}
                    role="radio"
                    aria-checked={on}
                    className={`opt${on ? ' on' : ''}`}
                    onClick={() => setOptionId(o.id)}
                    data-testid={`cockpit.rule-draft.option.${o.id}`}
                  >
                    <span className={`radio${on ? ' on' : ''}`} />
                    <span>
                      <span className="ol">{mono(o.value)}</span>
                      <span className="od">{optionText(draft, o)}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        )}
        <div className="qsec">
          <span className="l">{t('Scope')}</span>
          <Segmented
            label={t('Scope')}
            value={scope}
            options={[
              { value: 'session', label: t('This session'), icon: Icon.clock() },
              {
                value: 'project',
                label: t('This project'),
                icon: Icon.folder(),
                ...(props.canProject ? {} : { disabled: true }),
              },
            ]}
            onChange={setScope}
            data-testid="cockpit.rule-draft.scope"
          />
          <span className="meta">
            {scope === 'session'
              ? t('Ends with the session and goes with it in a handoff.')
              : fill(
                  t(
                    '{project} on {machine}. It stays on this machine in a handoff. A global rule is set in Settings.',
                  ),
                  { project: props.projectName, machine: props.machineName },
                )}
          </span>
        </div>
        <div className="qsec">
          <div className="fld">
            <span className="l">
              {t('Note')} <span className="meta">{t('optional')}</span>
            </span>
            <TextField
              label={t('Note')}
              value={note}
              placeholder={action === 'allow' ? t('Run the unit tests') : t('No paste sites')}
              onChange={(e) => setNote(e.target.value)}
              data-testid="cockpit.rule-draft.note"
            />
          </div>
        </div>
        {effect}
        {problem ? (
          <div className="qdesc bad" role="alert" data-testid="cockpit.rule-draft.problem">
            {Icon.alert()}
            <span>{problem}</span>
          </div>
        ) : null}
      </div>
      <div className="dlg-f">
        <span className="l">
          {Icon.info()}
          {t('Nothing is saved until you press the button.')}
        </span>
        <Button variant="quiet" onClick={props.onClose}>
          {t('Cancel')}
        </Button>
        <Button
          variant={action === 'allow' ? 'primary' : 'danger'}
          disabled={!option || !hostOk || saving}
          onClick={() => void save()}
          data-testid="cockpit.rule-draft.save"
        >
          {action === 'allow' ? t('Save and allow') : t('Save and deny')}
        </Button>
      </div>
    </Dialog>
  );
}
