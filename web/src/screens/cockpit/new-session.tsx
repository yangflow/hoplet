import { useMemo, useState } from 'react';
import { Dialog } from '../../components/Dialog.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Panel } from '../../components/Panel.tsx';
import { ProviderMark, providerName } from '../../components/provider-marks.tsx';
import { Select } from '../../components/Select.tsx';
import { Switch } from '../../components/Switch.tsx';
import { TextField } from '../../components/TextField.tsx';
import { errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import type { NewSessionRequest, SessionTarget } from './model.ts';
import { useDeviceDirs } from './use-device-dirs.ts';

/**
 * New session as a centred dialog: where, with what, and the first prompt. The providers and options a
 * machine offers come from what its agent declares; the directories are its watched projects.
 */
export function NewSession(props: {
  targets: SessionTarget[];
  preset?: { deviceName: string; path: string } | undefined;
  initialPrompt?: string;
  /** Read each machine's directories from its catalog; fixtures turn this off. */
  loadDirectories?: boolean;
  /** Starts the session; resolves when it exists. */
  onSubmit: (request: NewSessionRequest) => Promise<void>;
  onClose: () => void;
}) {
  const { targets, preset } = props;
  const firstOnline = targets.find((x) => x.online) ?? targets[0];
  const [machine, setMachine] = useState(preset?.deviceName ?? firstOnline?.deviceName ?? '');
  const target = targets.find((x) => x.deviceName === machine);
  const { dirs } = useDeviceDirs(
    target?.deviceId ?? null,
    target?.dirs ?? [],
    props.loadDirectories ?? true,
  );
  const [path, setPath] = useState(preset?.path ?? '');
  const providers = target?.providers ?? [];
  const [chosen, setChosen] = useState('');
  const provider = providers.find((p) => p.provider === chosen) ?? providers[0];
  const caps = provider?.capabilities;
  const [model, setModel] = useState('');
  const [thinking, setThinking] = useState(true);
  const [fast, setFast] = useState(false);
  const [prompt, setPrompt] = useState(props.initialPrompt ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const dirValue = dirs.some((d) => d.path === path) ? path : (dirs[0]?.path ?? '');
  const modelValue = caps?.models.includes(model) ? model : (caps?.models[0] ?? '');

  const ready =
    prompt.trim() !== '' &&
    !!target?.online &&
    !!target.deviceId &&
    !!provider &&
    dirValue !== '' &&
    !busy;

  async function submit() {
    if (!ready || !target?.deviceId || !provider || !caps) return;
    setBusy(true);
    setError(null);
    try {
      await props.onSubmit({
        deviceId: target.deviceId,
        deviceName: target.deviceName,
        path: dirValue,
        provider: provider.provider,
        options: {
          ...(caps.models.length > 0 ? { model: modelValue } : {}),
          ...(caps.thinking ? { thinking } : {}),
          ...(caps.fast ? { fast } : {}),
        },
        prompt: prompt.trim(),
      });
    } catch (err) {
      setError(errorText(err));
      setBusy(false);
    }
  }

  const machineOptions = useMemo(
    () =>
      targets.map((x) => ({
        value: x.deviceName,
        label: x.deviceName,
        disabled: !x.online,
        leading: <span className={`dot${x.online ? '' : ' off'}`} />,
      })),
    [targets],
  );

  return (
    <Dialog label={t('New session')} onClose={props.onClose} data-testid="cockpit.new-dialog">
      <div className="ns">
        <div className="ns-head">
          <b>{t('New session')}</b>
          <IconButton
            label={t('Close')}
            hint="Esc"
            icon={Icon.close()}
            onClick={props.onClose}
            data-testid="cockpit.new-close"
          />
        </div>
        <div className="ns-grid">
          <span className="ns-label">{t('Machine')}</span>
          <Select
            label={t('Machine')}
            detail={target?.online ? t('Machine · online') : t('Machine · offline')}
            value={machine}
            options={machineOptions}
            disabled={busy}
            onChange={(v) => {
              setMachine(v);
              setPath('');
              setChosen('');
            }}
            data-testid="cockpit.new-machine"
          />
          <span className="ns-label">{t('Directory')}</span>
          <Select
            label={t('Directory')}
            detail={dirValue || t('No directories yet')}
            value={dirValue}
            options={dirs.map((d) => ({
              value: d.path,
              label: d.name,
              leading: Icon.folder(),
            }))}
            disabled={busy}
            onChange={setPath}
            data-testid="cockpit.new-directory"
          />
          <span className="ns-label">{t('Provider')}</span>
          <Select
            label={t('Provider')}
            value={provider?.provider ?? ''}
            options={providers.map((p) => ({
              value: p.provider,
              label: providerName(p.provider),
              leading: <ProviderMark provider={p.provider} />,
            }))}
            disabled={busy || providers.length === 0}
            onChange={(v) => {
              setChosen(v);
              setModel('');
            }}
            data-testid="cockpit.new-provider"
          />
          {caps && caps.models.length > 0 ? (
            <>
              <span className="ns-label">{t('Model')}</span>
              <Select
                label={t('Model')}
                value={modelValue}
                options={caps.models.map((m) => ({ value: m, label: m }))}
                disabled={busy}
                onChange={setModel}
                data-testid="cockpit.new-model"
              />
            </>
          ) : null}
          {caps?.thinking || caps?.fast ? (
            <>
              <span />
              <div className="ns-toggles">
                {caps.thinking ? (
                  <Switch
                    label={t('Thinking')}
                    checked={thinking}
                    disabled={busy}
                    onChange={setThinking}
                    data-testid="cockpit.new-thinking"
                  />
                ) : null}
                {caps.fast ? (
                  <Switch
                    label={t('Fast')}
                    checked={fast}
                    disabled={busy}
                    onChange={setFast}
                    data-testid="cockpit.new-fast"
                  />
                ) : null}
              </div>
            </>
          ) : null}
        </div>
        <Panel tone="field" focused className="ns-prompt">
          <TextField
            multiline
            bare
            label={t('First prompt')}
            placeholder={t('First prompt')}
            value={prompt}
            disabled={busy}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void submit();
              }
            }}
            data-autofocus
            data-testid="cockpit.new-prompt"
          />
          <IconButton
            label={busy ? t('Starting…') : t('Start')}
            hint="↩"
            icon={busy ? <Spinner /> : Icon.send()}
            primary
            disabled={!ready}
            onClick={() => void submit()}
            data-testid="cockpit.new-start"
          />
        </Panel>
        {error ? (
          <p className="ns-error" role="alert" data-testid="cockpit.new-error">
            {error}
          </p>
        ) : null}
      </div>
    </Dialog>
  );
}
