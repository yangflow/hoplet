import type { DeviceSystem } from '@hoplet/shared';
import { Fragment, type ReactNode } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { MachineIcon } from '../../../components/MachineIcon.tsx';
import { t } from '../../../lib/i18n.ts';
import { machineLabel, systemOf } from '../../../lib/system.ts';
import { pathLeaf } from '../group.ts';

/**
 * The composer's first row: where the session runs, as machine / directory / branch, ruled off from the field.
 * In a draft machine and directory are pickers; once the session exists every part is read only. A part that
 * is not known (null) is left out with its separator.
 */
export function WhereRow(props: { children: ReactNode[] }) {
  const parts = props.children.filter(
    (part) => part !== null && part !== undefined && part !== false,
  );
  return (
    <div className="where" data-testid="cockpit.where">
      {parts.map((part, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: the parts keep their order
        <Fragment key={i}>
          {i > 0 ? (
            <span className="where-sep" aria-hidden="true">
              /
            </span>
          ) : null}
          {part}
        </Fragment>
      ))}
    </div>
  );
}

/** The git branch of a directory, read only; nothing when it is not known. */
export function BranchPart(props: { branch: string | undefined; path: string }) {
  if (!props.branch) return null;
  return (
    <span
      className="where-part"
      title={t('Git branch of {path}').replace('{path}', props.path)}
      data-testid="cockpit.where-branch"
    >
      {Icon.branch()}
      {props.branch}
    </span>
  );
}

/** Where an existing session runs: its machine, its directory and its git branch, read only. */
export function SessionWhere(props: {
  deviceName: string;
  online: boolean;
  system?: DeviceSystem | null | undefined;
  path: string;
  branch?: string | undefined;
}) {
  return (
    <WhereRow>
      {[
        <span
          key="m"
          className="where-part"
          title={machineLabel(props.deviceName, props.system, props.online)}
        >
          <MachineIcon kind={systemOf(props.system?.os)} online={props.online} />
          {props.deviceName}
        </span>,
        <span key="d" className="where-part" title={props.path}>
          {Icon.folder()}
          {pathLeaf(props.path) || props.path}
        </span>,
        props.branch ? <BranchPart key="b" branch={props.branch} path={props.path} /> : null,
      ]}
    </WhereRow>
  );
}
