import type { DeviceInfo, ResourceThresholdPair, ResourceThresholds } from '@hoplet/shared';
import { useEffect, useId, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { fill } from '../../components/fill.tsx';
import { Segmented } from '../../components/Segmented.tsx';
import { t } from '../../lib/i18n.ts';
import type { ResourceThresholdsState } from '../../lib/use-resource-thresholds.ts';
import {
  effectiveThresholds,
  hasOwnThresholds,
  parseThreshold,
  THRESHOLD_RANGE,
  type ThresholdKind,
  withDefault,
  withMachine,
} from './thresholds-model.ts';

type Mode = 'default' | 'custom';

const LABEL: Record<ThresholdKind, string> = {
  memory: 'Memory used above',
  disk: 'Disk free under',
};

function rangeText(kind: ThresholdKind): string {
  const { min, max } = THRESHOLD_RANGE[kind];
  return t('{label}: a whole number from {min} to {max}')
    .replace('{label}', t(LABEL[kind]))
    .replace('{min}', `${min}`)
    .replace('{max}', `${max}`);
}

/** One number in percent; a text that is not a whole number in range shows invalid and is not saved. */
function PercentField(props: {
  kind: ThresholdKind;
  text: string;
  invalid: boolean;
  onText: (text: string) => void;
}) {
  const id = useId();
  const { kind, text, invalid } = props;
  return (
    <div className="field-group th-f">
      <label className="field-label" htmlFor={id}>
        {t(LABEL[kind])}
      </label>
      <span className={`th-in${invalid ? ' bad' : ''}`}>
        <input
          id={id}
          className="field bare"
          inputMode="numeric"
          autoComplete="off"
          spellCheck={false}
          value={text}
          aria-invalid={invalid || undefined}
          onChange={(e) => props.onText(e.target.value)}
          data-testid={`settings.thresholds.${kind}`}
        />
        <span className="rmeta" aria-hidden="true">
          %
        </span>
      </span>
    </div>
  );
}

/** The two fields of one pair. What was typed is kept as text; only a pair that is valid as a whole goes up. */
function ThresholdFields(props: {
  pair: ResourceThresholdPair;
  onChange: (pair: ResourceThresholdPair) => void;
}) {
  const { pair } = props;
  const [text, setText] = useState({ memory: `${pair.memory}`, disk: `${pair.disk}` });
  // The values changed from outside (a save that failed went back): show them.
  useEffect(() => {
    setText((prev) => ({
      memory:
        parseThreshold('memory', prev.memory) === pair.memory ? prev.memory : `${pair.memory}`,
      disk: parseThreshold('disk', prev.disk) === pair.disk ? prev.disk : `${pair.disk}`,
    }));
  }, [pair.memory, pair.disk]);
  const invalid = {
    memory: parseThreshold('memory', text.memory) === null,
    disk: parseThreshold('disk', text.disk) === null,
  };
  const edit = (kind: ThresholdKind, value: string) => {
    const next = { ...text, [kind]: value };
    setText(next);
    const memory = parseThreshold('memory', next.memory);
    const disk = parseThreshold('disk', next.disk);
    if (memory !== null && disk !== null) props.onChange({ memory, disk });
  };
  return (
    <>
      <div className="th-row">
        <PercentField
          kind="memory"
          text={text.memory}
          invalid={invalid.memory}
          onText={(v) => edit('memory', v)}
        />
        <PercentField
          kind="disk"
          text={text.disk}
          invalid={invalid.disk}
          onText={(v) => edit('disk', v)}
        />
      </div>
      {(['memory', 'disk'] as const).map((kind) =>
        invalid[kind] ? (
          <p key={kind} className="mrow-err" role="alert" data-testid="settings.thresholds.invalid">
            {rangeText(kind)}
          </p>
        ) : null,
      )}
    </>
  );
}

function pairText(template: string, pair: ResourceThresholdPair): string {
  return template.replace('{memory}', `${pair.memory}`).replace('{disk}', `${pair.disk}`);
}

/**
 * The Warnings group at the end of a machine's tab: the default every machine uses (changed with Change default) or
 * values of this machine's own. Every edit is applied at once and saved shortly after.
 */
export function ThresholdsGroup(props: {
  device: DeviceInfo;
  state: ResourceThresholdsState;
  /** The default's fields open, for the design's states. */
  editDefault?: boolean;
}) {
  const { device, state } = props;
  const [editing, setEditing] = useState(props.editDefault ?? false);
  const th: ResourceThresholds | null = state.thresholds;
  if (th === null) {
    return state.error ? (
      <p className="mrow-err" role="alert" data-testid="settings.thresholds.error">
        {state.error}
      </p>
    ) : null;
  }
  const id = device.deviceId;
  const mode: Mode = hasOwnThresholds(th, id) ? 'custom' : 'default';
  const pair = effectiveThresholds(th, id);
  const choose = (next: Mode) => {
    setEditing(false);
    if (next === mode) return;
    state.change(withMachine(th, id, next === 'custom' ? th.default : null));
  };
  return (
    <section className="rth" aria-label={t('Warnings')} data-testid="settings.thresholds">
      <div className="th-h">
        <b className="th-t">{t('Warnings')}</b>
        <Segmented<Mode>
          label={t('Warnings for {name}').replace('{name}', device.name)}
          value={mode}
          options={[
            { value: 'default', label: t('Default') },
            { value: 'custom', label: t('Custom for {name}').replace('{name}', device.name) },
          ]}
          onChange={choose}
          data-testid="settings.thresholds.mode"
        />
      </div>
      {mode === 'custom' ? (
        <>
          <ThresholdFields
            key={`${id}:custom`}
            pair={pair}
            onChange={(p) => state.change(withMachine(th, id, p))}
          />
          <p className="set-meta th-note">
            {pairText(
              t(
                'The default is memory above {memory}% used, disk under {disk}% free. Saved as you type.',
              ),
              th.default,
            )}
          </p>
        </>
      ) : editing ? (
        <>
          <ThresholdFields
            key={`${id}:default`}
            pair={th.default}
            onChange={(p) => state.change(withDefault(th, p))}
          />
          <p className="set-meta th-note">
            {t('Every machine without its own values uses these. Saved as you type.')}
          </p>
        </>
      ) : (
        <div className="th-sum" data-testid="settings.thresholds.summary">
          <span>
            {fill(t('Memory above {memory} used, disk under {disk} free'), {
              '{memory}': <b className="th-v">{pair.memory}%</b>,
              '{disk}': <b className="th-v">{pair.disk}%</b>,
            })}
          </span>
          <Button
            variant="quiet"
            onClick={() => setEditing(true)}
            data-testid="settings.thresholds.change-default"
          >
            {t('Change default')}
          </Button>
        </div>
      )}
      {state.error ? (
        <p className="mrow-err" role="alert" data-testid="settings.thresholds.error">
          {state.error}
        </p>
      ) : null}
    </section>
  );
}
