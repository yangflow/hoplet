import { Fragment, type ReactNode } from 'react';
import { Icon } from '../../../components/icons.tsx';
import { t } from '../../../lib/i18n.ts';
import { pathLeaf } from '../group.ts';

/**
 * The composer's first row: where the session runs, as machine / directory, ruled off from the field. In a
 * draft the parts are pickers; once the session exists they are read only.
 */
export function WhereRow(props: { children: ReactNode[] }) {
  return (
    <div className="where" data-testid="cockpit.where">
      {props.children.map((part, i) => (
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

/** Where an existing session runs: its machine and its directory, read only. */
export function SessionWhere(props: { deviceName: string; online: boolean; path: string }) {
  const presence = props.online ? t('Online') : t('Offline');
  return (
    <WhereRow>
      {[
        <span key="m" className="where-part" title={`${props.deviceName} · ${presence}`}>
          <span className={`dot${props.online ? '' : ' off'}`} />
          {props.deviceName}
        </span>,
        <span key="d" className="where-part" title={props.path}>
          {Icon.folder()}
          {pathLeaf(props.path) || props.path}
        </span>,
      ]}
    </WhereRow>
  );
}
