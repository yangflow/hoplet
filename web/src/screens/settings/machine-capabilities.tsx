import type { DeviceInfo } from '@hoplet/shared';
import { type ReactNode, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Icon } from '../../components/icons.tsx';
import {
  checkLabel,
  gpuMemoryText,
  LABEL_RULE,
  type LabelProblem,
  projectName,
  toolchainsOf,
} from '../../lib/capabilities.ts';
import { t } from '../../lib/i18n.ts';
import { useDeviceLabels } from '../../lib/use-device-labels.ts';
import { agoText } from './last-seen.ts';

function Row(props: { label: string; testid: string; children: ReactNode; labels?: boolean }) {
  return (
    <div className={`cp-row${props.labels ? ' lbrow' : ''}`} data-testid={props.testid}>
      <span className="cp-k">{props.label}</span>
      <div className="cp-v">{props.children}</div>
    </div>
  );
}

/** What the agent reported: tools with versions (and the missing ones named), the GPUs, and the origin of each project. */
function Reported(props: { device: DeviceInfo }) {
  const { device } = props;
  const inventory = device.inventory;
  if (!inventory) {
    return (
      <p className="cp-none" data-testid="settings.capabilities.none">
        {t('{name}’s agent does not report capabilities. Labels still work.').replace(
          '{name}',
          device.name,
        )}
      </p>
    );
  }
  const { found, missing } = toolchainsOf(inventory);
  const unified = device.system?.os === 'darwin';
  return (
    <div className="cp-rows">
      <Row label={t('Toolchains')} testid="settings.capabilities.toolchains">
        {found.length > 0 ? (
          <div className="cp-tools">
            {found.map((x) => (
              <span key={x.name} className="cp-t">
                {x.name}
                <span>{x.version}</span>
              </span>
            ))}
          </div>
        ) : null}
        {missing.length > 0 ? (
          <span className="set-meta">
            {t('Not found: {tools}').replace('{tools}', missing.join(', '))}
          </span>
        ) : null}
      </Row>
      {inventory.gpus.length > 0 ? (
        <Row label={t('GPU')} testid="settings.capabilities.gpu">
          {inventory.gpus.map((g) => (
            <span key={g.name} className="cp-line">
              {g.name}
              {g.memory !== undefined ? (
                <span className="cp-dim">
                  {' · '}
                  {gpuMemoryText(g.memory)}
                  {unified ? ` ${t('unified')}` : ''}
                </span>
              ) : null}
            </span>
          ))}
        </Row>
      ) : null}
      {inventory.repositories.length > 0 ? (
        <Row label={t('Repositories')} testid="settings.capabilities.repositories">
          {inventory.repositories.map((r) => (
            <span key={r.path} className="cp-line" title={r.path}>
              <span className="nm">{projectName(r.path)}</span>
              {r.origin ? (
                <span className="mono">{r.origin}</span>
              ) : (
                <span className="cp-dim">{t('no origin')}</span>
              )}
            </span>
          ))}
        </Row>
      ) : null}
    </div>
  );
}

function problemText(problem: LabelProblem, text: string, name: string): string {
  return problem === 'full'
    ? t('{name} already has {count} labels. Remove one to add another.')
        .replace('{name}', name)
        .replace('{count}', `${LABEL_RULE.count}`)
    : t(
        '“{label}” is not a label. Use lowercase letters, digits and -, up to {max} characters, and at most {count} labels per machine.',
      )
        .replace('{label}', text.trim())
        .replace('{max}', `${LABEL_RULE.max}`)
        .replace('{count}', `${LABEL_RULE.count}`);
}

/** The labels as removable chips, then Add label or, while typing, an inline field that validates as you go. */
function Labels(props: {
  device: DeviceInfo;
  local: boolean;
  adding?: string | undefined;
  removed?: string | undefined;
}) {
  const { device } = props;
  const state = useDeviceLabels(device, props.local, props.removed);
  const [adding, setAdding] = useState(props.adding !== undefined);
  const [text, setText] = useState(props.adding ?? '');
  const [problem, setProblem] = useState<LabelProblem | null>(
    props.adding !== undefined && props.adding !== ''
      ? (() => {
          const c = checkLabel(props.adding, state.labels);
          return c.ok ? null : c.problem;
        })()
      : null,
  );
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (adding) input.current?.focus();
  }, [adding]);

  const close = () => {
    setAdding(false);
    setText('');
    setProblem(null);
  };
  const submit = () => {
    if (text.trim() === '') return close();
    const c = checkLabel(text, state.labels);
    if (!c.ok) return setProblem(c.problem);
    state.add(c.label);
    // Stay open for the next one: a machine usually gets several.
    setText('');
    setProblem(null);
  };
  return (
    <>
      <div className="cp-labels" data-testid="settings.capabilities.labels">
        {state.labels.map((l) => (
          <span key={l} className="cp-lb">
            {l}
            <button
              type="button"
              className="cp-x"
              aria-label={t('Remove label {label}').replace('{label}', l)}
              onClick={() => state.remove(l)}
              data-testid={`settings.capabilities.remove.${l}`}
            >
              {Icon.close()}
            </button>
          </span>
        ))}
        {adding ? (
          <span className={`cp-in${problem ? ' bad' : ''}`}>
            <input
              ref={input}
              className="field bare mono"
              aria-label={t('New label')}
              placeholder="ci-runner"
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              value={text}
              aria-invalid={problem !== null || undefined}
              onChange={(e) => {
                setText(e.target.value);
                setProblem(null);
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  submit();
                } else if (e.key === 'Escape') {
                  e.preventDefault();
                  e.stopPropagation();
                  close();
                }
              }}
              onBlur={() => {
                if (text.trim() === '') close();
              }}
              data-testid="settings.capabilities.input"
            />
          </span>
        ) : (
          <Button
            variant="quiet"
            className="cp-add"
            icon={Icon.plus()}
            onClick={() => setAdding(true)}
            data-testid="settings.capabilities.add"
          >
            {t('Add label')}
          </Button>
        )}
      </div>
      {problem ? (
        <p className="cp-msg set-meta bad" role="alert" data-testid="settings.capabilities.invalid">
          {Icon.alert()}
          <span>{problemText(problem, text, device.name)}</span>
        </p>
      ) : state.error ? (
        <p className="cp-msg set-meta bad" role="alert" data-testid="settings.capabilities.error">
          {Icon.alert()}
          <span>{state.error}</span>
        </p>
      ) : adding ? (
        <p className="cp-msg set-meta">
          <span>{t('Enter adds the label, Esc cancels. Saved as you add it.')}</span>
        </p>
      ) : state.removed ? (
        <p className="cp-msg set-meta" data-testid="settings.capabilities.removed">
          <span>{t('Removed {label}.').replace('{label}', state.removed.label)}</span>
          <Button variant="quiet" onClick={state.undo} data-testid="settings.capabilities.undo">
            {t('Undo')}
          </Button>
        </p>
      ) : (
        <p className="cp-msg set-meta">
          <span>
            {t(
              'Labels are kept by Hoplet, not on {name}, and can be edited while it is offline.',
            ).replace('{name}', device.name)}
          </span>
        </p>
      )}
    </>
  );
}

/**
 * The Capabilities group of a machine's tab, between the resources and Warnings: what its agent last reported (greyed
 * while the machine is offline) and the host's labels, which stay editable offline.
 */
export function CapabilitiesGroup(props: {
  device: DeviceInfo;
  now: number;
  /** Labels are kept locally and never saved: the design's states. */
  local?: boolean;
  /** The label field open with this text, for the design's states. */
  adding?: string | undefined;
  /** A label shown as just removed, with Undo, for the design's states. */
  removed?: string | undefined;
}) {
  const { device } = props;
  const at = device.inventory?.at;
  return (
    <section
      className={`rth rcap${device.connected ? '' : ' stale'}`}
      aria-label={t('Capabilities')}
      data-testid="settings.capabilities"
    >
      <div className="cp-h">
        <b>{t('Capabilities')}</b>
        {at !== undefined ? (
          <span className="set-meta" data-testid="settings.capabilities.at">
            {device.connected ? null : Icon.clock()}
            {(device.connected ? t('Detected {when}') : t('Last reported {when}')).replace(
              '{when}',
              agoText(at, props.now),
            )}
          </span>
        ) : null}
      </div>
      <Reported device={device} />
      <div className="cp-rows">
        <Row label={t('Labels')} testid="settings.capabilities.labelrow" labels>
          <Labels
            device={device}
            local={props.local ?? false}
            adding={props.adding}
            removed={props.removed}
          />
        </Row>
      </div>
    </section>
  );
}
