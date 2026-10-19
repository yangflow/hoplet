import type { DeviceDirsResult } from '@hoplet/shared';
import { type KeyboardEvent, useEffect, useMemo, useRef, useState } from 'react';
import { Button } from '../../../components/Button.tsx';
import { Dialog } from '../../../components/Dialog.tsx';
import { IconButton } from '../../../components/IconButton.tsx';
import { Icon, Spinner } from '../../../components/icons.tsx';
import { Select } from '../../../components/Select.tsx';
import { TextField } from '../../../components/TextField.tsx';
import { fetchDeviceDirs, fetchDeviceProjects } from '../../../lib/api.ts';
import {
  breadcrumbs,
  childPath,
  displayPath,
  moveIndex,
  type NavKey,
  resolveTyped,
} from '../../../lib/browse.ts';
import { ApiError, addProjectErrorText, browseErrorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';
import { ROOT_PATH, useDirBrowser } from '../../../lib/use-dir-browser.ts';
import { pathLeaf } from '../group.ts';
import '../../../components/dir-browser.css';

/** How many of a machine's candidates the Recent group shows. */
const RECENT_MAX = 5;
const ADD_MACHINE = '\u0000add-machine';

export type AddProjectMachine = { deviceId: string | null; name: string; online: boolean };

/** Development fixture: the listings a machine would answer, where to start and what is already chosen. */
export type AddProjectFixture = {
  listings: Record<string, DeviceDirsResult>;
  start: string;
  candidates: string[];
  selected?: string;
  /** A typed path the agent refused: the text in the field and the reason it gave. */
  typed?: { text: string; reason: string };
};

type Row = {
  key: string;
  path: string;
  name: string;
  group: 'recent' | 'dir';
  git: boolean;
  /** Where a recent folder lives, as `~/…`. */
  where: string;
};

/**
 * Add project as a centred dialog: pick a machine, browse its directories from the agent's browse root (or type
 * a path) and add the chosen one to the agent's watched projects.
 */
export function AddProject(props: {
  machines: AddProjectMachine[];
  /** The machine the plus was pressed on. */
  initialMachine: string;
  /** Adds the path; resolves when the agent has watched it, rejects with the reason it could not. */
  onSubmit: (deviceId: string, path: string) => Promise<void>;
  onClose: () => void;
  fixture?: AddProjectFixture;
}) {
  const { machines, fixture } = props;
  const [machineName, setMachineName] = useState(props.initialMachine);
  const machine = machines.find((m) => m.name === machineName) ?? machines[0];
  const deviceId = machine?.deviceId ?? null;
  const deviceName = machine?.name ?? '';
  const [hint, setHint] = useState(false);
  const [selected, setSelected] = useState<string | null>(fixture?.selected ?? null);
  const [editing, setEditing] = useState(fixture?.typed !== undefined);
  const [text, setText] = useState(fixture?.typed?.text ?? '');
  const [typedError, setTypedError] = useState<unknown>(
    fixture?.typed ? new ApiError(400, 'invalid-request', fixture.typed.reason) : null,
  );
  const [busy, setBusy] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [candidates, setCandidates] = useState<string[]>(fixture?.candidates ?? []);
  const listRef = useRef<HTMLDivElement>(null);

  const initial = fixture ? fixture.listings[fixture.start] : undefined;
  const browser = useDirBrowser(
    fixture || !machine?.online ? null : deviceId,
    async (path, signal) => {
      if (fixture) {
        const all = Object.values(fixture.listings);
        const hit =
          path === ROOT_PATH ? all.find((l) => l.parent === null) : fixture.listings[path];
        if (!hit) throw new ApiError(400, 'invalid-request', 'not a directory');
        return hit;
      }
      if (!deviceId) throw new ApiError(409, 'target-offline');
      return await fetchDeviceDirs(deviceId, path === ROOT_PATH ? undefined : path, signal);
    },
    initial,
  );
  const { listing } = browser;

  useEffect(() => {
    if (fixture || !deviceId) return;
    let cancelled = false;
    setCandidates([]);
    fetchDeviceProjects(deviceId)
      .then((catalog) => {
        if (cancelled || !catalog) return;
        const watched = new Set(catalog.projects.map((p) => p.path));
        setCandidates(catalog.candidates.filter((c) => !watched.has(c)));
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [deviceId, fixture]);

  const root = listing?.root ?? '/';
  const atRoot = listing !== null && listing.parent === null;
  const rows = useMemo<Row[]>(() => {
    if (!listing) return [];
    const recent: Row[] = atRoot
      ? candidates.slice(0, RECENT_MAX).map((path) => ({
          key: `recent:${path}`,
          path,
          name: pathLeaf(path),
          group: 'recent' as const,
          git: false,
          where: displayPath(path.slice(0, path.lastIndexOf('/')) || '/', listing.root),
        }))
      : [];
    const dirs: Row[] = listing.entries.map((e) => ({
      key: `dir:${e.name}`,
      path: childPath(listing.path, e.name),
      name: e.name,
      group: 'dir' as const,
      git: e.git,
      where: '',
    }));
    return [...recent, ...dirs];
  }, [listing, candidates, atRoot]);
  const index = rows.findIndex((r) => r.path === selected);

  /** Opens a directory; opening one selects it, except the root. */
  async function openDir(path: string): Promise<boolean> {
    setSubmitError(null);
    const result = await browser.open(path);
    if (!result.ok) return false;
    setSelected(result.listing.parent === null ? null : result.listing.path);
    // the row or crumb that had focus is gone; keep the keys working
    requestAnimationFrame(() => listRef.current?.focus());
    return true;
  }

  function goUp() {
    if (listing?.parent) void openDir(listing.parent);
  }

  function pick(next: number) {
    const row = rows[next];
    if (!row) return;
    setSelected(row.path);
    requestAnimationFrame(() =>
      listRef.current
        ?.querySelector<HTMLElement>(`[data-row="${next}"]`)
        ?.scrollIntoView?.({ block: 'nearest' }),
    );
  }

  function onListKey(e: KeyboardEvent) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Home' || e.key === 'End') {
      pick(moveIndex(index, rows.length, e.key as NavKey));
    } else if ((e.key === 'Enter' || e.key === 'ArrowRight') && selected && index >= 0) {
      void openDir(selected);
    } else if (e.key === 'ArrowLeft') {
      goUp();
    } else return;
    e.preventDefault();
  }

  function onKeyDown(e: KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      void submit();
    } else if (e.key === 'Backspace' && !(e.target instanceof HTMLInputElement)) {
      e.preventDefault();
      goUp();
    }
  }

  async function enterPath() {
    const target = resolveTyped(text, root);
    if (target === null) return;
    setTypedError(null);
    const result = await browser.open(target);
    if (result.ok) {
      setSelected(result.listing.parent === null ? null : result.listing.path);
      setEditing(false);
      requestAnimationFrame(() => listRef.current?.focus());
    } else setTypedError(result.error);
  }

  function chooseMachine(value: string) {
    if (value === ADD_MACHINE) {
      setHint(true);
      return;
    }
    setHint(false);
    setMachineName(value);
    setSelected(null);
    setEditing(false);
    setTypedError(null);
    setSubmitError(null);
  }

  const ready = selected !== null && !busy && !!deviceId;
  async function submit() {
    if (!ready || !selected || !deviceId) return;
    setBusy(true);
    setSubmitError(null);
    try {
      await props.onSubmit(deviceId, selected);
    } catch (err) {
      setSubmitError(addProjectErrorText(err, deviceName));
      setBusy(false);
    }
  }

  const machineOptions = [
    ...machines.map((m) => ({
      value: m.name,
      label: m.online ? m.name : `${m.name} · ${t('offline')}`,
      disabled: !m.online,
      leading: <span className={`dot${m.online ? '' : ' off'}`} aria-hidden="true" />,
    })),
    { value: ADD_MACHINE, label: t('Add a machine'), leading: Icon.plus() },
  ];
  const crumbs = listing ? breadcrumbs(listing.path, listing.root) : [];
  const shownError = typedError ?? browser.error;
  const errorText = submitError ?? (shownError ? browseErrorText(shownError, deviceName) : null);
  const recentRows = rows.filter((r) => r.group === 'recent');
  const dirRows = rows.filter((r) => r.group === 'dir');

  const renderRow = (row: Row) => {
    const at = rows.indexOf(row);
    return (
      // biome-ignore lint/a11y/useKeyWithClickEvents: the listbox owns the keys (aria-activedescendant)
      <div
        key={row.key}
        id={`add-row-${at}`}
        data-row={at}
        role="option"
        tabIndex={-1}
        aria-selected={row.path === selected}
        className={`dir${row.path === selected ? ' on' : ''}`}
        onMouseDown={(e) => {
          // The list keeps the focus (aria-activedescendant): a focused row would vanish when a folder opens,
          // taking the focus to the page, and the next key would miss the dialog.
          e.preventDefault();
          listRef.current?.focus();
        }}
        onClick={() => setSelected(row.path)}
        onDoubleClick={() => void openDir(row.path)}
        data-testid={row.group === 'recent' ? 'cockpit.add-recent' : 'cockpit.add-dir'}
      >
        <span className="dir-icon">{Icon.folder()}</span>
        <span className="dir-text">
          <span className="dir-name">{row.name}</span>
          {row.git ? (
            <span className="dir-git" title={t('Git repository')}>
              {t('git')}
            </span>
          ) : null}
          {row.where ? <span className="dir-where">{row.where}</span> : null}
        </span>
        <span className="dir-open">{Icon.chevron()}</span>
      </div>
    );
  };

  return (
    <Dialog
      label={t('Add project')}
      size="wide"
      onClose={props.onClose}
      data-testid="cockpit.add-dialog"
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: Backspace and ⌘↩ are dialog-wide shortcuts */}
      <div className="ns add" onKeyDown={onKeyDown}>
        <div className="ns-head">
          <b>{t('Add project')}</b>
          <IconButton
            label={t('Close')}
            hint="Esc"
            icon={Icon.close()}
            onClick={props.onClose}
            data-testid="cockpit.add-close"
          />
        </div>
        <div className="ns-grid">
          <span className="ns-label">{t('Machine')}</span>
          <Select
            label={t('Machine')}
            value={machineName}
            options={machineOptions}
            onChange={chooseMachine}
            data-testid="cockpit.add-machine"
          />
          {hint ? (
            <p className="ns-hint" role="status" data-testid="cockpit.add-hint">
              {t('Run hoplet devices add on the host to connect another machine.')}
            </p>
          ) : null}
          <span className="ns-label ns-label-top">{t('Folder')}</span>
          <div className={`dirs${errorText ? ' bad' : ''}`}>
            <div className="pathbar">
              {editing ? (
                <TextField
                  mono
                  bare
                  label={t('Folder path')}
                  placeholder="~/github/repopro"
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
                  data-testid="cockpit.add-path"
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
                        onClick={() => void openDir(c.path)}
                        data-testid="cockpit.add-crumb"
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
                    data-testid="cockpit.add-edit"
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
                aria-activedescendant={index >= 0 ? `add-row-${index}` : undefined}
                className="dir-list"
                onKeyDown={onListKey}
                data-autofocus
                data-testid="cockpit.add-list"
              >
                {recentRows.length > 0 ? (
                  // biome-ignore lint/a11y/useSemanticElements: a group of options inside a listbox
                  <div role="group" aria-label={t('Recent')}>
                    <div className="dir-head" aria-hidden="true">
                      {t('Recent')}
                    </div>
                    {recentRows.map(renderRow)}
                  </div>
                ) : null}
                {dirRows.length > 0 ? (
                  // biome-ignore lint/a11y/useSemanticElements: a group of options inside a listbox
                  <div role="group" aria-label={t('Folders')}>
                    {recentRows.length > 0 ? (
                      <div className="dir-head" aria-hidden="true">
                        {t('Folders')}
                      </div>
                    ) : null}
                    {dirRows.map(renderRow)}
                  </div>
                ) : null}
              </div>
              {browser.loading ? (
                <p className="dir-note" role="status" data-testid="cockpit.add-loading">
                  <Spinner />
                  {t('Loading folders…')}
                </p>
              ) : null}
              {listing && !browser.loading && dirRows.length === 0 ? (
                <p className="dir-note" data-testid="cockpit.add-empty">
                  {t('No folders here')}
                </p>
              ) : null}
              {listing?.truncated && !browser.loading ? (
                <p className="dir-note" data-testid="cockpit.add-truncated">
                  {t('Showing {n} of more. Type a path to go further.').replace(
                    '{n}',
                    String(listing.entries.length),
                  )}
                </p>
              ) : null}
            </div>
          </div>
          {errorText ? (
            <p className="ns-error" role="alert" data-testid="cockpit.add-error">
              {errorText}
            </p>
          ) : null}
        </div>
        <div className="ns-foot">
          <Button onClick={props.onClose} data-testid="cockpit.add-cancel">
            {t('Cancel')}
          </Button>
          <Button
            variant="primary"
            icon={busy ? <Spinner /> : undefined}
            disabled={!ready}
            title={selected ?? undefined}
            onClick={() => void submit()}
            data-testid="cockpit.add-submit"
          >
            <span className="ns-add">
              {selected ? t('Add {name}').replace('{name}', pathLeaf(selected)) : t('Add project')}
            </span>
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
