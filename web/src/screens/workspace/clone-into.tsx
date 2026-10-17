import type { DeviceCloneOffer, DeviceDirsResult } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useRef, useState } from 'react';
import { Button } from '../../components/Button.tsx';
import { Dialog } from '../../components/Dialog.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon, Spinner } from '../../components/icons.tsx';
import { Select } from '../../components/Select.tsx';
import { TextField } from '../../components/TextField.tsx';
import { fetchDeviceDirs } from '../../lib/api.ts';
import { ApiError, browseErrorText, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { breadcrumbs, childPath, displayPath, resolveTyped } from '../cockpit/sidebar/browse.ts';
import { ROOT_PATH, useDirBrowser } from '../cockpit/sidebar/use-dir-browser.ts';
import '../cockpit/shell.css';

/** Development fixture: the listings the machine would answer, where to start, and what the dialog already says. */
export type CloneIntoFixture = {
  listings: Record<string, DeviceDirsResult>;
  start: string;
  name: string;
  /** The destination was refused with this detail (the answer of the check). */
  taken?: 'exists' | 'outside-root' | 'symlink' | 'not-writable';
  /** A typed path the agent refused: the text in the field and the reason it gave. */
  typed?: { text: string; reason: string };
};

/** The folder part of an absolute path. */
export function dirname(path: string): string {
  const cut = path.replace(/\/+$/, '').lastIndexOf('/');
  return cut <= 0 ? '/' : path.slice(0, cut);
}

/** The last segment of a path. */
export function basename(path: string): string {
  return path.replace(/\/+$/, '').slice(path.replace(/\/+$/, '').lastIndexOf('/') + 1);
}

/** What the core's refusal of a destination says, by the detail the agent gave. */
export function destinationText(
  detail: DeviceCloneOffer['detail'] | undefined,
  where: { name: string; folder: string; device: string },
): string {
  const named = (text: string) =>
    t(text)
      .replace('{name}', where.name)
      .replace('{folder}', where.folder)
      .replace('{device}', where.device);
  switch (detail) {
    case 'exists':
      return named(
        'A folder named {name} already exists in {folder} on {device}. Pick another name or folder.',
      );
    case 'not-writable':
      return named('{device} cannot write to {folder}. Pick another folder.');
    case 'outside-root':
      return named('That folder is outside the browse root on {device}');
    case 'symlink':
      return named('{folder} is a link on {device}. Pick a folder that is not.');
    default:
      return named('{device} cannot clone into {folder}. Pick another folder.');
  }
}

/**
 * "Clone into": a folder on the target and the name of the clone's folder in it, over the same directory browser as
 * Add project. The core checks the destination before the dialog closes with it.
 */
export function CloneInto(props: {
  deviceId: string;
  deviceName: string;
  /** The destination the core suggested or the last one chosen. */
  current: string;
  /** Asks the core whether the target can clone into `path`; null when it cannot say. */
  check: (path: string) => Promise<DeviceCloneOffer | null>;
  onChosen: () => void;
  onClose: () => void;
  fixture?: CloneIntoFixture | undefined;
}) {
  const { fixture, deviceName } = props;
  const [name, setName] = useState(fixture?.name ?? basename(props.current));
  const [editing, setEditing] = useState(fixture?.typed !== undefined);
  const [text, setText] = useState(fixture?.typed?.text ?? '');
  const [typedError, setTypedError] = useState<unknown>(
    fixture?.typed ? new ApiError(400, 'invalid-request', fixture.typed.reason) : null,
  );
  const [taken, setTaken] = useState<CloneIntoFixture['taken'] | 'unsaid' | null>(
    fixture?.taken ?? null,
  );
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

  const initial = fixture ? fixture.listings[fixture.start] : undefined;
  const browser = useDirBrowser(
    fixture ? null : props.deviceId,
    async (path, signal) => {
      if (fixture) {
        const all = Object.values(fixture.listings);
        const hit =
          path === ROOT_PATH ? all.find((l) => l.parent === null) : fixture.listings[path];
        if (!hit) throw new ApiError(400, 'invalid-request', 'not a directory');
        return hit;
      }
      return await fetchDeviceDirs(props.deviceId, path === ROOT_PATH ? undefined : path, signal);
    },
    initial,
  );
  const { listing } = browser;

  // Start in the folder of the suggestion; the browse root when the agent cannot list it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: once, when the dialog opens
  useEffect(() => {
    if (fixture) return;
    void browser.open(dirname(props.current)).then((r) => {
      if (!r.ok) void browser.open(ROOT_PATH);
    });
  }, []);

  const root = listing?.root ?? '/';
  const folder = listing ? displayPath(listing.path, listing.root) : '';
  const clean = name.trim();
  const nameBad = clean === '' || clean.includes('/') || clean === '.' || clean === '..';
  const destination = listing && !nameBad ? childPath(listing.path, clean) : null;
  const refusal =
    typedError !== null
      ? browseErrorText(typedError, deviceName)
      : browser.error
        ? browseErrorText(browser.error, deviceName)
        : taken
          ? destinationText(taken === 'unsaid' ? undefined : taken, {
              name: clean,
              folder,
              device: deviceName,
            })
          : failure;

  function clear() {
    setTaken(null);
    setFailure(null);
  }

  async function open(path: string) {
    clear();
    setTypedError(null);
    const result = await browser.open(path);
    if (result.ok) requestAnimationFrame(() => listRef.current?.focus());
  }

  async function enterPath() {
    const target = resolveTyped(text, root);
    if (target === null) return;
    setTypedError(null);
    const result = await browser.open(target);
    if (result.ok) {
      clear();
      setEditing(false);
      requestAnimationFrame(() => listRef.current?.focus());
    } else setTypedError(result.error);
  }

  const ready = destination !== null && !busy && !refusal;
  async function submit() {
    if (!ready || !destination) return;
    setBusy(true);
    clear();
    try {
      const offer = await props.check(destination);
      if (offer?.reason === 'ok') {
        props.onChosen();
        return;
      }
      if (offer?.reason === 'destination-taken') setTaken(offer.detail ?? 'unsaid');
      else
        setFailure(
          t('{device} cannot clone into {folder}. Pick another folder.')
            .replace('{device}', deviceName)
            .replace('{folder}', folder),
        );
    } catch (err) {
      setFailure(errorText(err));
    }
    setBusy(false);
  }

  function onListKey(e: KeyboardEvent) {
    if (e.key === 'ArrowLeft' && listing?.parent) {
      e.preventDefault();
      void open(listing.parent);
    }
  }

  const crumbs = listing ? breadcrumbs(listing.path, listing.root) : [];
  const entries = listing?.entries ?? [];

  return (
    <Dialog
      label={t('Clone into')}
      size="wide"
      onClose={props.onClose}
      data-testid="workspace.clone-dialog"
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: ⌘↩ is a dialog-wide shortcut */}
      <div
        className="ns add"
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            void submit();
          }
        }}
      >
        <div className="ns-head">
          <b>{t('Clone into')}</b>
          <IconButton
            label={t('Close')}
            hint="Esc"
            icon={Icon.close()}
            onClick={props.onClose}
            data-testid="workspace.clone-close"
          />
        </div>
        <div className="ns-grid">
          <span className="ns-label">{t('Machine')}</span>
          <Select
            label={t('Machine')}
            value={deviceName}
            options={[{ value: deviceName, label: deviceName, leading: <span className="dot" /> }]}
            onChange={() => undefined}
            disabled
          />
          <span className="ns-label ns-label-top">{t('Folder')}</span>
          <div className={`dirs${refusal ? ' bad' : ''}`}>
            <div className="pathbar">
              {editing ? (
                <TextField
                  mono
                  bare
                  label={t('Folder path')}
                  placeholder="~/github"
                  value={text}
                  spellCheck={false}
                  autoComplete="off"
                  onChange={(e) => {
                    setText(e.target.value);
                    setTypedError(null);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey) {
                      e.preventDefault();
                      void enterPath();
                    } else if (e.key === 'Escape') {
                      e.stopPropagation();
                      setEditing(false);
                      setTypedError(null);
                    }
                  }}
                  onBlur={() => {
                    setEditing(false);
                    setTypedError(null);
                  }}
                  autoFocus
                  data-autofocus
                  data-testid="workspace.clone-path"
                />
              ) : (
                <nav className="crumbs" aria-label={t('Folder path')}>
                  {crumbs.map((c, i) => (
                    <span key={c.path} className="crumb-item">
                      {i > 0 ? <span className="crumb-sep">/</span> : null}
                      <button
                        type="button"
                        className="crumb"
                        aria-current={i === crumbs.length - 1 ? 'location' : undefined}
                        onClick={() => void open(c.path)}
                        data-testid="workspace.clone-crumb"
                      >
                        {c.label}
                      </button>
                    </span>
                  ))}
                  <button
                    type="button"
                    className="crumb-edit"
                    aria-label={t('Edit path')}
                    onClick={() => {
                      setText(listing ? displayPath(listing.path, listing.root) : '');
                      setEditing(true);
                    }}
                    data-testid="workspace.clone-edit"
                  />
                </nav>
              )}
            </div>
            <div className="dir-body">
              <div
                ref={listRef}
                role="listbox"
                tabIndex={0}
                aria-label={t('Folders on {name}').replace('{name}', deviceName)}
                aria-busy={browser.loading}
                className="dir-list"
                onKeyDown={onListKey}
                data-autofocus
                data-testid="workspace.clone-list"
              >
                {entries.map((e) => (
                  // biome-ignore lint/a11y/useKeyWithClickEvents: the listbox owns the keys
                  <div
                    key={e.name}
                    role="option"
                    tabIndex={-1}
                    aria-selected={e.name === clean}
                    className={`dir${e.name === clean ? ' on' : ''}`}
                    onMouseDown={(ev) => {
                      ev.preventDefault();
                      listRef.current?.focus();
                    }}
                    onClick={() => listing && void open(childPath(listing.path, e.name))}
                    data-testid="workspace.clone-dir"
                  >
                    <span className="dir-icon">{Icon.folder()}</span>
                    <span className="dir-text">
                      <span className="dir-name">{e.name}</span>
                      {e.git ? (
                        <span className="dir-git" title={t('Git repository')}>
                          {t('git')}
                        </span>
                      ) : null}
                    </span>
                    <span className="dir-open">{Icon.chevron()}</span>
                  </div>
                ))}
              </div>
              {browser.loading ? (
                <p className="dir-note" role="status">
                  <Spinner />
                  {t('Loading folders…')}
                </p>
              ) : null}
              {listing && !browser.loading && entries.length === 0 ? (
                <p className="dir-note">{t('No folders here')}</p>
              ) : null}
            </div>
          </div>
          <span className="ns-label">{t('Name')}</span>
          <TextField
            label={t('Name')}
            mono
            value={name}
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => {
              setName(e.target.value);
              clear();
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.metaKey && !e.ctrlKey) {
                e.preventDefault();
                void submit();
              }
            }}
            data-testid="workspace.clone-name"
          />
          {refusal ? (
            <p className="ns-error" role="alert" data-testid="workspace.clone-error">
              {refusal}
            </p>
          ) : null}
        </div>
        <div className="ns-foot">
          <Button onClick={props.onClose} data-testid="workspace.clone-cancel">
            {t('Cancel')}
          </Button>
          <Button
            variant="primary"
            icon={busy ? <Spinner /> : undefined}
            disabled={!ready}
            title={destination ?? undefined}
            onClick={() => void submit()}
            data-testid="workspace.clone-use"
          >
            {destination && listing ? (
              <UsePath path={displayPath(destination, listing.root)} />
            ) : (
              <span className="ns-add">{t('Use this folder')}</span>
            )}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

/** "Use {path}" with the path losing its start, not its end, when it does not fit. */
function UsePath(props: { path: string }) {
  const [before = '', after = ''] = t('Use {path}').split('{path}');
  return (
    <span className="ns-add ns-add-path">
      {before}
      <span className="path-tail" title={props.path}>
        <bdi>{props.path}</bdi>
      </span>
      {after}
    </span>
  );
}
