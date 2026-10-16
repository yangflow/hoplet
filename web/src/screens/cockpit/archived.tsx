import type { LiveSessionMeta } from '@hoplet/shared';
import { useEffect } from 'react';
import { Button } from '../../components/Button.tsx';
import { IconButton } from '../../components/IconButton.tsx';
import { Icon } from '../../components/icons.tsx';
import { ProviderMark } from '../../components/provider-marks.tsx';
import { t } from '../../lib/i18n.ts';
import { pageKeysSuspended } from '../../lib/page-keys.ts';
import { type DeviceGroup, displayTitle } from './group.ts';
import { compactRel } from './sidebar/rel.ts';

/** The archived sessions of the groups, as the page counts them. */
export function archivedCount(groups: readonly DeviceGroup[]): number {
  return groups.reduce((n, g) => n + g.dirs.reduce((m, d) => m + d.sessions.length, 0), 0);
}

/** Keeps the groups and directories that hold sessions. */
export function withSessions(groups: DeviceGroup[]): DeviceGroup[] {
  return groups
    .map((g) => ({ ...g, dirs: g.dirs.filter((d) => d.sessions.length > 0) }))
    .filter((g) => g.dirs.length > 0);
}

function Row(props: { session: LiveSessionMeta; now: number; onRestore: (ids: string[]) => void }) {
  const { session } = props;
  const title = displayTitle(session);
  return (
    <div className="arow" data-testid="cockpit.archived-session">
      <ProviderMark provider={session.provider} />
      <span className="t1" title={title}>
        {title}
      </span>
      <IconButton
        label={t('Restore')}
        icon={Icon.restore()}
        onClick={() => props.onRestore([session.sessionId])}
        data-testid="cockpit.restore"
      />
      <span className="m">{compactRel(session.updatedAt, props.now)}</span>
    </div>
  );
}

/**
 * The Archived page in place of the conversation: every archived session by machine, then folder (name, path,
 * count), newest first. Offline machines are listed and restorable. Restoring needs no confirmation.
 */
export function ArchivedPage(props: {
  /** Machines with their folders; only the archived sessions. */
  groups: DeviceGroup[];
  now: number;
  onRestore: (ids: string[]) => void;
  onBack: () => void;
}) {
  const { groups, onBack } = props;
  const total = archivedCount(groups);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (pageKeysSuspended()) return;
      if (e.key === 'Escape' && !document.querySelector('[role="dialog"]')) onBack();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onBack]);
  return (
    <>
      <header className="top" data-testid="cockpit.archived-page">
        <IconButton
          label={t('Back to the session')}
          hint="Esc"
          icon={Icon.back()}
          className="back"
          onClick={onBack}
          data-testid="cockpit.archived-back"
        />
        <h1 className="title">{t('Archived')}</h1>
        {total > 0 ? <span className="cnt2">{total}</span> : null}
      </header>
      {total === 0 ? (
        <div className="empty" data-testid="cockpit.archived-empty">
          <div className="empty-in">
            <span className="tile" aria-hidden="true">
              {Icon.archive()}
            </span>
            <p className="empty-title">{t('No archived sessions')}</p>
            <span className="empty-keys">
              <span>{t('Archive from the ··· menu')}</span>
              <span>⇧⌘A</span>
            </span>
          </div>
        </div>
      ) : (
        <div className="arch-scroll">
          <div className="agp">
            {groups.map((g) => (
              <section
                key={g.name}
                className={g.online ? undefined : 'agoff'}
                data-testid="cockpit.archived-machine"
              >
                <div className={`agm${g.online ? '' : ' off'}`}>
                  <span className={`dot${g.online ? '' : ' off'}`} />
                  <span className="dn">{g.name}</span>
                  {g.online ? null : <span className="tag">{t('Offline')}</span>}
                  <span className="cnt">{archivedCount([g])}</span>
                </div>
                {g.dirs.map((d) => (
                  <div key={d.path} data-testid="cockpit.archived-folder">
                    <div className="agf">
                      <span className="fi">{Icon.folderOpen()}</span>
                      <span className="dn">{d.name}</span>
                      <span className="pth">{d.path}</span>
                      <span className="rt">
                        <Button
                          variant="quiet"
                          icon={Icon.restore()}
                          className="restore-all"
                          onClick={() => props.onRestore(d.sessions.map((s) => s.sessionId))}
                          data-testid="cockpit.restore-all"
                        >
                          {t('Restore all')}
                        </Button>
                        <span className="cnt">{d.sessions.length}</span>
                      </span>
                    </div>
                    {d.sessions.map((s) => (
                      <Row
                        key={s.sessionId}
                        session={s}
                        now={props.now}
                        onRestore={props.onRestore}
                      />
                    ))}
                  </div>
                ))}
              </section>
            ))}
          </div>
        </div>
      )}
    </>
  );
}
