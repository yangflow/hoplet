import type { ChangeStatus, WorkspaceChange } from '@hoplet/shared';
import { useState } from 'react';
import { FileBadge, fileKind } from '../../components/file-types.tsx';
import { Icon } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';
import {
  changeSummary,
  formatCount,
  STATUS_LETTER,
  splitPath,
  type TreeNode,
  visibleRows,
} from './model.ts';

/** The name of a git status, for tooltips and assistive technology. */
export function statusName(status: ChangeStatus): string {
  return {
    modified: t('Modified'),
    added: t('Added'),
    deleted: t('Deleted'),
    untracked: t('Untracked'),
  }[status];
}

/** A status letter in its colour; its name is the tooltip and the accessible name. */
export function StatusTag({ status }: { status: ChangeStatus }) {
  return (
    <span
      className={`ws-tag g-${status}`}
      role="img"
      aria-label={statusName(status)}
      title={statusName(status)}
    >
      {STATUS_LETTER[status]}
    </span>
  );
}

/** Added and removed line counts, or `binary`. */
export function Counts({ change }: { change: WorkspaceChange }) {
  if (change.binary) return <span className="ws-bin">{t('binary')}</span>;
  return (
    <>
      {change.added ? <span className="add-n">+{formatCount(change.added)}</span> : null}
      {change.removed ? <span className="del-n">−{formatCount(change.removed)}</span> : null}
    </>
  );
}

/**
 * Changes: every changed path with its status, directory and counts under a summary row; `compact` (beside the
 * viewer) keeps the name and the letter.
 */
export function ChangesList(props: {
  changes: readonly WorkspaceChange[];
  selected: string | null;
  compact: boolean;
  onOpen: (path: string) => void;
}) {
  const { changes, compact } = props;
  const sum = changeSummary(changes);
  return (
    <div className="ws-list" data-testid="workspace.changes">
      {compact ? null : (
        <div className="ws-sum" data-testid="workspace.changes-summary">
          <span>
            {(sum.files === 1 ? t('{n} file') : t('{n} files')).replace(
              '{n}',
              formatCount(sum.files),
            )}
          </span>
          <i aria-hidden="true">·</i>
          <span className="add-n">+{formatCount(sum.added)}</span>
          <span className="del-n">−{formatCount(sum.removed)}</span>
        </div>
      )}
      <ul>
        {changes.map((c) => {
          const { dir, name } = splitPath(c.path);
          return (
            <li key={c.path}>
              <button
                type="button"
                className={`ws-row ws-change g-${c.status}${c.path === props.selected ? ' on' : ''}`}
                title={`${c.path} · ${statusName(c.status)}`}
                aria-current={c.path === props.selected ? 'true' : undefined}
                onClick={() => props.onOpen(c.path)}
                data-testid="workspace.change"
              >
                <FileBadge kind={fileKind(name)} />
                <span className="ws-name">{name}</span>
                <span className="ws-dir">{compact ? '' : dir}</span>
                {compact ? null : (
                  <span className="ws-counts">
                    <Counts change={c} />
                  </span>
                )}
                <StatusTag status={c.status} />
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** Files: the working tree as folders that fold; a folder holding changes is open at first and carries a dot. */
export function FilesTree(props: {
  nodes: readonly TreeNode[];
  selected: string | null;
  onOpen: (path: string) => void;
}) {
  const [toggled, setToggled] = useState<ReadonlyMap<string, boolean>>(new Map());
  const isOpen = (n: TreeNode) => toggled.get(n.path) ?? n.holdsChanges === true;
  const rows = visibleRows(props.nodes, isOpen);
  return (
    <ul className="ws-list ws-tree" data-testid="workspace.files">
      {rows.map((n) => {
        const indent = { paddingLeft: `calc(var(--space-1) + ${n.depth} * var(--space-3))` };
        if (n.children) {
          const open = isOpen(n);
          return (
            <li key={n.path}>
              <button
                type="button"
                className="ws-row ws-file"
                style={indent}
                aria-expanded={open}
                title={n.holdsChanges ? `${n.path} · ${t('Holds changes')}` : n.path}
                onClick={() => setToggled((m) => new Map(m).set(n.path, !open))}
                data-testid="workspace.folder"
              >
                <span className="ws-chev">{open ? Icon.down() : Icon.chevron()}</span>
                <FileBadge kind="folder" />
                <span className="ws-name">{n.name}</span>
                {n.holdsChanges ? (
                  <span className="ws-tag ws-holds" role="img" aria-label={t('Holds changes')} />
                ) : null}
              </button>
            </li>
          );
        }
        return (
          <li key={n.path}>
            <button
              type="button"
              className={`ws-row ws-file${n.status ? ` g-${n.status}` : ''}${n.path === props.selected ? ' on' : ''}`}
              style={indent}
              title={n.status ? `${n.path} · ${statusName(n.status)}` : n.path}
              aria-current={n.path === props.selected ? 'true' : undefined}
              onClick={() => props.onOpen(n.path)}
              data-testid="workspace.file"
            >
              <span className="ws-chev" />
              <FileBadge kind={fileKind(n.name)} />
              <span className="ws-name">{n.name}</span>
              {n.status ? <StatusTag status={n.status} /> : null}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
